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
	barmanApi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupPhase is the phase of the backup
type BackupPhase string

const (
	// BackupPhasePending means that the backup is still waiting to be started
	BackupPhasePending = "pending"

	// BackupPhaseStarted means that the backup is now running
	BackupPhaseStarted = "started"

	// BackupPhaseRunning means that the backup is now running
	BackupPhaseRunning = "running"

	// BackupPhaseFinalizing means that a consistent backup have been
	// taken and the operator is waiting for it to be ready to be
	// used to restore a cluster.
	// This phase is used for VolumeSnapshot backups, when a
	// VolumeSnapshotContent have already been provisioned, but it is
	// still now waiting for the `readyToUse` flag to be true.
	BackupPhaseFinalizing = "finalizing"

	// BackupPhaseCompleted means that the backup is now completed
	BackupPhaseCompleted = "completed"

	// BackupPhaseFailed means that the backup is failed
	BackupPhaseFailed = "failed"

	// BackupPhaseWalArchivingFailing means wal archiving isn't properly working
	BackupPhaseWalArchivingFailing = "walArchivingFailing"
)

// BarmanCredentials an object containing the potential credentials for each cloud provider
// +kubebuilder:object:generate:=false
type BarmanCredentials = barmanApi.BarmanCredentials

// AzureCredentials is the type for the credentials to be used to upload
// files to Azure Blob Storage. The connection string contains every needed
// information. If the connection string is not specified, we'll need the
// storage account name and also one (and only one) of:
//
// - storageKey
// - storageSasToken
//
// - inheriting the credentials from the pod environment by setting inheritFromAzureAD to true
// +kubebuilder:object:generate:=false
type AzureCredentials = barmanApi.AzureCredentials

// BarmanObjectStoreConfiguration contains the backup configuration
// using Barman against an S3-compatible object storage
// +kubebuilder:object:generate:=false
type BarmanObjectStoreConfiguration = barmanApi.BarmanObjectStoreConfiguration

// DataBackupConfiguration is the configuration of the backup of
// the data directory
// +kubebuilder:object:generate:=false
type DataBackupConfiguration = barmanApi.DataBackupConfiguration

// GoogleCredentials is the type for the Google Cloud Storage credentials.
// This needs to be specified even if we run inside a GKE environment.
// +kubebuilder:object:generate:=false
type GoogleCredentials = barmanApi.GoogleCredentials

// S3Credentials is the type for the credentials to be used to upload
// files to S3. It can be provided in two alternative ways:
//
// - explicitly passing accessKeyId and secretAccessKey
//
// - inheriting the role from the pod environment by setting inheritFromIAMRole to true
// +kubebuilder:object:generate:=false
type S3Credentials = barmanApi.S3Credentials

// WalBackupConfiguration is the configuration of the backup of the
// WAL stream
// +kubebuilder:object:generate:=false
type WalBackupConfiguration = barmanApi.WalBackupConfiguration

// BackupMethod defines the way of executing the physical base backups of
// the selected PostgreSQL instance
type BackupMethod string

const (
	// BackupMethodVolumeSnapshot means using the volume snapshot
	// Kubernetes feature
	BackupMethodVolumeSnapshot BackupMethod = "volumeSnapshot"

	// BackupMethodBarmanObjectStore means using barman to backup the
	// PostgreSQL cluster
	BackupMethodBarmanObjectStore BackupMethod = "barmanObjectStore"

	// BackupMethodPlugin means that this backup should be handled by
	// a plugin
	BackupMethodPlugin BackupMethod = "plugin"

	// BackupMethodPgBackRest means using pgbackrest for backups
	BackupMethodPgBackRest BackupMethod = "pgBackRest"
)

// PgBackRestBackupType defines the type of pgbackrest backup.
type PgBackRestBackupType string

const (
	// PgBackRestBackupTypeFull is a complete backup of everything.
	PgBackRestBackupTypeFull PgBackRestBackupType = "full"

	// PgBackRestBackupTypeDiff is a differential backup — changes since the last full backup.
	PgBackRestBackupTypeDiff PgBackRestBackupType = "diff"

	// PgBackRestBackupTypeIncr is an incremental backup — changes since the last backup of any type.
	PgBackRestBackupTypeIncr PgBackRestBackupType = "incr"
)

// PgBackRestConfiguration defines the backup configuration using pgbackrest.
type PgBackRestConfiguration struct {
	Repository *PgBackRestRepository `json:"repository"`
	Options    *PgBackRestOptions    `json:"options,omitempty"`
}

// PgBackRestExternalCluster defines the pgbackrest configuration for an external cluster,
// used for restore operations. Contains the repository location and optional process-level
// settings (e.g. processMax, delta, priority).
type PgBackRestExternalCluster struct {
	Repository PgBackRestRepository `json:"repository"`
	// +optional
	Options *PgBackRestOptions `json:"options,omitempty"`
}

