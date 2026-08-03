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
// an OS-level logrotate, which does not exist in a container. Rather than add
// logrotate plus a scheduler to the image — a CNPG pod deliberately has no
// cron/daemon — the instance manager rotates inline on config reconcile, where
// it already touches these files.
const logRotateSizeLimit = 10 * 1024 * 1024

// ownerMarkerFile records which stanza the log directory currently belongs to.
// It is how RotateLogs detects that a PGDATA volume changed owner — a clone,
// pool-cluster adoption, or restore — even when the previous owner left no
// stanza log files behind (e.g. a warm-pool cluster runs pgbackrest suspended
// and only writes all-server.log).
const ownerMarkerFile = ".stanza"

// RotateLogs bounds the pgbackrest log files on the PGDATA volume.
//
// pgbackrest names log files <stanza>-<command>.log. A PGDATA volume can carry
// another cluster's logs: clones inherit the parent's volume, and pool-cluster
// adoption reuses a warm cluster's volume. RotateLogs records the owning stanza
// in a marker file; when the marker is missing or names a different stanza, the
// volume changed owner, so it removes the previous owner's logs — every
// <other-stanza>-*.log plus a clean slate of all-server.log (which has no
// stanza in its name and so cannot be matched per-file). Two things are kept
// across an owner change: the current stanza's own files, and any
// <source-stanza>-restore.log, the one-time record of how the branch restored.
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

	// A missing marker is NOT treated as an owner change: it means either a
	// brand-new cluster (whose own history must be kept) or an existing cluster
	// on first run after this code ships (whose history must not be wiped
	// fleet-wide). Only a marker that exists and names a different stanza proves
	// the previous owner was someone else.
	markerPath := filepath.Join(logDir, ownerMarkerFile)
	prevOwner := readOwnerMarker(markerPath)
	ownerChanged := prevOwner != "" && prevOwner != stanza

	stanzaPrefix := stanza + "-"
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		path := filepath.Join(logDir, entry.Name())

		// On an owner change, delete the previous owner's logs (and their .old
		// sibling). Keep the current stanza's files and any -restore.log.
		if ownerChanged &&
			!strings.HasPrefix(entry.Name(), stanzaPrefix) &&
			entry.Name() != "all-server.log" &&
			!strings.HasSuffix(entry.Name(), "-restore.log") {
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

	if ownerChanged {
		// all-server.log has no stanza in its name, so it belonged to the
		// previous owner — truncate it for a clean slate.
		if err := os.Truncate(filepath.Join(logDir, "all-server.log"), 0); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}

	// Claim the directory whenever the marker does not already name us (missing
	// or changed), so subsequent reconciles see a match and leave everything —
	// including legitimate server-restart history — alone.
	if prevOwner != stanza {
		if err := writeOwnerMarker(markerPath, stanza); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// readOwnerMarker returns the stanza recorded in the marker file, or "" if it
// is missing or unreadable (treated as an owner change, i.e. cleanup runs).
func readOwnerMarker(path string) string {
	contents, err := os.ReadFile(path) //nolint:gosec // fixed path in the log directory
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

// writeOwnerMarker records stanza as the current owner of the log directory.
func writeOwnerMarker(path, stanza string) error {
	return os.WriteFile(path, []byte(stanza+"\n"), 0o600)
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
