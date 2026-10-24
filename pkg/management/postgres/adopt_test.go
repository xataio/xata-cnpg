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

package postgres

import (
	"path/filepath"

	"github.com/cloudnative-pg/machinery/pkg/fileutils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("adopted standby recovery marker", func() {
	var pgData string

	BeforeEach(func() {
		pgData = GinkgoT().TempDir()
	})

	touch := func(name string) {
		Expect(fileutils.CreateEmptyFile(filepath.Join(pgData, name))).To(Succeed())
	}

	Describe("IsAdoptedStandbyInRecovery", func() {
		It("is true for an adopted standby that has not been promoted", func() {
			touch(AdoptedMarkerFile)
			touch(AdoptedRecoveryMarkerFile)
			touch(standbySignalFile)
			Expect(IsAdoptedStandbyInRecovery(pgData)).To(BeTrue())
		})

		It("is false once PostgreSQL has promoted the adopted instance", func() {
			touch(AdoptedMarkerFile)
			touch(AdoptedRecoveryMarkerFile)
			Expect(IsAdoptedStandbyInRecovery(pgData)).To(BeFalse())
		})

		It("is false for a replica taken from an adopted primary", func() {
			// pg_basebackup carries AdoptedMarkerFile over, and the replica
			// gets its own standby.signal, but the recovery marker is gone.
			touch(AdoptedMarkerFile)
			touch(standbySignalFile)
			Expect(IsAdoptedStandbyInRecovery(pgData)).To(BeFalse())
		})

		It("is false for a standby that was not adopted", func() {
			touch(standbySignalFile)
			Expect(IsAdoptedStandbyInRecovery(pgData)).To(BeFalse())
		})

		It("is false when the data directory does not exist", func() {
			Expect(IsAdoptedStandbyInRecovery(filepath.Join(pgData, "missing"))).To(BeFalse())
		})
	})

	Describe("clearAdoptedRecoveryMarker", func() {
		It("removes the recovery marker and keeps the adoption marker", func() {
			touch(AdoptedMarkerFile)
			touch(AdoptedRecoveryMarkerFile)
			Expect(clearAdoptedRecoveryMarker(pgData)).To(Succeed())

			Expect(fileutils.FileExists(filepath.Join(pgData, AdoptedRecoveryMarkerFile))).To(BeFalse())
			Expect(fileutils.FileExists(filepath.Join(pgData, AdoptedMarkerFile))).To(BeTrue())
		})

		It("succeeds when the recovery marker is already gone", func() {
			Expect(clearAdoptedRecoveryMarker(pgData)).To(Succeed())
		})
	})
})
