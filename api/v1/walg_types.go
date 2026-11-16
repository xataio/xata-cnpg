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

// WalGExternalCluster describes a WAL-G archive (base backups plus WAL
// segments) that an external cluster keeps in an S3-compatible bucket. It is
// a read-only recovery source: a cluster can bootstrap from it and, as a
// replica cluster, keep replaying the WAL it receives. CloudNativePG never
// writes to it.
type WalGExternalCluster struct {
	// The S3 location of the archive
	S3 WalGS3 `json:"s3"`

	// The name of the base backup to restore, as listed by `wal-g backup-list`.
	// Defaults to LATEST.
	// +optional
	BackupName string `json:"backupName,omitempty"`
}

// WalGS3 configures how wal-g reaches an S3-compatible bucket
type WalGS3 struct {
	// The archive prefix, e.g. `s3://bucket` or `s3://bucket/path`. Passed to
	// wal-g as WALG_S3_PREFIX.
	// +kubebuilder:validation:Pattern=`^s3://.+`
	Prefix string `json:"prefix"`

	// The S3 region, passed as AWS_REGION
	Region string `json:"region"`

	// The S3 endpoint, overriding the automatic endpoint discovery. Required
	// for non-AWS S3-compatible storage (e.g. MinIO). Passed as AWS_ENDPOINT.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Use path-style bucket addressing (AWS_S3_FORCE_PATH_STYLE)
	// +optional
	ForcePathStyle bool `json:"forcePathStyle,omitempty"`

	// The reference to the access key id. When both static credential
	// references are absent, wal-g uses the ambient AWS credential chain
	// (instance profile, IRSA, pod identity).
	// +optional
	AccessKeyID *SecretKeySelector `json:"accessKeyId,omitempty"`

	// The reference to the secret access key
	// +optional
	SecretAccessKey *SecretKeySelector `json:"secretAccessKey,omitempty"`
}

// HasStaticCredentials reports whether static S3 credentials are configured
func (s *WalGS3) HasStaticCredentials() bool {
	return s != nil && s.AccessKeyID != nil && s.SecretAccessKey != nil
}

// WalGRecoverySource holds the wal-g configuration needed to restore from, or
// follow, an external cluster's archive.
type WalGRecoverySource struct {
	// Name of the external cluster the archive belongs to
	Name string
	// The archive configuration
	WalG *WalGExternalCluster
}

// GetRecoverySourceWalG returns the wal-g recovery source if the cluster is
// bootstrapping from a wal-g archive. Returns nil otherwise.
func (cluster *Cluster) GetRecoverySourceWalG() *WalGRecoverySource {
	if cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery == nil {
		return nil
	}

	return cluster.walGSource(cluster.Spec.Bootstrap.Recovery.Source)
}

// GetReplicaSourceWalG returns the wal-g recovery source if this is a replica
// cluster whose replication origin is a wal-g archive. Returns nil otherwise.
func (cluster *Cluster) GetReplicaSourceWalG() *WalGRecoverySource {
	if !cluster.IsReplica() || cluster.Spec.ReplicaCluster == nil {
		return nil
	}

	return cluster.walGSource(cluster.Spec.ReplicaCluster.Source)
}

func (cluster *Cluster) walGSource(externalClusterName string) *WalGRecoverySource {
	if len(externalClusterName) == 0 {
		return nil
	}

	externalCluster, found := cluster.ExternalCluster(externalClusterName)
	if !found || externalCluster.WalG == nil {
		return nil
	}

	return &WalGRecoverySource{
		Name: externalCluster.Name,
		WalG: externalCluster.WalG,
	}
}
