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
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// Exec applies the features and replaces the process with the program at
// path. argv is the complete argument list of the program, with argv[0].
// Exec returns only on failure. A feature that cannot be applied is an
// error: the caller must not start the program without it.
//
// A coredumpFilter that is not empty is written to
// /proc/self/coredump_filter first.
func Exec(features Features, coredumpFilter, path string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("missing argument list")
	}

	// Seccomp filters and no_new_privs belong to a thread. execve keeps
	// only the calling thread, so it must be the restricted one.
	runtime.LockOSThread()

	// The filter survives execve. Landlock makes /proc read-only, so the
	// write comes first.
	if coredumpFilter != "" {
		//nolint:gosec // the kernel owns the file
		if err := os.WriteFile("/proc/self/coredump_filter", []byte(coredumpFilter), 0o600); err != nil {
			return err
		}
	}

	// Landlock and seccomp need no_new_privs without CAP_SYS_ADMIN.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}

	for _, f := range features {
		if err := apply(f); err != nil {
			return fmt.Errorf("applying hardening feature %q: %w", f, err)
		}
	}

	return unix.Exec(path, argv, os.Environ())
}

// apply applies one feature of the PostgreSQL process tree.
func apply(f Feature) error {
	switch f {
	case MDWE:
		return applyMDWE()
	case Landlock:
		return applyLandlock()
	case LandlockExec:
		return applyLandlockExec()
	case LandlockNet:
		return applyLandlockNet()
	case SeccompLog:
		return applySeccomp(true)
	case Seccomp:
		return applySeccomp(false)
	default:
		return errors.New("the feature does not apply to a process tree")
	}
}
