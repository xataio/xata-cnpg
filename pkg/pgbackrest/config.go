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

package pgbackrest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/postgres"
)

// GenerateConfig builds a pgbackrest.conf INI configuration from the cluster
// spec. It resolves S3 credentials from Kubernetes secrets.
// isPrimary controls whether this pod gets replica-specific config for
// backup-standby (pg2-host pointing to the primary via TLS).
func GenerateConfig(
	ctx context.Context,
	k8sClient client.Client,
	cluster *apiv1.Cluster,
	pgDataPath string,
	isPrimary bool,
) (string, error) {
	pgbackrestConfig := cluster.Spec.Backup.PgBackRest

	cfg, err := generateBaseConfig(
		ctx, k8sClient, cluster.Namespace,
		pgbackrestConfig.Repository, cluster.GetPgBackRestStanzaName(), pgDataPath,
	)
	if err != nil {
		return "", err
	}

	// Options and retention (scoped to appropriate command sections)
	opts := pgbackrestConfig.Options
	if opts == nil {
		opts = &apiv1.PgBackRestOptions{}
	}
	applyOptionDefaults(opts, cluster)
	configureOptions(opts, cfg)

	// On replicas, keep pg1 as the local standby and add pg2 as the remote
	// primary (via TLS). This enables backup-standby: pgbackrest copies files
	// locally from the replica while coordinating with the primary over TLS.
	if !isPrimary {
		// The section key is the stanza identity; the clusterName arg is the
		// live cluster used to reach the primary (pg2-host: <name>-rw), so it
		// must stay the actual Cluster name even when the stanza is overridden.
		configureReplicaStanza(cfg.Section(cluster.GetPgBackRestStanzaName()), cluster.Name, pgDataPath)
	}

	return renderConfig(cfg)
}

// GenerateConfigFromRepository builds a minimal pgbackrest.conf from a repository
// configuration. Used for restore where only the storage location is needed
// (no options or retention).
func GenerateConfigFromRepository(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	repo *apiv1.PgBackRestRepository,
	opts *apiv1.PgBackRestOptions,
	stanzaName string,
	pgDataPath string,
) (string, error) {
	cfg, err := generateBaseConfig(ctx, k8sClient, namespace, repo, stanzaName, pgDataPath)
	if err != nil {
		return "", err
	}

	if opts != nil {
		configureOptions(opts, cfg)
	}

	return renderConfig(cfg)
}

// generateBaseConfig builds the core pgbackrest INI config with repository
// location, paths, and stanza section.
func generateBaseConfig(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	repo *apiv1.PgBackRestRepository,
	stanzaName string,
	pgDataPath string,
) (*ini.File, error) {
	cfg := ini.Empty()
	global := cfg.Section("global")

	// Repository storage configuration. The CRD guarantees exactly one
	// backend is set; the checks are independent as a defensive measure.
	if repo.S3 != nil {
		if err := configureS3(ctx, k8sClient, namespace, repo.S3, global); err != nil {
			return nil, fmt.Errorf("configuring S3: %w", err)
		}
	}
	if repo.GCS != nil {
		if err := configureGCS(repo.GCS, global); err != nil {
			return nil, fmt.Errorf("configuring GCS: %w", err)
		}
	}

	if repo.Azure != nil {
		if err := configureAzure(repo.Azure, global); err != nil {
			return nil, fmt.Errorf("configuring Azure: %w", err)
		}
	}
	if repo.Cipher != nil {
		passphrase, err := resolveSecretKeyRef(ctx, k8sClient, namespace, &repo.Cipher.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("resolving repository cipher passphrase: %w", err)
		}
		global.Key("repo1-cipher-type").SetValue(repo.Cipher.Type)
		global.Key("repo1-cipher-pass").SetValue(passphrase)
	}

	// pgbackrest working directories — stored on the PGDATA PVC (outside the
	// pgdata/ subdirectory) so each cluster uses its own dedicated storage
	// instead of shared node scratch space.
	pgbackrestDir := workingDir(pgDataPath)
	global.Key("spool-path").SetValue(pgbackrestDir + "/spool")
	global.Key("log-path").SetValue(pgbackrestDir + "/log")
	global.Key("lock-path").SetValue(pgbackrestDir + "/lock")

	// TLS server config — every pod runs a pgbackrest TLS server for
	// backup-standby inter-pod communication. Uses existing CNPG certs.
	// The streaming_replica cert (CN=streaming_replica) is used by replicas
	// to connect to the primary's TLS server.
	global.Key("tls-server-ca-file").SetValue(postgres.ServerCACertificateLocation)
	global.Key("tls-server-cert-file").SetValue(postgres.ServerCertificateLocation)
	global.Key("tls-server-key-file").SetValue(postgres.ServerKeyLocation)
	global.Key("tls-server-address").SetValue("*")
	global.Key("tls-server-auth").SetValue(apiv1.StreamingReplicationUser + "=*")

	// Stanza section
	stanza := cfg.Section(stanzaName)
	stanza.Key("pg1-path").SetValue(pgDataPath)

	return cfg, nil
}

