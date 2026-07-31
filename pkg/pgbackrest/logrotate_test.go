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
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotateLogs(t *testing.T) {
	pgData := filepath.Join(t.TempDir(), "pgdata")
	logDir := filepath.Join(workingDir(pgData), "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}

	small := filepath.Join(logDir, "stanza-small.log")
	if err := os.WriteFile(small, []byte("small\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	big := filepath.Join(logDir, "stanza-big.log")
	bigContent := bytes.Repeat([]byte("x"), logRotateSizeLimit+1)
	if err := os.WriteFile(big, bigContent, 0o600); err != nil {
		t.Fatal(err)
	}

	// A previous .old generation must be overwritten, not rotated again
	if err := os.WriteFile(big+".old", []byte("previous generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A foreign stanza's logs (inherited from a parent volume on clone) and
	// all-server.log (no stanza)
	foreign := filepath.Join(logDir, "otherstanza-backup.log")
	if err := os.WriteFile(foreign, []byte("foreign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign+".old", []byte("foreign old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(logDir, "all-server.log")
	if err := os.WriteFile(server, []byte("server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The source stanza's restore record must be kept
	restore := filepath.Join(logDir, "otherstanza-restore.log")
	if err := os.WriteFile(restore, []byte("restore\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RotateLogs(pgData, "stanza"); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}

	// Foreign-stanza logs deleted; all-server.log and -restore.log kept
	if _, err := os.Stat(foreign); !os.IsNotExist(err) {
		t.Errorf("foreign stanza log should be deleted, err %v", err)
	}
	if _, err := os.Stat(foreign + ".old"); !os.IsNotExist(err) {
		t.Errorf("foreign stanza .old should be deleted, err %v", err)
	}
	if _, err := os.Stat(server); err != nil {
		t.Errorf("all-server.log should be kept: %v", err)
	}
	if _, err := os.Stat(restore); err != nil {
		t.Errorf("-restore.log should be kept: %v", err)
	}

	// Small file untouched
	content, err := os.ReadFile(small) //nolint:gosec // test-controlled temp path
	if err != nil || string(content) != "small\n" {
		t.Errorf("small file should be untouched, got %q err %v", content, err)
	}

	// Big file truncated in place
	info, err := os.Stat(big)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("big file should be truncated, size %d", info.Size())
	}

	// .old holds the previous content, replacing the older generation
	oldContent, err := os.ReadFile(big + ".old") //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldContent, bigContent) {
		t.Errorf(".old should hold the rotated content, got %d bytes", len(oldContent))
	}

	// Second run: nothing over the cap, .old stays
	if err := RotateLogs(pgData, "stanza"); err != nil {
		t.Fatalf("RotateLogs second run: %v", err)
	}
	if _, err := os.Stat(big + ".old"); err != nil {
		t.Errorf(".old should survive a run with nothing to rotate: %v", err)
	}
}

func TestRotateLogsMissingDir(t *testing.T) {
	if err := RotateLogs(filepath.Join(t.TempDir(), "pgdata"), "stanza"); err != nil {
		t.Fatalf("missing log dir should not error: %v", err)
	}
}
