/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package pgbackrest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const reaperTestMode = "CNPG_PGBACKREST_REAPER_TEST"

func TestOrphansReaper(t *testing.T) {
	if os.Getenv(reaperTestMode) != "supervisor" {
		runReaperTestSubprocess(t)
		return
	}

	// A separate subreaper process exercises Linux adoption without changing
	// which process receives orphaned children from the rest of the test suite.
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	session, err := unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(t.TempDir(), pgbackrestBinary)
	if err := os.Symlink(executable, worker); err != nil {
		t.Fatal(err)
	}

	t.Run("detached orphan", func(t *testing.T) {
		pid := startReaperTestOrphan(t, worker, "parent")
		waitForReaperTestZombie(t, pid)
		if err := reapOrphans(session); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); !os.IsNotExist(err) {
			t.Fatalf("orphan %d remains after cleanup: %v", pid, err)
		}
	})

	t.Run("managed command", func(t *testing.T) {
		cmd := reaperTestCommand(worker, "worker")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		waitForReaperTestZombie(t, cmd.Process.Pid)
		if err := reapOrphans(session); err != nil {
			t.Fatal(err)
		}
		var exitErr *exec.ExitError
		if err := cmd.Wait(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
			t.Fatalf("manager lost command exit status: %v", err)
		}
	})

	t.Run("other executable", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other)worker")
		if err := os.Symlink(executable, other); err != nil {
			t.Fatal(err)
		}
		pid := startReaperTestOrphan(t, other, "parent")
		waitForReaperTestZombie(t, pid)
		if err := reapOrphans(session); err != nil {
			t.Fatal(err)
		}
		var status syscall.WaitStatus
		if waited, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err != nil || waited != pid {
			t.Fatalf("unrelated orphan was collected: pid=%d, err=%v", waited, err)
		}
	})

	t.Run("running detached orphan", func(t *testing.T) {
		pid := startReaperTestOrphan(t, worker, "parent-block")
		if err := reapOrphans(session); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, 0); err != nil {
			t.Fatalf("running worker did not survive cleanup: %v", err)
		}
	})
}

func TestOrphansReaperStart(t *testing.T) {
	if os.Getpid() != 1 {
		t.Skip("requires the test binary to run as container PID 1")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(t.TempDir(), pgbackrestBinary)
	if err := os.Symlink(executable, worker); err != nil {
		t.Fatal(err)
	}
	pid := startReaperTestOrphan(t, worker, "parent")
	waitForReaperTestZombie(t, pid)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&OrphansReaper{}).Start(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan %d remains after starting the reaper", pid)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func runReaperTestSubprocess(t *testing.T) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOrphansReaper$", "-test.timeout=30s") //nolint:gosec
	cmd.Env = append(os.Environ(), reaperTestMode+"=supervisor")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("orphan reaper subprocess: %v\n%s", err, output)
	}
}

func reaperTestCommand(executable, mode string) *exec.Cmd {
	cmd := exec.Command(executable, "-test.run=^TestOrphansReaperHelper$") //nolint:gosec
	cmd.Env = append(os.Environ(), reaperTestMode+"="+mode)
	return cmd
}

func startReaperTestOrphan(t *testing.T, executable, mode string) int {
	t.Helper()
	output, err := reaperTestCommand(executable, mode).Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var status syscall.WaitStatus
		waited, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if waited == pid || errors.Is(err, syscall.ECHILD) {
			return
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_, _ = syscall.Wait4(pid, &status, 0, nil)
	})
	return pid
}

func waitForReaperTestZombie(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) //nolint:gosec
		if err == nil && strings.Contains(string(data), ") Z ") {
			// Wait until only main thread of Go test binary remains to collect.
			if tasks, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid)); len(tasks) <= 1 {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("process %d did not become a zombie", pid)
}

func TestOrphansReaperHelper(t *testing.T) {
	switch os.Getenv(reaperTestMode) {
	case "parent", "parent-block":
		mode := "worker"
		if os.Getenv(reaperTestMode) == "parent-block" {
			mode = "worker-block"
		}
		child := reaperTestCommand(os.Args[0], mode)
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Println(child.Process.Pid)
		os.Exit(0)
	case "worker":
		os.Exit(23)
	case "worker-block":
		time.Sleep(time.Hour)
	}
}