// renderConfig serializes an INI config to a string.
func renderConfig(cfg *ini.File) (string, error) {
	var buf bytes.Buffer
	if _, err := cfg.WriteTo(&buf); err != nil {
		return "", fmt.Errorf("rendering pgbackrest config: %w", err)
	}
	return buf.String(), nil
}

// workingDir returns the pgbackrest working directory on the PGDATA volume.
// Both the configuration values (spool-path, log-path, lock-path) and the
// directory creation in WriteConfigFile derive from it so they cannot drift
// apart.
func workingDir(pgDataPath string) string {
	return filepath.Dir(pgDataPath) + "/pgbackrest"
}

// ensureWorkingDirectories creates the pgbackrest working directories on the
// PGDATA volume. pgbackrest creates the spool and lock paths on demand, but
// never the log path — without it every command warns and file logging is
// silently disabled.
func ensureWorkingDirectories(pgDataPath string) error {
	for _, subdir := range []string{"spool", "log", "lock"} {
		path := filepath.Join(workingDir(pgDataPath), subdir)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("creating directory %s: %w", path, err)
		}
	}
	return nil
}

// WriteConfigFile writes the pgbackrest configuration to ConfigFilePath.
// It creates the config parent directory and the pgbackrest working
// directories on the PGDATA volume.
// Returns true if the file content changed, false if it was already up to date.
func WriteConfigFile(content, pgDataPath string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(ConfigFilePath), 0o700); err != nil {
		return false, fmt.Errorf("creating directory %s: %w", filepath.Dir(ConfigFilePath), err)
	}

	if err := ensureWorkingDirectories(pgDataPath); err != nil {
		return false, err
	}

	existing, err := os.ReadFile(ConfigFilePath)
	if err == nil && string(existing) == content {
		return false, nil
	}

	if err := os.WriteFile(ConfigFilePath, []byte(content), 0o600); err != nil {
		return false, fmt.Errorf("writing config file %s: %w", ConfigFilePath, err)
	}

	return true, nil
}

const (
	keyTypeAuto   = "auto"
	keyTypeShared = "shared"
)

// effectiveS3KeyType returns the explicitly selected pgBackRest provider. New
// resources default to auto. Existing resources with static credential
// references retain pgBackRest's shared-key behavior.
func effectiveS3KeyType(s3 *apiv1.PgBackRestS3) string {
	if s3.KeyType != "" {
		return s3.KeyType
	}

	if s3.AccessKeyID != nil && s3.SecretAccessKey != nil {
		return keyTypeShared
	}

	return keyTypeAuto
}

