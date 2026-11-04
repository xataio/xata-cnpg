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
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/util/wait"
)

// OrphansReaper collects detached pgBackRest workers adopted by the instance manager.
type OrphansReaper struct{}

// Start runs orphan cleanup while the instance manager is PID 1.
func (*OrphansReaper) Start(ctx context.Context) error {
	if os.Getpid() != 1 {
		return nil
	}
	session, err := unix.Getsid(0)
	if err != nil {
		return err
	}

	// Polling bounds cleanup work when archiving produces frequent child exits.
	// The first sweep also collects workers inherited across a manager upgrade.
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := reapOrphans(session); err != nil {
			log.FromContext(ctx).Error(err, "while reaping pgbackrest workers, will retry")
		}
	}, 10*time.Second)
	return nil
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
	name, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)) //nolint:gosec // PID comes from procfs children.
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSuffix(string(name), "\n") != pgbackrestBinary {
		return nil
	}
	session, err := unix.Getsid(pid)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	// Async workers detach into a new session. Manager-owned commands and the
	// TLS server must inherit its session so their Go waiters keep exit status
	// ownership, including across manager upgrades.
	if session == managerSession {
		return nil
	}
	_, err = unix.Wait4(pid, nil, unix.WNOHANG, nil)
	if errors.Is(err, unix.ECHILD) || errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}
