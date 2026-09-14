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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	schemeBuilder "github.com/xataio/xata-cnpg/internal/scheme"
	"github.com/xataio/xata-cnpg/pkg/management/postgres"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Noop bootstrap startup", func() {
	It("marks the target as current primary after PGDATA appears", func(ctx SpecContext) {
		const (
			clusterName = "test"
			namespace   = "default"
			podName     = "test-1"
		)

		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: apiv1.ClusterSpec{
				Bootstrap: &apiv1.BootstrapConfiguration{
					Noop: &apiv1.BootstrapNoop{},
				},
			},
			Status: apiv1.ClusterStatus{
				TargetPrimary: podName,
			},
		}

		scheme := schemeBuilder.BuildWithAllKnownScheme()
		k8sClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&apiv1.Cluster{}).
			WithObjects(cluster).
			Build()

		instance := postgres.NewInstance().
			WithClusterName(clusterName).
			WithNamespace(namespace).
			WithPodName(podName)
		instance.PgData = GinkgoT().TempDir()

		reconciler := &InstanceReconciler{
			client:   k8sClient,
			instance: instance,
		}

		persistedCluster := &apiv1.Cluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), persistedCluster)).To(Succeed())
		Expect(reconciler.verifyPgDataCoherenceForPrimary(ctx, persistedCluster)).To(Succeed())

		updatedCluster := &apiv1.Cluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), updatedCluster)).To(Succeed())
		Expect(updatedCluster.Status.CurrentPrimary).To(Equal(podName))
		Expect(updatedCluster.Status.CurrentPrimaryTimestamp).ToNot(BeEmpty())
	})
})
