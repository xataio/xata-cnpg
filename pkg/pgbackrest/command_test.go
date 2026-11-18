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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
)

func TestCommandError_Error(t *testing.T) {
	err := &CommandError{
		Command:  "backup",
		Args:     []string{"--config=/controller/pgbackrest/pgbackrest.conf", "--stanza=test", "backup"},
		Stderr:   "ERROR: some pgbackrest error",
		ExitCode: 1,
	}

	msg := err.Error()
	if msg != "pgbackrest backup failed (exit code 1): ERROR: some pgbackrest error" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestCommandError_ErrorEmptyStderr(t *testing.T) {
	err := &CommandError{
		Command:  "stanza-create",
		ExitCode: 2,
	}

	msg := err.Error()
	if msg != "pgbackrest stanza-create failed (exit code 2): " {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestCommandError_IsError(t *testing.T) {
	var err error = &CommandError{
		Command:  "archive-push",
		ExitCode: 1,
	}

	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Error("expected CommandError to satisfy errors.As")
	}
	if cmdErr.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", cmdErr.ExitCode)
	}
}

func TestErrWALNotFound(t *testing.T) {
	if ErrWALNotFound.Error() != "WAL segment not found in repository" {
		t.Errorf("unexpected error message: %s", ErrWALNotFound.Error())
	}
}

func TestArchiveGet_WALNotFound(t *testing.T) {
	// Simulate a CommandError with exit code 2 (WAL not found)
	cmdErr := &CommandError{
		Command:  "archive-get",
		ExitCode: 2,
		Stderr:   "WAL segment not found",
	}

	// Verify that exit code 2 maps to ErrWALNotFound
	var err error = cmdErr
	if !errors.As(err, &cmdErr) {
		t.Fatal("expected CommandError")
	}
	if cmdErr.ExitCode != 2 {
		t.Errorf("expected exit code 2, got %d", cmdErr.ExitCode)
	}

	// Verify ErrWALNotFound is a distinct error
	if errors.Is(cmdErr, ErrWALNotFound) {
		t.Error("CommandError should not be ErrWALNotFound directly")
	}
}

func TestIsStanzaMissingFromRepo(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			"FileMissingError with exit 103",
			&CommandError{
				Command:  "archive-push",
				ExitCode: 103,
				Stderr:   "repo1: [FileMissingError] unable to load info file '/stanza/archive/stanza/archive.info'",
			},
			true,
		},
		{
			"exit 103 without FileMissingError",
			&CommandError{
				Command:  "archive-push",
				ExitCode: 103,
				Stderr:   "repo1: [ArchiveMismatchError] PostgreSQL version 17, system-id 123 do not match",
			},
			false,
		},
		{
			"FileMissingError with different exit code",
			&CommandError{
				Command:  "archive-push",
				ExitCode: 1,
				Stderr:   "[FileMissingError] something",
			},
			false,
		},
		{
			"nil error",
			nil,
			false,
		},
		{
			"non-CommandError",
			fmt.Errorf("some error"),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStanzaMissingFromRepo(tt.err); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestIsLockBusy(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			"lock acquire failure with exit 50",
			&CommandError{
				Command:  "backup",
				ExitCode: 50,
				Stderr:   "ERROR: [050]: unable to acquire lock: Resource temporarily unavailable",
			},
			true,
		},
		{
			"different exit code",
			&CommandError{
				Command:  "backup",
				ExitCode: 1,
				Stderr:   "some other failure",
			},
			false,
		},
		{
			"nil error",
			nil,
			false,
		},
		{
			"non-CommandError",
			fmt.Errorf("some error"),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLockBusy(tt.err); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestClassifyExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected apiv1.BackupFailureReason
		ok       bool
	}{
		{
			name: "lock contention, exit 50",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 50,
				Stderr: "unable to acquire lock on file " +
					"'/var/lib/postgresql/data/pgbackrest/lock/<cluster>-backup-1.lock'",
			},
			expected: apiv1.BackupFailureReasonLockContention,
			ok:       true,
		},
		{
			name: "object store denied, exit 39",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 39,
				Stderr: "ERROR: [039]: unable to load info file '/<c>/backup/<c>/backup.info' " +
					"ProtocolError: HTTP request failed with 403 (Forbidden): " +
					"GET /storage/v1/b/example-backups-bucket/o/... " +
					"Caller does not have storage.objects.get access to the Google Cloud Storage object.",
			},
			expected: apiv1.BackupFailureReasonObjectStoreDenied,
			ok:       true,
		},
		{
			name: "db unavailable, exit 56, DbConnectError right after pod recreation",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 56,
				Stderr:   "ERROR: [056]: unable to connect to 'dbname=postgres port=5432': DbConnectError",
			},
			expected: apiv1.BackupFailureReasonDBUnavailable,
			ok:       true,
		},
		{
			name: "stanza not ready, exit 55, bucket has no stanza",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 55,
				Stderr: "ERROR: [055]: unable to load info file '/<c>/backup/<c>/backup.info' " +
					"or '/<c>/backup/<c>/backup.info.copy'",
			},
			expected: apiv1.BackupFailureReasonStanzaNotReady,
			ok:       true,
		},
		{
			name: "object store error, exit 39, no 403/Forbidden/AccessDenied",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 39,
				Stderr:   "ERROR: [039]: unable to load info file: ProtocolError: unexpected end of file",
			},
			expected: apiv1.BackupFailureReasonObjectStoreError,
			ok:       true,
		},
		{
			name: "version mismatch, exit 39, pgbackrest greeting version handshake",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 39,
				Stderr:   "ERROR: [039]: expected value '2.32' for greeting key 'version' but got '2.33'",
			},
			expected: apiv1.BackupFailureReasonVersionMismatch,
			ok:       true,
		},
		{
			name: "version mismatch, exit 56, standby handshake fails during a mixed rollout",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 56,
				Stderr: "WARN: unable to check pg2: [ProtocolError] expected value '2.59.1' for greeting key " +
					"'version' but got '2.59.0'\n" +
					"HINT: is the same version of pgBackRest installed on the local and remote host?\n" +
					"ERROR: [056]: unable to find primary cluster - cannot proceed\n" +
					"HINT: are all available clusters in recovery?\n",
			},
			expected: apiv1.BackupFailureReasonVersionMismatch,
			ok:       true,
		},
		{
			name: "standby unreachable, exit 39, TLS connect failure on port 8432",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 39,
				Stderr:   "ERROR: [039]: unable to negotiate TLS connection to 'cluster-example-rw:8432'",
			},
			expected: apiv1.BackupFailureReasonStandbyUnreachable,
			ok:       true,
		},
		{
			name: "standby unreachable, exit 49, host-connect to the primary",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 49,
				Stderr:   "ERROR: [049]: unable to connect to 'cluster-example-rw:8432'",
			},
			expected: apiv1.BackupFailureReasonStandbyUnreachable,
			ok:       true,
		},
		{
			name: "object store error, exit 49, the S3 endpoint refuses the connection",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 49,
				Stderr: "ERROR: [049]: unable to load info file '/c/backup/c/backup.info' or " +
					"'/c/backup/c/backup.info.copy':\n" +
					"HostConnectError: unable to connect to 'minio.example.svc:9000 (10.0.0.9)': " +
					"[111] Connection refused\n" +
					"[HostConnectError] on 10 retries from 105-60001ms: unable to connect to " +
					"'minio.example.svc:9000 (10.0.0.9)': [111] Connection refused\n" +
					"HINT: backup.info cannot be opened and is required to perform a backup.\n",
			},
			expected: apiv1.BackupFailureReasonObjectStoreError,
			ok:       true,
		},
		{
			name: "standby unreachable, exit 49, error raised from a remote pg host",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 49,
				Stderr: "ERROR: [049]: raised from remote-0 tls protocol on 'cluster-example-2': " +
					"unable to connect to 'cluster-example-2.svc (10.0.0.2)': [111] Connection refused",
			},
			expected: apiv1.BackupFailureReasonStandbyUnreachable,
			ok:       true,
		},
		{
			name: "wal archiving, exit 82, the backup stop segment never reached the repository",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 82,
				Stderr: "ERROR: [082]: WAL segment 000000010000000000000005 was not archived before the 60000ms timeout\n" +
					"HINT: check the archive_command to ensure that all options are correct (especially --stanza).",
			},
			expected: apiv1.BackupFailureReasonWalArchiving,
			ok:       true,
		},
		{
			name: "generic pgbackrest error, exit 82, standby does not replay in time",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 82,
				Stderr: "ERROR: [082]: timeout before standby replayed to 0/5000028 - only reached 0/4000000\n" +
					"HINT: is replication running and current on the standby?",
			},
			expected: apiv1.BackupFailureReasonPgBackRestError,
			ok:       true,
		},
		{
			name: "generic pgbackrest error for an exit code with no dedicated reason",
			err: &CommandError{
				Command:  "backup",
				ExitCode: 100,
				Stderr:   "ERROR: [100]: kernel error",
			},
			expected: apiv1.BackupFailureReasonPgBackRestError,
			ok:       true,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: "",
			ok:       false,
		},
		{
			name:     "non-CommandError falls back to the caller's own classification",
			err:      fmt.Errorf("some non-pgbackrest error"),
			expected: "",
			ok:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClassifyExitCode(tt.err)
			if ok != tt.ok {
				t.Fatalf("expected ok=%v, got ok=%v", tt.ok, ok)
			}
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

const testRoleARN = "arn:aws:iam::123456789012:role/cluster-cnpg-backups-restore"

func TestRepositoryRoleARN(t *testing.T) {
	tests := []struct {
		name    string
		repo    *apiv1.PgBackRestRepository
		want    string
		wantErr bool
	}{
		{name: "nil repository"},
		{name: "gcs", repo: &apiv1.PgBackRestRepository{GCS: &apiv1.PgBackRestGCS{Bucket: "b"}}},
		{name: "s3 without role", repo: &apiv1.PgBackRestRepository{S3: &apiv1.PgBackRestS3{Bucket: "b"}}},
		{
			name: "s3 with role",
			repo: &apiv1.PgBackRestRepository{S3: &apiv1.PgBackRestS3{RoleARN: testRoleARN}},
			want: testRoleARN,
		},
		{
			name: "role with a path",
			repo: &apiv1.PgBackRestRepository{S3: &apiv1.PgBackRestS3{
				RoleARN: "arn:aws-us-gov:iam::123456789012:role/a/b/name+=,.@_-",
			}},
			want: "arn:aws-us-gov:iam::123456789012:role/a/b/name+=,.@_-",
		},
		{
			name:    "shell metacharacters",
			repo:    &apiv1.PgBackRestRepository{S3: &apiv1.PgBackRestS3{RoleARN: testRoleARN + "'; touch /tmp/x"}},
			wantErr: true,
		},
		{
			name:    "not a role",
			repo:    &apiv1.PgBackRestRepository{S3: &apiv1.PgBackRestS3{RoleARN: "arn:aws:iam::123456789012:user/x"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RepositoryRoleARN(tt.repo)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArchiveGetCommand(t *testing.T) {
	want := "pgbackrest --config=" + ConfigFilePath + " --stanza=src archive-get %f \"%p\""
	if got := ArchiveGetCommand("src", ""); got != want {
		t.Errorf("without role: got %q, want %q", got, want)
	}
	if got := ArchiveGetCommand("src", testRoleARN); got != "AWS_ROLE_ARN="+testRoleARN+" "+want {
		t.Errorf("with role: got %q", got)
	}
}

// fakePgBackRest puts a pgbackrest on PATH that records AWS_ROLE_ARN and its
// arguments, and sets the inherited AWS_ROLE_ARN to podRole.
func fakePgBackRest(t *testing.T) (outFile string) {
	t.Helper()
	dir := t.TempDir()
	outFile = filepath.Join(dir, "out")
	script := "#!/bin/sh\nprintf '%s\\n' \"$AWS_ROLE_ARN\" \"$@\" > " + outFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pgbackrest"), []byte(script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AWS_ROLE_ARN", "pod-role")
	return outFile
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestRestore_RoleARN(t *testing.T) {
	for _, tt := range []struct{ roleARN, want string }{
		{roleARN: "", want: "pod-role"},
		{roleARN: testRoleARN, want: testRoleARN},
	} {
		out := fakePgBackRest(t)
		if err := Restore(context.Background(), "src", "/pgdata", "", tt.roleARN); err != nil {
			t.Fatal(err)
		}
		lines := readLines(t, out)
		if lines[0] != tt.want {
			t.Errorf("roleARN %q: pgbackrest saw AWS_ROLE_ARN %q, want %q", tt.roleARN, lines[0], tt.want)
		}
		if !strings.Contains(strings.Join(lines[1:], " "), "--stanza=src restore") {
			t.Errorf("unexpected args %q", lines[1:])
		}
	}
}

// TestArchiveGetCommand_Shell runs the restore_command through sh, the way
// Postgres does, after substituting %f and %p.
func TestArchiveGetCommand_Shell(t *testing.T) {
	for _, tt := range []struct{ roleARN, want string }{
		{roleARN: "", want: "pod-role"},
		{roleARN: testRoleARN, want: testRoleARN},
	} {
		out := fakePgBackRest(t)
		command := ArchiveGetCommand("src", tt.roleARN)
		command = strings.NewReplacer("%f", "000000010000000000000001", "%p", "pg_wal/RECOVERYXLOG").Replace(command)
		if output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil { //nolint:gosec
			t.Fatalf("%v: %s", err, output)
		}
		lines := readLines(t, out)
		want := []string{
			tt.want, "--config=" + ConfigFilePath, "--stanza=src", "archive-get",
			"000000010000000000000001", "pg_wal/RECOVERYXLOG",
		}
		if strings.Join(lines, "|") != strings.Join(want, "|") {
			t.Errorf("roleARN %q: got %q, want %q", tt.roleARN, lines, want)
		}
	}
}

// TestRoleARNPatternMatchesCRD keeps roleARNPattern equal to the RoleARN
// kubebuilder pattern, which cannot reference a Go constant.
func TestRoleARNPatternMatchesCRD(t *testing.T) {
	data, err := os.ReadFile("../../config/crd/bases/postgresql.cnpg.io_clusters.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	s3 := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].
		Properties["externalClusters"].Items.Schema.Properties["pgBackRest"].
		Properties["repository"].Properties["s3"]
	if got := s3.Properties["roleARN"].Pattern; got != roleARNPattern.String() {
		t.Errorf("CRD pattern %q differs from roleARNPattern %q", got, roleARNPattern.String())
	}
}