// configureS3 sets S3-specific keys in the [global] section.
func configureS3(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	s3 *apiv1.PgBackRestS3,
	section *ini.Section,
) error {
	section.Key("repo1-type").SetValue("s3")
	section.Key("repo1-s3-bucket").SetValue(s3.Bucket)
	section.Key("repo1-s3-region").SetValue(s3.Region)

	if s3.Endpoint != "" {
		section.Key("repo1-s3-endpoint").SetValue(s3.Endpoint)
		// Non-AWS endpoints (e.g. MinIO) typically need path-style URIs
		section.Key("repo1-s3-uri-style").SetValue("path")
	} else {
		// pgbackrest requires an explicit endpoint even for AWS
		section.Key("repo1-s3-endpoint").SetValue("s3." + s3.Region + ".amazonaws.com")
	}

	keyType := effectiveS3KeyType(s3)
	section.Key("repo1-s3-key-type").SetValue(keyType)

	if s3.AccessKeyID != nil {
		accessKey, err := resolveSecretKeyRef(ctx, k8sClient, namespace, s3.AccessKeyID)
		if err != nil {
			return fmt.Errorf("resolving S3 access key: %w", err)
		}
		section.Key("repo1-s3-key").SetValue(accessKey)
	}
	if s3.SecretAccessKey != nil {
		secretKey, err := resolveSecretKeyRef(ctx, k8sClient, namespace, s3.SecretAccessKey)
		if err != nil {
			return fmt.Errorf("resolving S3 secret key: %w", err)
		}
		section.Key("repo1-s3-key-secret").SetValue(secretKey)
	}

	return nil
}

// configureGCS sets GCS-specific keys in the [global] section.
func configureGCS(gcs *apiv1.PgBackRestGCS, section *ini.Section) error {
	section.Key("repo1-type").SetValue("gcs")
	section.Key("repo1-gcs-bucket").SetValue(gcs.Bucket)

	if gcs.Endpoint != "" {
		section.Key("repo1-gcs-endpoint").SetValue(gcs.Endpoint)
	}

	keyType := gcs.KeyType
	if keyType == "" {
		// pgbackrest defaults repo1-gcs-key-type to "service", so "auto"
		// (workload identity / ADC) must be set explicitly.
		keyType = keyTypeAuto
	}
	section.Key("repo1-gcs-key-type").SetValue(keyType)

	if keyType != keyTypeAuto {
		// The "service" and "token" key types need repo1-gcs-key to point at
		// a file on disk holding the service account JSON or bearer token.
		// Plumbing the KeyRef secret to a file on the pod is not wired up
		// yet, so only "auto" is supported for now.
		return fmt.Errorf("gcs key type %q is not supported yet, only \"auto\" is", keyType)
	}

	return nil
}

// configureAzure sets Azure-specific keys in the [global] section.
func configureAzure(azure *apiv1.PgBackRestAzure, section *ini.Section) error {
	section.Key("repo1-type").SetValue("azure")
	section.Key("repo1-azure-account").SetValue(azure.Account)
	section.Key("repo1-azure-container").SetValue(azure.Container)

	if azure.Endpoint != "" {
		section.Key("repo1-azure-endpoint").SetValue(azure.Endpoint)
	}

	keyType := azure.KeyType
	if keyType == "" {
		// pgbackrest defaults repo1-azure-key-type to "shared", so "auto"
		// (managed identity via instance metadata, pgbackrest >= 2.58) must
		// be set explicitly.
		keyType = keyTypeAuto
	}
	section.Key("repo1-azure-key-type").SetValue(keyType)

	if keyType != keyTypeAuto {
		// The "shared" and "sas" key types need repo1-azure-key holding the
		// account key or SAS token. Plumbing the KeyRef secret is not wired
		// up yet, so only "auto" is supported for now.
		return fmt.Errorf("azure key type %q is not supported yet, only \"auto\" is", keyType)
	}

	return nil
}

