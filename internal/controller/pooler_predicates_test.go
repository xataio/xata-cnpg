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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pooler_predicates unit tests", func() {
	var env *testingEnvironment
	BeforeEach(func() {
		env = buildTestEnvironment()
	})

	It("makes sure isUsefulPoolerSecret works correctly", func() {
		namespace := newFakeNamespace(env.client)
		cluster := newFakeCNPGCluster(env.client, namespace)
		pooler := newFakePooler(env.client, cluster)

		By("making sure it returns true for owned secrets", func() {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: rand.String(10), Namespace: namespace}}
			utils.SetAsOwnedBy(&secret.ObjectMeta, pooler.ObjectMeta, pooler.TypeMeta)
			isUseful := isUsefulPoolerSecret(secret)
			Expect(isUseful).To(BeTrue())
		})

		By("making sure it returns true for secrets with reload label", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rand.String(10),
					Namespace: namespace,
					Labels: map[string]string{
						utils.WatchedLabelName: "true",
					},
				},
			}
			isUseful := isUsefulPoolerSecret(secret)
			Expect(isUseful).To(BeTrue())
		})

		By("making sure it returns false with not owned secrets", func() {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: rand.String(10), Namespace: namespace}}
			isUseful := isUsefulPoolerSecret(secret)
			Expect(isUseful).To(BeFalse())
		})
	})

	Describe("clusterSwitchoverPredicate", func() {
		newCluster := func(current, target, phase string) *apiv1.Cluster {
			return &apiv1.Cluster{
				Status: apiv1.ClusterStatus{
					CurrentPrimary: current,
					TargetPrimary:  target,
					Phase:          phase,
				},
			}
		}

		It("fires when CurrentPrimary changes", func() {
			old := newCluster("pod-1", "pod-2", apiv1.PhaseSwitchover)
			updated := newCluster("pod-2", "pod-2", apiv1.PhaseSwitchover)
			Expect(clusterSwitchoverPredicate.UpdateFunc(event.UpdateEvent{
				ObjectOld: old, ObjectNew: updated,
			})).To(BeTrue())
		})

		It("fires when TargetPrimary changes", func() {
			old := newCluster("pod-1", "pod-1", apiv1.PhaseHealthy)
			updated := newCluster("pod-1", "pod-2", apiv1.PhaseSwitchover)
			Expect(clusterSwitchoverPredicate.UpdateFunc(event.UpdateEvent{
				ObjectOld: old, ObjectNew: updated,
			})).To(BeTrue())
		})

		It("fires when Phase changes even if primaries are unchanged", func() {
			// Regression: the resume edge in reconcileSwitchoverPause is gated
			// on PhaseHealthy, so a Phase-only update during settle must wake
			// the reconciler.
			old := newCluster("pod-2", "pod-2", apiv1.PhaseFailOver)
			updated := newCluster("pod-2", "pod-2", apiv1.PhaseHealthy)
			Expect(clusterSwitchoverPredicate.UpdateFunc(event.UpdateEvent{
				ObjectOld: old, ObjectNew: updated,
			})).To(BeTrue())
		})

		It("does not fire when nothing relevant changed", func() {
			old := newCluster("pod-1", "pod-1", apiv1.PhaseHealthy)
			updated := newCluster("pod-1", "pod-1", apiv1.PhaseHealthy)
			Expect(clusterSwitchoverPredicate.UpdateFunc(event.UpdateEvent{
				ObjectOld: old, ObjectNew: updated,
			})).To(BeFalse())
		})
	})

	It("makes sure isOwnedByPoolerOrSatisfiesPredicate works correctly", func() {
		namespace := newFakeNamespace(env.client)
		cluster := newFakeCNPGCluster(env.client, namespace)
		pooler := newFakePooler(env.client, cluster)

		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: rand.String(10), Namespace: namespace}}
		utils.SetAsOwnedBy(&secret.ObjectMeta, pooler.ObjectMeta, pooler.TypeMeta)
		isOwnedByPoolerOrSatisfiesPredicate(secret, func(_ client.Object) bool {
			return false
		})
	})
})
