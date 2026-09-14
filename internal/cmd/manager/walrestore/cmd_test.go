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
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/pgbackrest"
	"github.com/xataio/xata-cnpg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Function isStreamingAvailable", func() {
	It("returns false if cluster is nil", func() {
		Expect(isStreamingAvailable(nil, "testPod")).To(BeFalse())
	})

	It("returns true if current primary does not match the given pod name", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
		}
		Expect(isStreamingAvailable(&cluster, "replicaPod")).To(BeTrue())
	})

	It("returns false if current primary matches the given pod name and this is not a replica cluster", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
		}
		Expect(isStreamingAvailable(&cluster, "primaryPod")).To(BeFalse())
	})

	It("returns false if there are not connection parameters and this is a replica cluster", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
			Spec: apiv1.ClusterSpec{
				ExternalClusters: []apiv1.ExternalCluster{
					{
						Name: "clusterSource",
					},
				},
				ReplicaCluster: &apiv1.ReplicaClusterConfiguration{
					Enabled: ptr.To(true),
					Source:  "clusterSource",
				},
			},
		}
		Expect(isStreamingAvailable(&cluster, "primaryPod")).To(BeFalse())
	})

	It("returns false if this is a replica cluster, "+
		"but replica cluster source does not match external cluster name", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
			Spec: apiv1.ClusterSpec{
				ExternalClusters: []apiv1.ExternalCluster{
					{
						Name: "wrongNameClusterSource",
					},
				},
				ReplicaCluster: &apiv1.ReplicaClusterConfiguration{
					Enabled: ptr.To(true),
					Source:  "clusterSource",
				},
			},
		}
		Expect(isStreamingAvailable(&cluster, "primaryPod")).To(BeFalse())
	})

	It("returns true if the external cluster has streaming connection and this is a replica cluster", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
			Spec: apiv1.ClusterSpec{
				ExternalClusters: []apiv1.ExternalCluster{
					{
						Name:                 "clusterSource",
						ConnectionParameters: map[string]string{"dbname": "test"},
					},
				},
				ReplicaCluster: &apiv1.ReplicaClusterConfiguration{
					Enabled: ptr.To(true),
					Source:  "clusterSource",
				},
			},
		}
		Expect(isStreamingAvailable(&cluster, "primaryPod")).To(BeTrue())
	})
})

var _ = Describe("Function shouldRestoreViaPgBackRest", func() {
	pgBackRestBackup := &apiv1.BackupConfiguration{
		PgBackRest: &apiv1.PgBackRestConfiguration{
			Repository: &apiv1.PgBackRestRepository{},
		},
	}

	It("returns false if no backup is configured", func() {
		cluster := apiv1.Cluster{}
		Expect(shouldRestoreViaPgBackRest(&cluster, "testPod")).To(BeFalse())
	})

	It("returns false if only barman is configured", func() {
		cluster := apiv1.Cluster{
			Spec: apiv1.ClusterSpec{
				Backup: &apiv1.BackupConfiguration{
					BarmanObjectStore: &apiv1.BarmanObjectStoreConfiguration{},
				},
			},
		}
		Expect(shouldRestoreViaPgBackRest(&cluster, "testPod")).To(BeFalse())
	})

	It("returns false if pgbackrest is configured without a repository", func() {
		cluster := apiv1.Cluster{
			Spec: apiv1.ClusterSpec{
				Backup: &apiv1.BackupConfiguration{
					PgBackRest: &apiv1.PgBackRestConfiguration{},
				},
			},
		}
		Expect(shouldRestoreViaPgBackRest(&cluster, "testPod")).To(BeFalse())
	})

	It("returns true if a pgbackrest repository is configured", func() {
		cluster := apiv1.Cluster{
			Spec: apiv1.ClusterSpec{
				Backup: pgBackRestBackup,
			},
		}
		Expect(shouldRestoreViaPgBackRest(&cluster, "testPod")).To(BeTrue())
	})

	It("returns false for the designated primary of a replica cluster", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
			Spec: apiv1.ClusterSpec{
				Backup: pgBackRestBackup,
				ReplicaCluster: &apiv1.ReplicaClusterConfiguration{
					Enabled: ptr.To(true),
					Source:  "clusterSource",
				},
			},
		}
		Expect(shouldRestoreViaPgBackRest(&cluster, "primaryPod")).To(BeFalse())
	})

	It("returns true for a replica pod of a replica cluster", func() {
		cluster := apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				CurrentPrimary: "primaryPod",
			},
			Spec: apiv1.ClusterSpec{
				Backup: pgBackRestBackup,
				ReplicaCluster: &apiv1.ReplicaClusterConfiguration{
					Enabled: ptr.To(true),
					Source:  "clusterSource",
				},
			},
		}
		Expect(shouldRestoreViaPgBackRest(&cluster, "replicaPod")).To(BeTrue())
	})
})

var _ = Describe("Function restoreWALViaPgBackRest", func() {
	It("reports WAL not found while pgbackrest is suspended", func() {
		cluster := apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					utils.PgBackRestSuspended: "enabled",
				},
			},
			Spec: apiv1.ClusterSpec{
				Backup: &apiv1.BackupConfiguration{
					PgBackRest: &apiv1.PgBackRestConfiguration{
						Repository: &apiv1.PgBackRestRepository{},
					},
				},
			},
		}
		err := restoreWALViaPgBackRest(context.TODO(), &cluster,
			"000000010000000000000001", "pg_wal/RECOVERYXLOG")
		Expect(err).To(MatchError(pgbackrest.ErrWALNotFound))
	})
})
