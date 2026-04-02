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
)

// GenerateConfig builds a pgbackrest.conf INI configuration from the cluster
// spec. It resolves S3 credentials from Kubernetes secrets.
func GenerateConfig(
	ctx context.Context,
	k8sClient client.Client,
	cluster *apiv1.Cluster,
	pgDataPath string,
) (string, error) {
	pgbackrestConfig := cluster.Spec.Backup.PgBackRest

	cfg, err := generateBaseConfig(
		ctx, k8sClient, cluster.Namespace,
		pgbackrestConfig.Repository, cluster.Name, pgDataPath,
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

	// S3 configuration
	if repo.S3 != nil {
		if err := configureS3(ctx, k8sClient, namespace, repo.S3, global); err != nil {
			return nil, fmt.Errorf("configuring S3: %w", err)
		}
	}

	// Repository path
	global.Key("repo1-path").SetValue("/" + stanzaName)

	// Spool path (used when archive-async is enabled)
	global.Key("spool-path").SetValue(SpoolPath)

	// Log and temp paths — container filesystem is read-only,
	// redirect to the writable scratch-data volume.
	global.Key("log-path").SetValue("/controller/pgbackrest/log")
	global.Key("lock-path").SetValue("/controller/pgbackrest/lock")

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

// WriteConfigFile writes the pgbackrest configuration to ConfigFilePath.
// It creates the parent directory if it doesn't exist.
// Returns true if the file content changed, false if it was already up to date.
func WriteConfigFile(content string) (bool, error) {
	dir := filepath.Dir(ConfigFilePath)
	for _, subdir := range []string{"", "log", "lock"} {
		if err := os.MkdirAll(filepath.Join(dir, subdir), 0o700); err != nil {
			return false, fmt.Errorf("creating directory %s: %w", filepath.Join(dir, subdir), err)
		}
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

	if s3.InheritFromIAMRole {
		section.Key("repo1-s3-key-type").SetValue("auto")
	} else {
		accessKey, err := resolveSecretKeyRef(ctx, k8sClient, namespace, s3.AccessKeyID)
		if err != nil {
			return fmt.Errorf("resolving S3 access key: %w", err)
		}
		secretKey, err := resolveSecretKeyRef(ctx, k8sClient, namespace, s3.SecretAccessKey)
		if err != nil {
			return fmt.Errorf("resolving S3 secret key: %w", err)
		}
		section.Key("repo1-s3-key").SetValue(accessKey)
		section.Key("repo1-s3-key-secret").SetValue(secretKey)
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