// PgBackRestRepository defines the storage repository for pgbackrest.
// Exactly one of s3, gcs, or azure must be specified.
// +kubebuilder:validation:XValidation:rule="(has(self.s3) ? 1 : 0) + (has(self.gcs) ? 1 : 0) + (has(self.azure) ? 1 : 0) == 1",message="exactly one of s3, gcs, or azure must be specified"
type PgBackRestRepository struct {
	S3    *PgBackRestS3    `json:"s3,omitempty"`
	GCS   *PgBackRestGCS   `json:"gcs,omitempty"`
	Azure *PgBackRestAzure `json:"azure,omitempty"`
}

// PgBackRestS3 defines the S3-compatible storage configuration for pgbackrest.
// +kubebuilder:validation:XValidation:rule="(self.inheritFromIAMRole == true) != (has(self.accessKeyId) && has(self.secretAccessKey))",message="either inheritFromIAMRole or both accessKeyId and secretAccessKey must be specified, but not both"
type PgBackRestS3 struct {
	// The S3 bucket name
	Bucket string `json:"bucket"`
	// The S3 region
	Region string `json:"region"`
	// The S3 endpoint, overriding the automatic endpoint discovery.
	// Required for non-AWS S3-compatible storage (e.g. MinIO).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// The reference to the access key id
	// +optional
	AccessKeyID *SecretKeySelector `json:"accessKeyId,omitempty"`
	// The reference to the secret access key
	// +optional
	SecretAccessKey *SecretKeySelector `json:"secretAccessKey,omitempty"`
	// Use IAM role-based authentication (e.g. IRSA, instance profile).
	// Sets pgbackrest repo1-s3-key-type=auto.
	// +optional
	InheritFromIAMRole bool `json:"inheritFromIAMRole,omitempty"`
}

// PgBackRestRetention defines the backup retention policy for pgbackrest.
type PgBackRestRetention struct {
	// Number of full backups to retain. Required.
	// +kubebuilder:validation:Minimum=1
	Full int `json:"full"`
	// Type of full backup retention: "count" (number of backups) or "time" (days).
	// Defaults to "count".
	// +optional
	// +kubebuilder:validation:Enum=count;time
	// +kubebuilder:default:=count
	FullType string `json:"fullType,omitempty"`
	// Number of WAL archive sets to retain. If not set, pgbackrest will
	// use the full backup retention to determine WAL expiration.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Archive *int `json:"archive,omitempty"`
	// TODO: add differential backup retention (diff, diffType) when
	// differential backups are supported
}

// PgBackRestOptions defines process-level settings for pgbackrest.
// +kubebuilder:validation:XValidation:rule="!has(self.blockIncremental) || !self.blockIncremental || (has(self.bundle) && self.bundle)",message="blockIncremental requires bundle to be true"
type PgBackRestOptions struct {
	// Compression algorithm. Defaults to "lz4".
	// +optional
	// +kubebuilder:validation:Enum=none;gz;bz2;lz4;zst
	// +kubebuilder:default:=lz4
	CompressType string `json:"compressType,omitempty"`
	// Compression level (0-9). The meaning depends on the algorithm.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=9
	CompressLevel *int `json:"compressLevel,omitempty"`
	// Maximum number of parallel processes for backup/restore.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ProcessMax *int `json:"processMax,omitempty"`
	// Force an immediate checkpoint at backup start instead of
	// waiting for the next scheduled checkpoint.
	// +optional
	StartFast *bool `json:"startFast,omitempty"`
	// Only restore files that have changed. Speeds up restores.
	// +optional
	Delta *bool `json:"delta,omitempty"`
	// Enable async WAL archiving for better throughput.
	// +optional
	ArchiveAsync *bool `json:"archiveAsync,omitempty"`
	// Max size of the WAL archive push queue when archiveAsync is enabled.
	// Uses pgbackrest size format (e.g. "1GiB", "256MiB").
	// +optional
	ArchivePushQueueMax string `json:"archivePushQueueMax,omitempty"`
	// Max size of the WAL restore queue when archiveAsync is enabled.
	// Uses pgbackrest size format (e.g. "1GiB", "256MiB").
	// +optional
	ArchiveGetQueueMax string `json:"archiveGetQueueMax,omitempty"`
	// Bundle small files together for more efficient object storage operations.
	// pgbackrest defaults: bundle-limit=2MiB (max file size eligible for bundling),
	// bundle-size=20MiB (target size per bundle). Files larger than bundle-limit
	// are stored individually.
	// TODO: consider exposing bundleLimit and bundleSize as tunable options
	// +optional
	Bundle *bool `json:"bundle,omitempty"`
	// Enable block-level incremental backup. Only changed blocks within files
	// are backed up instead of entire files. Requires Bundle to be true.
	// Requires pgbackrest 2.46+.
	// +optional
	BlockIncremental *bool `json:"blockIncremental,omitempty"`
	// Take backups from a standby instance instead of the primary,
	// reducing load on the primary.
	// +optional
	BackupStandby *bool `json:"backupStandby,omitempty"`
	// Allow for deprioritization when taking CPU (nice value), making sure we are
	// not impacting postgres TPS. If not set defaults to 0 - which means no
	// deprioritization. Do not go below 0 because that would make it more important than postgres
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=19
	Priority *int `json:"priority,omitempty"`
	// Backup retention policy.
	// +optional
	Retention *PgBackRestRetention `json:"retention,omitempty"`
	// RepoPath is the pgbackrest repo1-path — the prefix in the storage
	// backend where backups and WAL archives are stored. When unset, it
	// defaults to /<clusterName>. Configure this to isolate a new cluster
	// incarnation from existing data under the default path, or to restore
	// from a custom path.
	// +optional
	RepoPath string `json:"repoPath,omitempty"`
	// TODO: add in future iterations:
	// - encryption: cipherType, cipherPass
	// - backup behavior: stopAuto, manifestSaveThreshold, resumeOff
	// - network/performance: bufferSize, protocolTimeout, ioReadRateMax, ioWriteRateMax, ioBurstDurationSec
	// - WAL: archiveTimeout, archiveMissing
	// - repo: repoHardlink, bundleLimit
}

