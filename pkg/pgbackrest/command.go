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

// Package pgbackrest provides pgbackrest configuration generation and
// command execution for WAL archiving and restore.
package pgbackrest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cloudnative-pg/machinery/pkg/log"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/postgres"
)

const (
	dataDirectory = postgres.ScratchDataDirectory + "/pgbackrest"

	// ConfigFilePath is the location of the pgbackrest configuration file.
	ConfigFilePath = dataDirectory + "/pgbackrest.conf"

	// pgbackrestBinary is the pgbackrest executable name.
	pgbackrestBinary = "pgbackrest"
)

// IsAvailable checks if the pgbackrest binary is present in the container. This
// is needed so the reconciler does not keep retrying to create a stanza in an
// image without pgbackrest.
func IsAvailable() bool {
	_, err := exec.LookPath(pgbackrestBinary)
	return err == nil
}

// ErrWALNotFound is returned by ArchiveGet when the requested WAL segment
// does not exist in the repository. This is a normal condition — PostgreSQL
// uses it to stop recovery or fall back to streaming replication.
var ErrWALNotFound = errors.New("WAL segment not found in repository")

// IsStanzaMissingFromRepo checks whether a pgbackrest error indicates that
// the stanza metadata (archive.info) has been deleted from the repository.
// This is exit code 103 with FileMissingError — meaning the info files are
// gone but pgbackrest found no valid repository to operate against.
// Recreating the stanza will only succeed if the S3 path is fully clean;
// if partial data remains, stanza-create will fail separately.
func IsStanzaMissingFromRepo(err error) bool {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		return false
	}
	return cmdErr.ExitCode == 103 && strings.Contains(cmdErr.Stderr, "FileMissingError")
}

// lockAcquireExitCode is the pgbackrest exit code (LockAcquireError) returned
// when a command cannot acquire its lock because another pgbackrest process is
// already holding it.
const lockAcquireExitCode = 50

// IsLockBusy reports whether a pgbackrest error indicates the command could not
// acquire its lock because another pgbackrest process holds it. stanza-create,
// backup and expire all share pgbackrest's "backup" lock, so a backup triggered
// at cluster adoption can race the stanza-create that runs on the same event and
// fail with this error. The contention is transient — it clears as soon as the
// other process releases the lock — so callers can safely retry.
func IsLockBusy(err error) bool {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		return false
	}
	return cmdErr.ExitCode == lockAcquireExitCode
}

const (
	protocolExitCode    = 39
	hostConnectExitCode = 49
	fileMissingExitCode = 55
	dbConnectExitCode   = 56
)

// ClassifyExitCode returns the failure reason for a pgbackrest CommandError, or false for any other error.
func ClassifyExitCode(err error) (apiv1.BackupFailureReason, bool) {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		return "", false
	}

	switch cmdErr.ExitCode {
	case lockAcquireExitCode:
		return apiv1.BackupFailureReasonLockContention, true
	case dbConnectExitCode:
		return apiv1.BackupFailureReasonDBUnavailable, true
	case hostConnectExitCode:
		return apiv1.BackupFailureReasonStandbyUnreachable, true
	case fileMissingExitCode:
		return apiv1.BackupFailureReasonStanzaNotReady, true
	case protocolExitCode:
		return classifyProtocolError(cmdErr.Stderr), true
	default:
		return apiv1.BackupFailureReasonPgBackRestError, true
	}
}

func classifyProtocolError(stderr string) apiv1.BackupFailureReason {
	switch {
	case strings.Contains(stderr, "403") ||
		strings.Contains(stderr, "Forbidden") ||
		strings.Contains(stderr, "AccessDenied"):
		return apiv1.BackupFailureReasonObjectStoreDenied
	case strings.Contains(stderr, "greeting key 'version'"):
		return apiv1.BackupFailureReasonVersionMismatch
	case strings.Contains(stderr, fmt.Sprintf(":%d", TLSServerPort)) ||
		strings.Contains(strings.ToLower(stderr), "tls"):
		return apiv1.BackupFailureReasonStandbyUnreachable
	default:
		return apiv1.BackupFailureReasonObjectStoreError
	}
}