// applyOptionDefaults sets sensible defaults for options that the user hasn't
// explicitly configured.
//
// processMax is derived from the pod's CPU request (1 process per 1000m CPU,
// minimum 1). This uses request (not limit) because request is the guaranteed
// CPU under node contention:
//
//	| Instance     | Request | processMax |
//	|--------------|---------|------------|
//	| xata.micro   |   250m  |     1      |
//	| xata.small   |   500m  |     1      |
//	| xata.medium  |  1000m  |     1      |
//	| xata.large   |  2000m  |     2      |
//	| xata.xlarge  |  4000m  |     4      |
//	| xata.2xlarge |  8000m  |     8      |
//	| xata.4xlarge | 16000m  |    16      |
//	| xata.8xlarge | 32000m  |    32      |
//
// priority defaults to 19 (lowest nice value) so pgbackrest never competes
// with PostgreSQL for CPU.
func applyOptionDefaults(opts *apiv1.PgBackRestOptions, cluster *apiv1.Cluster) {
	if opts.Priority == nil {
		defaultPriority := 19
		opts.Priority = &defaultPriority
	}
	if opts.ProcessMax == nil {
		cpuRequest := cluster.Spec.Resources.Requests.Cpu()
		if cpuRequest != nil && !cpuRequest.IsZero() {
			processMax := int(cpuRequest.MilliValue() / 1000)
			if processMax < 1 {
				processMax = 1
			}
			opts.ProcessMax = &processMax
		}
	}
	if opts.RestoreProcessMax == nil {
		cpuRequest := cluster.Spec.Resources.Requests.Cpu()
		if cpuRequest != nil && !cpuRequest.IsZero() {
			// During restore PostgreSQL is not running, so shared_buffers and
			// other PG memory is free. We use 2x the backup process-max. Each
			// process uses ~230MB; the freed shared_buffers (~25% of RAM) more
			// than covers the extra memory at every instance size.
			restoreMax := int(cpuRequest.MilliValue()/1000) * 2
			if restoreMax < 1 {
				restoreMax = 1
			}
			opts.RestoreProcessMax = &restoreMax
		}
	}
	if opts.RepoPath == "" {
		// Default the repo path to the stanza name so that overriding only the
		// stanza still keeps the whole backup set (prefix + stanza) under one
		// stable location.
		opts.RepoPath = cluster.GetPgBackRestStanzaName()
	}
}

// configureOptions maps PgBackRestOptions fields to pgbackrest config sections.
// Most options go in [global]. Backup-only options (start-fast, backup-standby,
// bundle, block-incremental, retention) go in [global:backup] to prevent
// pgbackrest from rejecting them during other commands. Restore-only options
// (delta) go in [global:restore].
func configureOptions(opts *apiv1.PgBackRestOptions, cfg *ini.File) {
	configureGlobalOptions(opts, cfg.Section("global"))
	configureBackupOptions(opts, cfg.Section("global:backup"))
	configureRestoreOptions(opts, cfg.Section("global:restore"))
}

// configureGlobalOptions sets options in [global] that apply to all commands
// or are safely ignored by commands that don't use them.
func configureGlobalOptions(opts *apiv1.PgBackRestOptions, section *ini.Section) {
	if opts.RepoPath != "" {
		section.Key("repo1-path").SetValue("/" + opts.RepoPath)
	}
	if opts.CompressType != "" {
		section.Key("compress-type").SetValue(opts.CompressType)
	}
	if opts.CompressLevel != nil {
		section.Key("compress-level").SetValue(strconv.Itoa(*opts.CompressLevel))
	}
	if opts.ProcessMax != nil {
		section.Key("process-max").SetValue(strconv.Itoa(*opts.ProcessMax))
	}
	if opts.Priority != nil {
		section.Key("priority").SetValue(strconv.Itoa(*opts.Priority))
	}
	if opts.ArchiveAsync != nil && *opts.ArchiveAsync {
		section.Key("archive-async").SetValue("y")
		if opts.ArchivePushQueueMax != "" {
			section.Key("archive-push-queue-max").SetValue(opts.ArchivePushQueueMax)
		} else {
			section.Key("archive-push-queue-max").SetValue("2GiB")
		}
		if opts.ArchiveGetQueueMax != "" {
			section.Key("archive-get-queue-max").SetValue(opts.ArchiveGetQueueMax)
		} else {
			section.Key("archive-get-queue-max").SetValue("2GiB")
		}
	}
}