// PgBackRestGCS defines the Google Cloud Storage configuration for pgbackrest.
// TODO: implement in a future iteration
type PgBackRestGCS struct{}

// PgBackRestAzure defines the Azure Blob Storage configuration for pgbackrest.
// TODO: implement in a future iteration
type PgBackRestAzure struct{}

// BackupSpec defines the desired state of Backup
// +kubebuilder:validation:XValidation:rule="oldSelf == self",message="BackupSpec is immutable once set"
type BackupSpec struct {
	// The cluster to backup
	Cluster LocalObjectReference `json:"cluster"`

	// The policy to decide which instance should perform this backup. If empty,
	// it defaults to `cluster.spec.backup.target`.
	// Available options are empty string, `primary` and `prefer-standby`.
	// `primary` to have backups run always on primary instances,
	// `prefer-standby` to have backups run preferably on the most updated
	// standby, if available.
	// +optional
	// +kubebuilder:validation:Enum=primary;prefer-standby
	Target BackupTarget `json:"target,omitempty"`

	// The backup method to be used, possible options are `barmanObjectStore`,
	// `volumeSnapshot`, `plugin` or `pgBackRest`. Defaults to: `pgBackRest`.
	// +optional
	// +kubebuilder:validation:Enum=barmanObjectStore;volumeSnapshot;plugin;pgBackRest
	// +kubebuilder:default:=pgBackRest
	Method BackupMethod `json:"method,omitempty"`

	// The pgBackRest backup type. Possible values are `full`, `diff` (differential),
	// or `incr` (incremental). Defaults to `full`. Only used when method is `pgBackRest`.
	// +optional
	// +kubebuilder:validation:Enum=full;diff;incr
	// +kubebuilder:default:=full
	PgBackRestBackupType PgBackRestBackupType `json:"pgBackRestBackupType,omitempty"`

	// Configuration parameters passed to the plugin managing this backup
	// +optional
	PluginConfiguration *BackupPluginConfiguration `json:"pluginConfiguration,omitempty"`

	// Whether the default type of backup with volume snapshots is
	// online/hot (`true`, default) or offline/cold (`false`)
	// Overrides the default setting specified in the cluster field '.spec.backup.volumeSnapshot.online'
	// +optional
	Online *bool `json:"online,omitempty"`

	// Configuration parameters to control the online/hot backup with volume snapshots
	// Overrides the default settings specified in the cluster '.backup.volumeSnapshot.onlineConfiguration' stanza
	// +optional
	OnlineConfiguration *OnlineConfiguration `json:"onlineConfiguration,omitempty"`
}