// CommandError is returned when a pgbackrest command fails.
type CommandError struct {
	// Command is the pgbackrest subcommand that failed (e.g. "archive-push").
	Command string
	// Args is the full argument list passed to pgbackrest.
	Args []string
	// Stderr is the captured standard error output.
	Stderr string
	// ExitCode is the process exit code.
	ExitCode int
}

// Error implements the error interface.
func (e *CommandError) Error() string {
	return fmt.Sprintf("pgbackrest %s failed (exit code %d): %s", e.Command, e.ExitCode, e.Stderr)
}

// runPgBackRest executes a pgbackrest command with the given arguments.
// It captures stderr and maps exit codes to appropriate error types.
func runPgBackRest(ctx context.Context, args ...string) error {
	contextLog := log.FromContext(ctx)

	// Prepend the config file flag
	fullArgs := append([]string{"--config=" + ConfigFilePath}, args...)

	contextLog.Debug("Running pgbackrest command", "args", fullArgs)

	cmd := exec.CommandContext(ctx, pgbackrestBinary, fullArgs...) // #nosec G204
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("failed to execute pgbackrest: %w", err)
	}

	exitCode := exitErr.ExitCode()

	// Determine the subcommand for error reporting
	subcommand := ""
	for _, arg := range args {
		if arg[0] != '-' {
			subcommand = arg
			break
		}
	}

	return &CommandError{
		Command:  subcommand,
		Args:     fullArgs,
		Stderr:   output.String(),
		ExitCode: exitCode,
	}
}

// StanzaCreate initializes a pgbackrest stanza. This is idempotent — if the
// stanza already exists and matches, it's a no-op. Uses --no-online so it
// doesn't require PostgreSQL to be running (reads PG version from PG_VERSION
// file in PGDATA instead). PGDATA is guaranteed to exist at this point because
// the instance Pod is only created after the bootstrap Job (initdb/restore)
// completes and writes PGDATA to the PVC.
func StanzaCreate(ctx context.Context, stanzaName string) error {
	contextLog := log.FromContext(ctx)
	contextLog.Info("Creating pgbackrest stanza", "stanza", stanzaName)

	return runPgBackRest(ctx, "--stanza="+stanzaName, "stanza-create", "--no-online")
}

// ArchivePush archives a WAL file to the pgbackrest repository.
func ArchivePush(ctx context.Context, stanzaName string, walPath string) error {
	return runPgBackRest(ctx, "--stanza="+stanzaName, "archive-push", walPath)
}

// Backup takes a backup of the PostgreSQL cluster.
// backupType should be "full", "diff", or "incr".
// annotation is a key=value pair attached to the backup for identification.
func Backup(ctx context.Context, stanzaName string, backupType string, annotation string) error {
	contextLog := log.FromContext(ctx)
	contextLog.Info("Starting pgbackrest backup", "stanza", stanzaName, "type", backupType)

	args := []string{"--stanza=" + stanzaName, "backup", "--type=" + backupType}
	if annotation != "" {
		args = append(args, "--annotation="+annotation)
	}

	return runPgBackRest(ctx, args...)
}

// Restore restores a PostgreSQL data directory from the pgbackrest repository.
func Restore(ctx context.Context, stanzaName string, pgDataPath string, backupLabel string) error {
	contextLog := log.FromContext(ctx)
	contextLog.Info("Starting pgbackrest restore",
		"stanza", stanzaName, "pgDataPath", pgDataPath, "backupLabel", backupLabel)

	args := []string{"--stanza=" + stanzaName, "restore", "--pg1-path=" + pgDataPath}
	if backupLabel != "" {
		args = append(args, "--set="+backupLabel)
	}

	return runPgBackRest(ctx, args...)
}

// ArchiveGet retrieves a WAL file from the pgbackrest repository.
// Returns ErrWALNotFound if the WAL segment does not exist in the repository.
func ArchiveGet(ctx context.Context, stanzaName string, walName string, destPath string) error {
	err := runPgBackRest(ctx, "--stanza="+stanzaName, "archive-get", walName, destPath)
	if err == nil {
		return nil
	}

	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && cmdErr.ExitCode == 2 {
		return ErrWALNotFound
	}

	return err
}