// configureBackupOptions sets options scoped to the backup command,
// including retention settings.
func configureBackupOptions(opts *apiv1.PgBackRestOptions, section *ini.Section) {
	if opts.StartFast != nil && *opts.StartFast {
		section.Key("start-fast").SetValue("y")
	}
	if opts.BackupStandby != nil && *opts.BackupStandby {
		section.Key("backup-standby").SetValue("y")
	}
	if opts.Bundle != nil && *opts.Bundle {
		section.Key("repo1-bundle").SetValue("y")
	}
	if opts.BlockIncremental != nil && *opts.BlockIncremental {
		section.Key("repo1-block").SetValue("y")
	}
	if ret := opts.Retention; ret != nil {
		if ret.Full > 0 {
			section.Key("repo1-retention-full").SetValue(strconv.Itoa(ret.Full))
		}
		if ret.FullType != "" {
			section.Key("repo1-retention-full-type").SetValue(ret.FullType)
		}
		if ret.Archive != nil {
			section.Key("repo1-retention-archive").SetValue(strconv.Itoa(*ret.Archive))
		}
	}
}

// configureRestoreOptions sets options scoped to the restore command.
func configureRestoreOptions(opts *apiv1.PgBackRestOptions, section *ini.Section) {
	if opts.Delta != nil && *opts.Delta {
		section.Key("delta").SetValue("y")
	}
	if opts.RestoreProcessMax != nil {
		section.Key("process-max").SetValue(strconv.Itoa(*opts.RestoreProcessMax))
	}
}

// resolveSecretKeyRef fetches a Kubernetes secret and extracts the value
// for the given key reference.
func resolveSecretKeyRef(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	selector *apiv1.SecretKeySelector,
) (string, error) {
	if selector == nil {
		return "", fmt.Errorf("secret key selector is nil")
	}

	var secret corev1.Secret
	err := k8sClient.Get(ctx, types.NamespacedName{
		Name:      selector.Name,
		Namespace: namespace,
	}, &secret)
	if err != nil {
		return "", fmt.Errorf("fetching secret %s/%s: %w", namespace, selector.Name, err)
	}

	value, ok := secret.Data[selector.Key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %s/%s", selector.Key, namespace, selector.Name)
	}

	return string(value), nil
}

// configureReplicaStanza sets up the stanza section on a replica for
// backup-standby mode. pg1 stays the local standby (pg1-path is already set
// by generateBaseConfig), where the bulk of the file copy happens; pg2 is the
// remote primary, reached via TLS for pg_backup_start/stop and the files that
// must come from the primary.
//
// The local instance must be pg1: pgbackrest's archive-get, archive-push and
// restore commands refuse to run when the default pg index (pg1) has a pg-host
// set ("command must be run on the PostgreSQL host", error 072). With pg1-host
// on replicas, restore_command could never fetch WAL from the repository. For
// backup the index order is irrelevant: pgbackrest connects to every configured
// pg and detects which one is the primary and which one is the standby.
func configureReplicaStanza(stanza *ini.Section, clusterName string, pgDataPath string) {
	// pg2 = remote primary (TLS connection for pg_backup_start/stop + remaining files)
	stanza.Key("pg2-path").SetValue(pgDataPath)
	stanza.Key("pg2-host").SetValue(clusterName + "-rw")
	stanza.Key("pg2-host-type").SetValue("tls")
	stanza.Key("pg2-host-ca-file").SetValue(postgres.ServerCACertificateLocation)
	stanza.Key("pg2-host-cert-file").SetValue(postgres.StreamingReplicaCertificateLocation)
	stanza.Key("pg2-host-key-file").SetValue(postgres.StreamingReplicaKeyLocation)
}
