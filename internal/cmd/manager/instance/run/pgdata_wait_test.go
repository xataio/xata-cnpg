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

package run

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("waitForPGData", func() {
	It("returns immediately when PGDATA already exists", func() {
		tmpDir := GinkgoT().TempDir()
		pgData := filepath.Join(tmpDir, "pgdata")
		Expect(os.Mkdir(pgData, 0o755)).To(Succeed())

		ctx := context.Background()
		err := waitForPGData(ctx, pgData)
		Expect(err).ToNot(HaveOccurred())
	})

	It("waits and returns when PGDATA appears after a delay", func() {
		tmpDir := GinkgoT().TempDir()
		pgData := filepath.Join(tmpDir, "pgdata")

		// Create PGDATA after a short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			Expect(os.Mkdir(pgData, 0o755)).To(Succeed())
		}()

		ctx := context.Background()
		err := waitForPGData(ctx, pgData)
		Expect(err).ToNot(HaveOccurred())
	})

	It("waits and returns when marker file appears after a delay", func() {
		tmpDir := GinkgoT().TempDir()
		pgData := filepath.Join(tmpDir, "pgdata")
		markerPath := filepath.Join(tmpDir, xataReadyMarker)

		// Create marker file after a short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			f, err := os.Create(markerPath)
			Expect(err).ToNot(HaveOccurred())
			f.Close()
		}()

		ctx := context.Background()
		err := waitForPGData(ctx, pgData)
		Expect(err).ToNot(HaveOccurred())
	})

	It("returns error on context cancellation", func() {
		tmpDir := GinkgoT().TempDir()
		pgData := filepath.Join(tmpDir, "pgdata")

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := waitForPGData(ctx, pgData)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("context cancelled"))
	})
})