// BackupPluginConfiguration contains the backup configuration used by
// the backup plugin
type BackupPluginConfiguration struct {
	// Name is the name of the plugin managing this backup
	Name string `json:"name"`

	// Parameters are the configuration parameters passed to the backup
	// plugin for this backup
	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`
}

// BackupSnapshotStatus the fields exclusive to the volumeSnapshot method backup
type BackupSnapshotStatus struct {
	// The elements list, populated with the gathered volume snapshots
	// +optional
	Elements []BackupSnapshotElementStatus `json:"elements,omitempty"`
}

// BackupSnapshotElementStatus is a volume snapshot that is part of a volume snapshot method backup
type BackupSnapshotElementStatus struct {
	// Name is the snapshot resource name
	Name string `json:"name"`

	// Type is tho role of the snapshot in the cluster, such as PG_DATA, PG_WAL and PG_TABLESPACE
	Type string `json:"type"`

	// TablespaceName is the name of the snapshotted tablespace. Only set
	// when type is PG_TABLESPACE
	// +optional
	TablespaceName string `json:"tablespaceName,omitempty"`
}

// BackupStatus defines the observed state of Backup
type BackupStatus struct {
	// The potential credentials for each cloud provider
	BarmanCredentials `json:",inline"`

	// The PostgreSQL major version that was running when the
	// backup was taken.
	MajorVersion int `json:"majorVersion,omitempty"`

	// EndpointCA store the CA bundle of the barman endpoint.
	// Useful when using self-signed certificates to avoid
	// errors with certificate issuer and barman-cloud-wal-archive.
	// +optional
	EndpointCA *SecretKeySelector `json:"endpointCA,omitempty"`

	// Endpoint to be used to upload data to the cloud,
	// overriding the automatic endpoint discovery
	// +optional
	EndpointURL string `json:"endpointURL,omitempty"`

	// The path where to store the backup (i.e. s3://bucket/path/to/folder)
	// this path, with different destination folders, will be used for WALs
	// and for data. This may not be populated in case of errors.
	// +optional
	DestinationPath string `json:"destinationPath,omitempty"`

	// The server name on S3, the cluster name is used if this
	// parameter is omitted
	// +optional
	ServerName string `json:"serverName,omitempty"`

	// Encryption method required to S3 API
	// +optional
	Encryption string `json:"encryption,omitempty"`

	// The ID of the Barman backup
	// +optional
	BackupID string `json:"backupId,omitempty"`

	// The Name of the Barman backup
	// +optional
	BackupName string `json:"backupName,omitempty"`

	// The last backup status
	// +optional
	Phase BackupPhase `json:"phase,omitempty"`

	// When the backup was started
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// When the backup was terminated
	// +optional
	StoppedAt *metav1.Time `json:"stoppedAt,omitempty"`

	// The starting WAL
	// +optional
	BeginWal string `json:"beginWal,omitempty"`

	// The ending WAL
	// +optional
	EndWal string `json:"endWal,omitempty"`

	// The starting xlog
	// +optional
	BeginLSN string `json:"beginLSN,omitempty"`

	// The ending xlog
	// +optional
	EndLSN string `json:"endLSN,omitempty"`

	// The detected error
	// +optional
	Error string `json:"error,omitempty"`

	// Unused. Retained for compatibility with old versions.
	// +optional
	CommandOutput string `json:"commandOutput,omitempty"`

	// The backup command output in case of error
	// +optional
	CommandError string `json:"commandError,omitempty"`

	// Backup label file content as returned by Postgres in case of online (hot) backups
	// +optional
	BackupLabelFile []byte `json:"backupLabelFile,omitempty"`

	// Tablespace map file content as returned by Postgres in case of online (hot) backups
	// +optional
	TablespaceMapFile []byte `json:"tablespaceMapFile,omitempty"`

	// Information to identify the instance where the backup has been taken from
	// +optional
	InstanceID *InstanceID `json:"instanceID,omitempty"`

	// Status of the volumeSnapshot backup
	// +optional
	BackupSnapshotStatus BackupSnapshotStatus `json:"snapshotBackupStatus,omitempty"`

	// The backup method being used
	// +optional
	Method BackupMethod `json:"method,omitempty"`

	// Whether the backup was online/hot (`true`) or offline/cold (`false`)
	// +optional
	Online *bool `json:"online,omitempty"`

	// A map containing the plugin metadata
	// +optional
	PluginMetadata map[string]string `json:"pluginMetadata,omitempty"`
}

// InstanceID contains the information to identify an instance
type InstanceID struct {
	// The pod name
	// +optional
	PodName string `json:"podName,omitempty"`
	// The container ID
	// +optional
	ContainerID string `json:"ContainerID,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.cluster.name"
// +kubebuilder:printcolumn:name="Method",type="string",JSONPath=".spec.method"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".status.error"

// A Backup resource is a request for a PostgreSQL backup by the user.
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// Specification of the desired behavior of the backup.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	Spec BackupSpec `json:"spec"`
	// Most recently observed status of the backup. This data may not be up to
	// date. Populated by the system. Read-only.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Status BackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupList contains a list of Backup
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// List of backups
	Items []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Backup{}, &BackupList{})
}
