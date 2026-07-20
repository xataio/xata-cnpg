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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	ps "github.com/mitchellh/go-ps"
)

func TestCheckForExistingTLSServer(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "server.pid")

	t.Run("returns nil when the PID file is absent", func(t *testing.T) {
		process, err := checkForExistingTLSServer(pidFile, pgbackrestBinary)
		if err != nil {
			t.Fatal(err)
		}
		if process != nil {
			t.Fatalf("expected no process, got PID %d", process.Pid)
		}
	})

	t.Run("removes a stale PID file", func(t *testing.T) {
		if err := os.WriteFile(pidFile, []byte("1073741824\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		process, err := checkForExistingTLSServer(pidFile, pgbackrestBinary)
		if err != nil {
			t.Fatal(err)
		}
		if process != nil {
			t.Fatalf("expected no process, got PID %d", process.Pid)
		}
		if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
			t.Fatalf("expected stale PID file to be removed, got %v", err)
		}
	})

	t.Run("returns the live server process", func(t *testing.T) {
		pid := os.Getpid()
		if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		currentProcess, err := ps.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}

		process, err := checkForExistingTLSServer(pidFile, currentProcess.Executable())
		if err != nil {
			t.Fatal(err)
		}
		if process == nil || process.Pid != pid {
			t.Fatalf("expected PID %d, got %v", pid, process)
		}
	})
}
