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

package persistentvolumeclaim

import (
	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/utils"
)

// StorageSource the storage source to be used when creating a set
// of PVCs
type StorageSource struct {
	// The data source that should be used for PGDATA
	DataSource corev1.TypedLocalObjectReference `json:"dataSource"`

	// The (optional) data source that should be used for WALs
	WALSource *corev1.TypedLocalObjectReference `json:"walSource"`

	// The (optional) data source that should be used for TABLESPACE
	TablespaceSource map[string]corev1.TypedLocalObjectReference `json:"tablespaceSource"`
}

// GetCandidateStorageSourceForPrimary gets the candidate storage source
// to be used to create a primary PVC
func GetCandidateStorageSourceForPrimary(
	cluster *apiv1.Cluster,
	backup *apiv1.Backup,
) *StorageSource {
	if backup.IsCompletedVolumeSnapshot() {
		return getCandidateSourceFromBackup(backup)
	}
	return getCandidateSourceFromClusterDefinition(cluster)
}

func getCandidateSourceFromBackup(backup *apiv1.Backup) *StorageSource {
	var result StorageSource
	for _, element := range backup.Status.BackupSnapshotStatus.Elements {
		reference := corev1.TypedLocalObjectReference{
			APIGroup: ptr.To(volumesnapshotv1.GroupName),
			Kind:     apiv1.VolumeSnapshotKind,
			Name:     element.Name,
		}
		switch utils.PVCRole(element.Type) {
		case utils.PVCRolePgData:
			result.DataSource = reference
		case utils.PVCRolePgWal:
			result.WALSource = &reference
		case utils.PVCRolePgTablespace:
			if result.TablespaceSource == nil {
				result.TablespaceSource = map[string]corev1.TypedLocalObjectReference{}
			}
			result.TablespaceSource[element.TablespaceName] = reference
		}
	}

	return &result
}

// getCandidateSourceFromClusterDefinition gets a candidate storage source
// from a Cluster definition, taking into consideration the backup that the
// cluster has been bootstrapped from
func getCandidateSourceFromClusterDefinition(cluster *apiv1.Cluster) *StorageSource {
	if cluster.Spec.Bootstrap == nil ||
		cluster.Spec.Bootstrap.Recovery == nil ||
		cluster.Spec.Bootstrap.Recovery.VolumeSnapshots == nil {
		return nil
	}

	volumeSnapshots := cluster.Spec.Bootstrap.Recovery.VolumeSnapshots
	return &StorageSource{
		DataSource:       volumeSnapshots.Storage,
		WALSource:        volumeSnapshots.WalStorage,
		TablespaceSource: volumeSnapshots.TablespaceStorage,
	}
}
