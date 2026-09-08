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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Instance reload state", func() {
	It("does nothing when no reload is pending", func() {
		reconciler := &InstanceReconciler{}

		err := reconciler.reconcilePendingReload(context.Background(), nil, false)

		Expect(err).ToNot(HaveOccurred())
		Expect(reconciler.reloadPending.Load()).To(BeFalse())
	})

	It("marks a reload as pending", func() {
		reconciler := &InstanceReconciler{}

		reconciler.markReloadPending()

		Expect(reconciler.reloadPending.Load()).To(BeTrue())
	})

	It("is cleared when an intervening restart applies the configuration", func() {
		reconciler := &InstanceReconciler{}
		reconciler.markReloadPending()

		err := reconciler.reconcilePendingReload(context.Background(), nil, true)

		Expect(err).ToNot(HaveOccurred())
		Expect(reconciler.reloadPending.Load()).To(BeFalse())
	})
})
