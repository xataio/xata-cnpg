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

package controller

import (
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pgbackrest stanzaCreateNeeded", func() {
	It("returns true when no stanza has been created yet", func() {
		Expect(stanzaCreateNeeded(nil, "branch-abc")).To(BeTrue())
	})

	It("returns false when the same stanza was already created", func() {
		Expect(stanzaCreateNeeded(ptr.To("branch-abc"), "branch-abc")).To(BeFalse())
	})

	It("returns true when the stanza name changed (e.g. pool cluster adopted by a branch)", func() {
		// Pool cluster created its own (cluster-name) stanza; on adoption the
		// stanza switches to the branch id and must be created, not skipped.
		Expect(stanzaCreateNeeded(ptr.To("pool-cluster-xyz"), "branch-abc")).To(BeTrue())
	})
})
