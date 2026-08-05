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

// newLogDir creates a pgbackrest log directory under a temp PGDATA and returns
// both paths.
func newLogDir(t *testing.T) (pgData, logDir string) {
	t.Helper()
	pgData = filepath.Join(t.TempDir(), "pgdata")
	logDir = filepath.Join(workingDir(pgData), "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return pgData, logDir
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// Size rotation of the current stanza's files, with the marker already ours.
func TestRotateLogsSizeRotation(t *testing.T) {
	pgData, logDir := newLogDir(t)
	write(t, filepath.Join(logDir, ownerMarkerFile), []byte("stanza\n"))

	small := filepath.Join(logDir, "stanza-small.log")
	write(t, small, []byte("small\n"))

	big := filepath.Join(logDir, "stanza-big.log")
	bigContent := bytes.Repeat([]byte("x"), logRotateSizeLimit+1)
	write(t, big, bigContent)
	write(t, big+".old", []byte("previous generation\n"))

	if err := RotateLogs(pgData, "stanza"); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}

	if c, err := os.ReadFile(small); err != nil || string(c) != "small\n" { //nolint:gosec // temp path
		t.Errorf("small file should be untouched, got %q err %v", c, err)
	}
	if s := mustSize(t, big); s != 0 {
		t.Errorf("big file should be truncated, size %d", s)
	}
	oldContent, err := os.ReadFile(big + ".old") //nolint:gosec // temp path
	if err != nil || !bytes.Equal(oldContent, bigContent) {
		t.Errorf(".old should hold the rotated content, got %d bytes err %v", len(oldContent), err)
	}
}

// Owner change (clone): a marker naming a different stanza triggers deletion of
// the previous owner's logs and a clean slate of all-server.log; the current
// stanza's files and any -restore.log are kept, and the marker is updated.
func TestRotateLogsOwnerChange(t *testing.T) {
	pgData, logDir := newLogDir(t)
	write(t, filepath.Join(logDir, ownerMarkerFile), []byte("parent\n"))

	foreign := filepath.Join(logDir, "parent-backup.log")
	write(t, foreign, []byte("foreign\n"))
	write(t, foreign+".old", []byte("foreign old\n"))
	server := filepath.Join(logDir, "all-server.log")
	write(t, server, []byte("parent server history\n"))
	restore := filepath.Join(logDir, "parent-restore.log")
	write(t, restore, []byte("restore\n"))
	own := filepath.Join(logDir, "stanza-backup.log")
	write(t, own, []byte("own\n"))

	if err := RotateLogs(pgData, "stanza"); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}

	if _, err := os.Stat(foreign); !os.IsNotExist(err) {
		t.Errorf("foreign stanza log should be deleted, err %v", err)
	}
	if _, err := os.Stat(foreign + ".old"); !os.IsNotExist(err) {
		t.Errorf("foreign stanza .old should be deleted, err %v", err)
	}
	if s := mustSize(t, server); s != 0 {
		t.Errorf("all-server.log should be truncated on owner change, size %d", s)
	}
	if _, err := os.Stat(restore); err != nil {
		t.Errorf("-restore.log should be kept: %v", err)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("own stanza log should be kept: %v", err)
	}
	if got := readOwnerMarker(filepath.Join(logDir, ownerMarkerFile)); got != "stanza" {
		t.Errorf("marker should be updated to current stanza, got %q", got)
	}
}

// Pool adoption: the previous owner (a suspended warm-pool cluster) left NO
// stanza log files, only all-server.log. The marker still proves the change, so
// all-server.log is truncated even with no foreign *.log to find.
func TestRotateLogsPoolAdoptionNoForeignLogs(t *testing.T) {
	pgData, logDir := newLogDir(t)
	write(t, filepath.Join(logDir, ownerMarkerFile), []byte("poolcluster\n"))
	server := filepath.Join(logDir, "all-server.log")
	write(t, server, []byte("warm pool server history\n"))

	if err := RotateLogs(pgData, "branch"); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}

	if s := mustSize(t, server); s != 0 {
		t.Errorf("all-server.log should be truncated on pool adoption, size %d", s)
	}
	if got := readOwnerMarker(filepath.Join(logDir, ownerMarkerFile)); got != "branch" {
		t.Errorf("marker should be updated, got %q", got)
	}
}

// Missing marker (new or existing cluster): NOT treated as an owner change, so
// all-server.log is kept; the marker is written so later reconciles match.
func TestRotateLogsMissingMarkerKeepsHistory(t *testing.T) {
	pgData, logDir := newLogDir(t)
	server := filepath.Join(logDir, "all-server.log")
	write(t, server, []byte("existing history\n"))

	if err := RotateLogs(pgData, "stanza"); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}

	if c, err := os.ReadFile(server); err != nil || string(c) != "existing history\n" { //nolint:gosec // temp path
		t.Errorf("all-server.log should be kept when marker is missing, got %q err %v", c, err)
	}
	if got := readOwnerMarker(filepath.Join(logDir, ownerMarkerFile)); got != "stanza" {
		t.Errorf("marker should be written, got %q", got)
	}
}

// Marker matches (steady state / restart): all-server.log left alone across
// runs, so legitimate server-restart history accumulates.
func TestRotateLogsMarkerMatchKeepsHistory(t *testing.T) {
	pgData, logDir := newLogDir(t)
	write(t, filepath.Join(logDir, ownerMarkerFile), []byte("stanza\n"))
	server := filepath.Join(logDir, "all-server.log")
	write(t, server, []byte("start1\nstart2\n"))

	if err := RotateLogs(pgData, "stanza"); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if c, err := os.ReadFile(server); err != nil || string(c) != "start1\nstart2\n" { //nolint:gosec // temp path
		t.Errorf("all-server.log should be untouched on marker match, got %q err %v", c, err)
	}
}

func TestRotateLogsMissingDir(t *testing.T) {
	if err := RotateLogs(filepath.Join(t.TempDir(), "pgdata"), "stanza"); err != nil {
		t.Fatalf("missing log dir should not error: %v", err)
	}
}
