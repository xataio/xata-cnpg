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

package v1

import (
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("wal-g recovery source", func() {
	walGExternal := ExternalCluster{
		Name: "ubicloud",
		WalG: &WalGExternalCluster{
			S3: WalGS3{Prefix: "s3://pg-ubid", Region: "us-west-2"},
		},
	}

	It("returns nil when bootstrap is nil", func() {
		cluster := &Cluster{}
		Expect(cluster.GetRecoverySourceWalG()).To(BeNil())
	})

	It("returns nil when the recovery source is empty", func() {
		cluster := &Cluster{Spec: ClusterSpec{
			Bootstrap: &BootstrapConfiguration{Recovery: &BootstrapRecovery{}},
		}}
		Expect(cluster.GetRecoverySourceWalG()).To(BeNil())
	})

	It("returns nil when the external cluster has no wal-g archive", func() {
		cluster := &Cluster{Spec: ClusterSpec{
			Bootstrap:        &BootstrapConfiguration{Recovery: &BootstrapRecovery{Source: "other"}},
			ExternalClusters: []ExternalCluster{{Name: "other", BarmanObjectStore: &BarmanObjectStoreConfiguration{}}},
		}}
		Expect(cluster.GetRecoverySourceWalG()).To(BeNil())
	})

	It("resolves the wal-g archive of the recovery source", func() {
		cluster := &Cluster{Spec: ClusterSpec{
			Bootstrap:        &BootstrapConfiguration{Recovery: &BootstrapRecovery{Source: "ubicloud"}},
			ExternalClusters: []ExternalCluster{walGExternal},
		}}
		source := cluster.GetRecoverySourceWalG()
		Expect(source).ToNot(BeNil())
		Expect(source.Name).To(Equal("ubicloud"))
		Expect(source.WalG.S3.Prefix).To(Equal("s3://pg-ubid"))
	})

	It("returns no replica source when replica mode is off", func() {
		cluster := &Cluster{Spec: ClusterSpec{
			ReplicaCluster:   &ReplicaClusterConfiguration{Enabled: ptr.To(false), Source: "ubicloud"},
			ExternalClusters: []ExternalCluster{walGExternal},
		}}
		Expect(cluster.GetReplicaSourceWalG()).To(BeNil())
	})

	It("resolves the wal-g archive of a replica cluster", func() {
		cluster := &Cluster{Spec: ClusterSpec{
			ReplicaCluster:   &ReplicaClusterConfiguration{Enabled: ptr.To(true), Source: "ubicloud"},
			ExternalClusters: []ExternalCluster{walGExternal},
		}}
		source := cluster.GetReplicaSourceWalG()
		Expect(source).ToNot(BeNil())
		Expect(source.Name).To(Equal("ubicloud"))
	})

	It("reports static credentials only when both keys are set", func() {
		s3 := &WalGS3{}
		Expect(s3.HasStaticCredentials()).To(BeFalse())
		s3.AccessKeyID = &SecretKeySelector{Key: "id"}
		Expect(s3.HasStaticCredentials()).To(BeFalse())
		s3.SecretAccessKey = &SecretKeySelector{Key: "key"}
		Expect(s3.HasStaticCredentials()).To(BeTrue())
	})
})
