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
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// logRotateSizeLimit is the per-file size cap for pgbackrest log files.
// pgbackrest has no log management of its own: packaged installations rely on
// an OS-level logrotate configuration, which does not exist in a container,
// so the instance manager takes that role.
const logRotateSizeLimit = 10 * 1024 * 1024

// RotateLogs bounds the pgbackrest log files on the PGDATA volume.
//
// pgbackrest names log files <stanza>-<command>.log. Clones inherit the PGDATA
// volume, so a child carries its parent's log files; without cleanup this
// accumulates one stanza per level down a clone chain. Files belonging to a
// foreign stanza are therefore deleted (all-server.log has no stanza and is
// kept).
//
// Files of the current stanza over the cap are copied to a single .old
// generation (overwriting the previous one) and truncated in place.
// Copy-then-truncate is required rather than rename: the long-running TLS
// server keeps all-server.log open, and a rename would leave it appending to
// the rotated file. Lines written between the copy and the truncate are lost;
// that window is negligible for log data.
func RotateLogs(pgDataPath, stanza string) error {
	logDir := filepath.Join(workingDir(pgDataPath), "log")

	entries, err := os.ReadDir(logDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	stanzaPrefix := stanza + "-"
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		path := filepath.Join(logDir, entry.Name())

		// Delete logs (and their .old sibling) that belong to another stanza,
		// inherited from a parent volume on clone.
		if !strings.HasPrefix(entry.Name(), stanzaPrefix) && entry.Name() != "all-server.log" {
			if err := os.Remove(path); err != nil {
				errs = append(errs, err)
			}
			if err := os.Remove(path + ".old"); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
			continue
		}

		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.Size() <= logRotateSizeLimit {
			continue
		}

		if err := copyTruncate(path, path+".old"); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// copyTruncate copies src to dst (replacing dst) and then truncates src in
// place, preserving any open file handles appending to src.
func copyTruncate(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // paths are derived from the fixed log directory
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // see above
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	return os.Truncate(src, 0)
}
