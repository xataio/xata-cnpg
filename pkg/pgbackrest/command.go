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

	"github.com/cloudnative-pg/machinery/pkg/log"
)

const (
	// ConfigFilePath is the location of the pgbackrest configuration file.
	ConfigFilePath = "/controller/pgbackrest/pgbackrest.conf"

	// SpoolPath is the spool directory for pgbackrest async archiving.
	SpoolPath = "/controller/pgbackrest-spool"

	// pgbackrestBinary is the pgbackrest executable name.
	pgbackrestBinary = "pgbackrest"
)

// ErrWALNotFound is returned by ArchiveGet when the requested WAL segment
// does not exist in the repository. This is a normal condition — PostgreSQL
// uses it to stop recovery or fall back to streaming replication.
var ErrWALNotFound = errors.New("WAL segment not found in repository")

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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

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
		Stderr:   stderr.String(),
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

	return runPgBackRest(ctx, "stanza-create", "--no-online", "--stanza="+stanzaName)
}

// ArchivePush archives a WAL file to the pgbackrest repository.
func ArchivePush(ctx context.Context, stanzaName string, walPath string) error {
	return runPgBackRest(ctx, "archive-push", "--stanza="+stanzaName, walPath)
}

// ArchiveGet retrieves a WAL file from the pgbackrest repository.
// Returns ErrWALNotFound if the WAL segment does not exist in the repository.
func ArchiveGet(ctx context.Context, stanzaName string, walName string, destPath string) error {
	err := runPgBackRest(ctx, "archive-get", "--stanza="+stanzaName, walName, destPath)
	if err == nil {
		return nil
	}

	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && cmdErr.ExitCode == 2 {
		return ErrWALNotFound
	}

	return err
}
