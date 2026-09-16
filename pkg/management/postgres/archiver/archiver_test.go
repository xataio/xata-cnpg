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

package archiver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/pgbackrest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const backupHistoryFile = "000000010000000000000003.00000028.backup"

var _ = Describe("empty backup history files", func() {
	var walDir string

	writeFile := func(name string, content []byte) string {
		path := filepath.Join(walDir, name)
		Expect(os.WriteFile(path, content, 0o600)).To(Succeed())
		return path
	}

	BeforeEach(func() {
		walDir = GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(walDir, "archive_status"), 0o700)).To(Succeed())
	})

	DescribeTable("emptyBackupHistoryFileFromError",
		func(err error, expectedName string, expectedOK bool) {
			name, ok := emptyBackupHistoryFileFromError(err)
			Expect(name).To(Equal(expectedName))
			Expect(ok).To(Equal(expectedOK))
		},
		Entry("zero-size backup history file",
			&pgbackrest.CommandError{
				Command:  "archive-push",
				ExitCode: 29,
				Stderr:   "ERROR: [029]: size of WAL segment '" + backupHistoryFile + "' is 0",
			},
			backupHistoryFile, true),
		Entry("zero-size WAL segment",
			&pgbackrest.CommandError{
				Command:  "archive-push",
				ExitCode: 29,
				Stderr:   "ERROR: [029]: size of WAL segment '000000010000000000000003' is 0",
			},
			"", false),
		Entry("different pgbackrest error",
			&pgbackrest.CommandError{
				Command:  "archive-push",
				ExitCode: 82,
				Stderr:   "ERROR: [082]: WAL segment 000000010000000000000003 was not archived before the 60000ms timeout",
			},
			"", false),
		Entry("not a pgbackrest error",
			errors.New("size of WAL segment '"+backupHistoryFile+"' is 0"),
			"", false),
		Entry("nil error", nil, "", false),
	)

	Describe("isEmptyBackupHistoryFile", func() {
		It("returns true for an empty backup history file", func() {
			Expect(isEmptyBackupHistoryFile(writeFile(backupHistoryFile, nil))).To(BeTrue())
		})

		It("returns false for a backup history file with content", func() {
			path := writeFile(backupHistoryFile, []byte("START WAL LOCATION: 0/3000028\n"))
			Expect(isEmptyBackupHistoryFile(path)).To(BeFalse())
		})

		It("returns false for an empty WAL segment", func() {
			Expect(isEmptyBackupHistoryFile(writeFile("000000010000000000000003", nil))).To(BeFalse())
		})

		It("returns false for an empty timeline history file", func() {
			Expect(isEmptyBackupHistoryFile(writeFile("00000002.history", nil))).To(BeFalse())
		})

		It("returns false when the file does not exist", func() {
			Expect(isEmptyBackupHistoryFile(filepath.Join(walDir, backupHistoryFile))).To(BeFalse())
		})
	})

	Describe("markEmptyBackupHistoryFileDone", func() {
		statusFile := func(suffix string) string {
			return filepath.Join(walDir, "archive_status", backupHistoryFile+suffix)
		}

		BeforeEach(func() {
			writeFile(filepath.Join("archive_status", backupHistoryFile+".ready"), nil)
		})

		It("marks an empty backup history file as done", func() {
			writeFile(backupHistoryFile, nil)
			Expect(markEmptyBackupHistoryFileDone(walDir, backupHistoryFile)).To(Succeed())
			Expect(statusFile(".ready")).ToNot(BeAnExistingFile())
			Expect(statusFile(".done")).To(BeAnExistingFile())
		})

		It("refuses a backup history file with content", func() {
			writeFile(backupHistoryFile, []byte("START WAL LOCATION: 0/3000028\n"))
			Expect(markEmptyBackupHistoryFileDone(walDir, backupHistoryFile)).ToNot(Succeed())
			Expect(statusFile(".ready")).To(BeAnExistingFile())
		})

		It("fails when the ready file does not exist", func() {
			writeFile(backupHistoryFile, nil)
			Expect(os.Remove(statusFile(".ready"))).To(Succeed())
			Expect(markEmptyBackupHistoryFileDone(walDir, backupHistoryFile)).ToNot(Succeed())
		})
	})
})

var _ = Describe("archiveWALViaPgBackRest with an empty backup history file", func() {
	const walName = "pg_wal/000000010000000000000003"

	var (
		pgData  string
		callLog string
		cluster *apiv1.Cluster
	)

	// fakePgBackRest puts a pgbackrest script first on PATH. The script fails
	// with the empty backup history file error for its first failures calls.
	fakePgBackRest := func(failures int) {
		binDir := GinkgoT().TempDir()
		callLog = filepath.Join(binDir, "calls")
		script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> '%[1]s'
if [ $(wc -l < '%[1]s') -le %[2]d ]; then
	echo "ERROR: [029]: size of WAL segment '%[3]s' is 0"
	exit 29
fi
`, callLog, failures, backupHistoryFile)
		scriptPath := filepath.Join(binDir, "pgbackrest")
		Expect(os.WriteFile(scriptPath, []byte(script), 0o700)).To(Succeed()) //nolint:gosec // executable script
		GinkgoT().Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	pgBackRestCalls := func() int {
		content, err := os.ReadFile(callLog) //nolint:gosec // temp path
		if errors.Is(err, fs.ErrNotExist) {
			return 0
		}
		Expect(err).ToNot(HaveOccurred())
		return strings.Count(string(content), "\n")
	}

	statusFile := func(suffix string) string {
		return filepath.Join(pgData, "pg_wal", "archive_status", backupHistoryFile+suffix)
	}

	BeforeEach(func() {
		pgData = GinkgoT().TempDir()
		cluster = &apiv1.Cluster{}
		cluster.Name = "test"
		Expect(os.MkdirAll(filepath.Join(pgData, "pg_wal", "archive_status"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(pgData, "pg_wal", backupHistoryFile), nil, 0o600)).To(Succeed())
	})

	It("marks the backup history file as archived and pushes again", func() {
		Expect(os.WriteFile(statusFile(".ready"), nil, 0o600)).To(Succeed())
		fakePgBackRest(1)

		Expect(archiveWALViaPgBackRest(context.Background(), cluster, pgData, walName)).To(Succeed())
		Expect(pgBackRestCalls()).To(Equal(2))
		Expect(statusFile(".ready")).ToNot(BeAnExistingFile())
		Expect(statusFile(".done")).To(BeAnExistingFile())
	})

	It("returns the error when the second push fails", func() {
		Expect(os.WriteFile(statusFile(".ready"), nil, 0o600)).To(Succeed())
		fakePgBackRest(2)

		err := archiveWALViaPgBackRest(context.Background(), cluster, pgData, walName)
		var cmdErr *pgbackrest.CommandError
		Expect(errors.As(err, &cmdErr)).To(BeTrue())
		Expect(pgBackRestCalls()).To(Equal(2))
	})

	It("returns both errors when the backup history file cannot be marked", func() {
		fakePgBackRest(1)

		err := archiveWALViaPgBackRest(context.Background(), cluster, pgData, walName)
		var cmdErr *pgbackrest.CommandError
		Expect(errors.As(err, &cmdErr)).To(BeTrue())
		Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue())
		Expect(pgBackRestCalls()).To(Equal(1))
	})

	It("skips the backup history file when it is requested by name", func() {
		fakePgBackRest(1)

		Expect(archiveWALViaPgBackRest(context.Background(), cluster, pgData, "pg_wal/"+backupHistoryFile)).
			To(Succeed())
		Expect(pgBackRestCalls()).To(Equal(0))
	})
})
