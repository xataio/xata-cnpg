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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

// OrphansReaper collects detached pgBackRest workers adopted by the instance manager.
type OrphansReaper struct{}

// Start runs orphan cleanup while the instance manager is PID 1.
func (*OrphansReaper) Start(ctx context.Context) error {
	if os.Getpid() != 1 {
		return nil
	}
	_, session, err := readProcessStat("/proc/self/stat")
	if err != nil {
		return err
	}

	// Polling bounds cleanup work when archiving produces frequent child exits.
	// The first sweep also collects workers inherited across a manager upgrade.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := reapOrphans(session); err != nil {
			log.FromContext(ctx).Error(err, "while reaping pgbackrest workers, will retry")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func reapOrphans(managerSession int) error {
	// Go can launch children from any OS thread. Reading each thread's children
	// avoids scanning unrelated processes in the container.
	paths, err := filepath.Glob("/proc/self/task/*/children")
	if err != nil {
		return err
	}
	for _, path := range paths {
		children, err := os.ReadFile(path) //nolint:gosec // Paths come from the manager's procfs task directory.
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, child := range strings.Fields(string(children)) {
			pid, err := strconv.Atoi(child)
			if err != nil {
				return err
			}
			if err := reapOrphan(pid, managerSession); err != nil {
				return err
			}
		}
	}
	return nil
}

func reapOrphan(pid, managerSession int) error {
	name, session, err := readProcessStat(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Async pgBackRest workers detach into a new session. Commands and the TLS
	// server owned by the manager must inherit its session so their Go waiters
	// retain ownership of their exit status, including across manager upgrades.
	if name != pgbackrestBinary || session == managerSession {
		return nil
	}
	var status syscall.WaitStatus
	_, err = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if errors.Is(err, syscall.ECHILD) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func readProcessStat(path string) (name string, session int, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // Callers use procfs paths for the manager and its children.
	if err != nil {
		return "", 0, err
	}
	stat := string(data)
	start, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if start < 0 || end <= start {
		return "", 0, fmt.Errorf("invalid process stat in %s", path)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 4 {
		return "", 0, fmt.Errorf("missing process session in %s", path)
	}
	session, err = strconv.Atoi(fields[3])
	return stat[start+1 : end], session, err
}
