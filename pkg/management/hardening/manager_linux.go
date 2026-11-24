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

package hardening

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ProtectManager applies the features that concern the instance manager
// process. Call it first thing in the process: execve resets the dumpable
// flag, so an online upgrade starts a manager that is dumpable again while
// PostgreSQL runs.
func ProtectManager(features Features) error {
	if features.Has(NoDumpable) {
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			return fmt.Errorf("prctl(PR_SET_DUMPABLE): %w", err)
		}
	}
	// The move happens here and not in harden-exec, so that it cannot run
	// at the same time as an online upgrade, which replaces the binary.
	if features.usesLandlock() {
		if err := moveToOwnDir(managerPath, managerDir); err != nil {
			return fmt.Errorf("moving the instance manager binary: %w", err)
		}
	}
	return nil
}

// ManagerDumpable reports whether the instance manager can write the files
// in its own /proc/self directory. The kernel gives them to root when the
// process is not dumpable.
func ManagerDumpable() bool {
	dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	return err != nil || dumpable == 1
}
