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

// Package walg runs the wal-g binary against a read-only archive: fetching a
// base backup to bootstrap a data directory and fetching WAL segments to keep
// replaying it. CloudNativePG never pushes to a wal-g archive.
package walg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

const (
	// walgBinary is the wal-g executable name
	walgBinary = "wal-g"

	// LatestBackup is the wal-g name for the most recent base backup
	LatestBackup = "LATEST"
)

// IsAvailable checks if the wal-g binary is present in the container
func IsAvailable() bool {
	_, err := exec.LookPath(walgBinary)
	return err == nil
}

// ErrWALNotFound is returned by WalFetch when the requested WAL segment does
// not exist in the archive. This is a normal condition: PostgreSQL uses it to
// stop recovery or to wait for the next segment.
var ErrWALNotFound = errors.New("WAL segment not found in wal-g archive")

// ErrNotAvailable is returned when the wal-g binary is missing from the image
var ErrNotAvailable = errors.New("wal-g binary not found in the container image")

// CommandError carries the details of a failed wal-g invocation
type CommandError struct {
	Command  string
	Args     []string
	Output   string
	ExitCode int
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("wal-g %s failed with exit code %d: %s", e.Command, e.ExitCode, strings.TrimSpace(e.Output))
}

// walNotFoundMarkers are substrings wal-g prints when a requested segment or
// history file is absent from the archive
var walNotFoundMarkers = []string{
	"does not exist",
	"not found",
	"nosuchkey",
}

func run(ctx context.Context, env []string, args ...string) error {
	contextLog := log.FromContext(ctx)

	if !IsAvailable() {
		return ErrNotAvailable
	}

	contextLog.Debug("Running wal-g command", "args", args)

	cmd := exec.CommandContext(ctx, walgBinary, args...) // #nosec G204
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("failed to execute wal-g: %w", err)
	}

	return &CommandError{
		Command:  args[0],
		Args:     args,
		Output:   output.String(),
		ExitCode: exitErr.ExitCode(),
	}
}

// BackupFetch restores the base backup named backupName (LatestBackup when
// empty) into pgDataPath, which must be empty.
func BackupFetch(ctx context.Context, env []string, pgDataPath, backupName string) error {
	if backupName == "" {
		backupName = LatestBackup
	}

	log.FromContext(ctx).Info("Starting wal-g backup-fetch",
		"pgDataPath", pgDataPath, "backupName", backupName)

	return run(ctx, env, "backup-fetch", pgDataPath, backupName)
}

// WalFetch retrieves a single WAL segment or timeline history file from the
// archive into destPath. Returns ErrWALNotFound if the archive has no such
// file.
func WalFetch(ctx context.Context, env []string, walName, destPath string) error {
	err := run(ctx, env, "wal-fetch", walName, destPath)
	if err == nil {
		return nil
	}

	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && isWALNotFound(cmdErr.Output) {
		return ErrWALNotFound
	}

	return err
}

func isWALNotFound(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range walNotFoundMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
