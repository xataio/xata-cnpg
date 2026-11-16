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

package walrestore

import (
	"k8s.io/utils/ptr"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Function walGSource", func() {
	walGExternal := apiv1.ExternalCluster{
		Name: "ubicloud",
		WalG: &apiv1.WalGExternalCluster{S3: apiv1.WalGS3{Prefix: "s3://pg", Region: "us-west-2"}},
	}

	It("returns nil when no wal-g archive is configured", func() {
		cluster := apiv1.Cluster{}
		Expect(walGSource(&cluster, "testPod")).To(BeNil())
	})

	It("returns the recovery source while bootstrapping from a wal-g archive", func() {
		cluster := apiv1.Cluster{Spec: apiv1.ClusterSpec{
			Bootstrap:        &apiv1.BootstrapConfiguration{Recovery: &apiv1.BootstrapRecovery{Source: "ubicloud"}},
			ExternalClusters: []apiv1.ExternalCluster{walGExternal},
		}}
		Expect(walGSource(&cluster, "testPod")).ToNot(BeNil())
	})

	It("returns the replica source for the designated primary of a replica cluster", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{CurrentPrimary: "primaryPod"},
			Spec: apiv1.ClusterSpec{
				ReplicaCluster:   &apiv1.ReplicaClusterConfiguration{Enabled: ptr.To(true), Source: "ubicloud"},
				ExternalClusters: []apiv1.ExternalCluster{walGExternal},
			},
		}
		Expect(walGSource(&cluster, "primaryPod")).ToNot(BeNil())
	})

	It("returns nil for a standby of a replica cluster, which streams from the designated primary", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{CurrentPrimary: "primaryPod"},
			Spec: apiv1.ClusterSpec{
				ReplicaCluster:   &apiv1.ReplicaClusterConfiguration{Enabled: ptr.To(true), Source: "ubicloud"},
				ExternalClusters: []apiv1.ExternalCluster{walGExternal},
			},
		}
		Expect(walGSource(&cluster, "standbyPod")).To(BeNil())
	})

	It("returns nil when the replica source is a barman object store", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{CurrentPrimary: "primaryPod"},
			Spec: apiv1.ClusterSpec{
				ReplicaCluster: &apiv1.ReplicaClusterConfiguration{Enabled: ptr.To(true), Source: "barman"},
				ExternalClusters: []apiv1.ExternalCluster{{
					Name: "barman", BarmanObjectStore: &apiv1.BarmanObjectStoreConfiguration{},
				}},
			},
		}
		Expect(walGSource(&cluster, "primaryPod")).To(BeNil())
	})
})
