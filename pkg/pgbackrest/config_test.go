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
	"context"
	"testing"

	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
)

func TestApplyOptionDefaults_Priority(t *testing.T) {
	cluster := &apiv1.Cluster{
		Spec: apiv1.ClusterSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}

	opts := &apiv1.PgBackRestOptions{}
	applyOptionDefaults(opts, cluster)

	if opts.Priority == nil || *opts.Priority != 19 {
		t.Errorf("expected priority 19, got %v", opts.Priority)
	}
}

func TestApplyOptionDefaults_PriorityNotOverridden(t *testing.T) {
	cluster := &apiv1.Cluster{
		Spec: apiv1.ClusterSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}

	userPriority := 10
	opts := &apiv1.PgBackRestOptions{Priority: &userPriority}
	applyOptionDefaults(opts, cluster)

	if *opts.Priority != 10 {
		t.Errorf("expected user priority 10, got %v", *opts.Priority)
	}
}

func TestApplyOptionDefaults_ProcessMax(t *testing.T) {
	tests := []struct {
		name       string
		cpuRequest string
		expected   int
	}{
		{"micro 250m", "250m", 1},
		{"small 500m", "500m", 1},
		{"medium 1000m", "1000m", 1},
		{"large 2000m", "2000m", 2},
		{"xlarge 4000m", "4000m", 4},
		{"2xlarge 8000m", "8000m", 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &apiv1.Cluster{
				Spec: apiv1.ClusterSpec{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse(tt.cpuRequest),
						},
					},
				},
			}

			opts := &apiv1.PgBackRestOptions{}
			applyOptionDefaults(opts, cluster)

			if opts.ProcessMax == nil || *opts.ProcessMax != tt.expected {
				t.Errorf("expected processMax %d for %s, got %v", tt.expected, tt.cpuRequest, opts.ProcessMax)
			}
		})
	}
}

func TestApplyOptionDefaults_ProcessMaxNotOverridden(t *testing.T) {
	cluster := &apiv1.Cluster{
		Spec: apiv1.ClusterSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4000m"),
				},
			},
		},
	}

	userMax := 2
	opts := &apiv1.PgBackRestOptions{ProcessMax: &userMax}
	applyOptionDefaults(opts, cluster)

	if *opts.ProcessMax != 2 {
		t.Errorf("expected user processMax 2, got %v", *opts.ProcessMax)
	}
}

func TestApplyOptionDefaults_RepoPath(t *testing.T) {
	cluster := &apiv1.Cluster{
		Spec: apiv1.ClusterSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}
	cluster.Name = "my-cluster"

	opts := &apiv1.PgBackRestOptions{}
	applyOptionDefaults(opts, cluster)

	if opts.RepoPath != "my-cluster" {
		t.Errorf("expected repoPath my-cluster, got %s", opts.RepoPath)
	}
}

func TestApplyOptionDefaults_RepoPathNotOverridden(t *testing.T) {
	cluster := &apiv1.Cluster{
		Spec: apiv1.ClusterSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}
	cluster.Name = "my-cluster"

	opts := &apiv1.PgBackRestOptions{RepoPath: "custom-path"}
	applyOptionDefaults(opts, cluster)

	if opts.RepoPath != "custom-path" {
		t.Errorf("expected repoPath custom-path, got %s", opts.RepoPath)
	}
}

func TestConfigureGlobalOptions_RepoPath(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global")

	opts := &apiv1.PgBackRestOptions{RepoPath: "my-cluster"}
	configureGlobalOptions(opts, section)

	if v := section.Key("repo1-path").String(); v != "/my-cluster" {
		t.Errorf("expected repo1-path /my-cluster, got %s", v)
	}
}

func TestConfigureGlobalOptions_RepoPathEmpty(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global")

	opts := &apiv1.PgBackRestOptions{}
	configureGlobalOptions(opts, section)

	if section.HasKey("repo1-path") {
		t.Error("repo1-path should not be set when repoPath is empty")
	}
}

func TestConfigureGlobalOptions(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global")

	compressLevel := 3
	processMax := 2
	priority := 10
	opts := &apiv1.PgBackRestOptions{
		CompressType:  "lz4",
		CompressLevel: &compressLevel,
		ProcessMax:    &processMax,
		Priority:      &priority,
	}

	configureGlobalOptions(opts, section)

	if v := section.Key("compress-type").String(); v != "lz4" {
		t.Errorf("expected compress-type lz4, got %s", v)
	}
	if v := section.Key("compress-level").String(); v != "3" {
		t.Errorf("expected compress-level 3, got %s", v)
	}
	if v := section.Key("process-max").String(); v != "2" {
		t.Errorf("expected process-max 2, got %s", v)
	}
	if v := section.Key("priority").String(); v != "10" {
		t.Errorf("expected priority 10, got %s", v)
	}
}

func TestConfigureGlobalOptions_AsyncArchiving(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global")

	opts := &apiv1.PgBackRestOptions{
		ArchiveAsync: ptr.To(true),
	}

	configureGlobalOptions(opts, section)

	if v := section.Key("archive-async").String(); v != "y" {
		t.Errorf("expected archive-async y, got %s", v)
	}
	if v := section.Key("archive-push-queue-max").String(); v != "2GiB" {
		t.Errorf("expected default archive-push-queue-max 2GiB, got %s", v)
	}
	if v := section.Key("archive-get-queue-max").String(); v != "2GiB" {
		t.Errorf("expected default archive-get-queue-max 2GiB, got %s", v)
	}
}

func TestConfigureGlobalOptions_AsyncArchivingCustomQueues(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global")

	opts := &apiv1.PgBackRestOptions{
		ArchiveAsync:        ptr.To(true),
		ArchivePushQueueMax: "4GiB",
		ArchiveGetQueueMax:  "1GiB",
	}

	configureGlobalOptions(opts, section)

	if v := section.Key("archive-push-queue-max").String(); v != "4GiB" {
		t.Errorf("expected archive-push-queue-max 4GiB, got %s", v)
	}
	if v := section.Key("archive-get-queue-max").String(); v != "1GiB" {
		t.Errorf("expected archive-get-queue-max 1GiB, got %s", v)
	}
}

func TestConfigureGlobalOptions_NoAsyncNoQueues(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global")

	opts := &apiv1.PgBackRestOptions{}

	configureGlobalOptions(opts, section)

	if section.HasKey("archive-async") {
		t.Error("archive-async should not be set when archiveAsync is not enabled")
	}
	if section.HasKey("archive-push-queue-max") {
		t.Error("archive-push-queue-max should not be set when archiveAsync is not enabled")
	}
}

func TestConfigureBackupOptions(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global:backup")

	opts := &apiv1.PgBackRestOptions{
		StartFast:        ptr.To(true),
		BackupStandby:    ptr.To(true),
		Bundle:           ptr.To(true),
		BlockIncremental: ptr.To(true),
		Retention: &apiv1.PgBackRestRetention{
			Full:     7,
			FullType: "count",
		},
	}

	configureBackupOptions(opts, section)

	if v := section.Key("start-fast").String(); v != "y" {
		t.Errorf("expected start-fast y, got %s", v)
	}
	if v := section.Key("backup-standby").String(); v != "y" {
		t.Errorf("expected backup-standby y, got %s", v)
	}
	if v := section.Key("repo1-bundle").String(); v != "y" {
		t.Errorf("expected repo1-bundle y, got %s", v)
	}
	if v := section.Key("repo1-block").String(); v != "y" {
		t.Errorf("expected repo1-block y, got %s", v)
	}
	if v := section.Key("repo1-retention-full").String(); v != "7" {
		t.Errorf("expected repo1-retention-full 7, got %s", v)
	}
	if v := section.Key("repo1-retention-full-type").String(); v != "count" {
		t.Errorf("expected repo1-retention-full-type count, got %s", v)
	}
}

func TestConfigureBackupOptions_Empty(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global:backup")

	opts := &apiv1.PgBackRestOptions{}
	configureBackupOptions(opts, section)

	if section.HasKey("start-fast") {
		t.Error("start-fast should not be set when not configured")
	}
	if section.HasKey("repo1-retention-full") {
		t.Error("retention should not be set when not configured")
	}
}

func TestConfigureRestoreOptions(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global:restore")

	opts := &apiv1.PgBackRestOptions{
		Delta: ptr.To(true),
	}

	configureRestoreOptions(opts, section)

	if v := section.Key("delta").String(); v != "y" {
		t.Errorf("expected delta y, got %s", v)
	}
}

func TestConfigureRestoreOptions_Empty(t *testing.T) {
	cfg := ini.Empty()
	section := cfg.Section("global:restore")

	opts := &apiv1.PgBackRestOptions{}
	configureRestoreOptions(opts, section)

	if section.HasKey("delta") {
		t.Error("delta should not be set when not configured")
	}
}

func TestConfigureOptions_CommandScoping(t *testing.T) {
	cfg := ini.Empty()

	opts := &apiv1.PgBackRestOptions{
		CompressType:     "lz4",
		ProcessMax:       ptr.To(2),
		Priority:         ptr.To(19),
		ArchiveAsync:     ptr.To(true),
		StartFast:        ptr.To(true),
		BackupStandby:    ptr.To(true),
		Bundle:           ptr.To(true),
		BlockIncremental: ptr.To(true),
		Delta:            ptr.To(true),
	}

	configureOptions(opts, cfg)

	// Global section should have global options
	global := cfg.Section("global")
	if v := global.Key("compress-type").String(); v != "lz4" {
		t.Errorf("expected compress-type in global, got %s", v)
	}
	if v := global.Key("process-max").String(); v != "2" {
		t.Errorf("expected process-max in global, got %s", v)
	}
	if v := global.Key("archive-async").String(); v != "y" {
		t.Errorf("expected archive-async in global, got %s", v)
	}

	// Global section should NOT have backup-only options
	if global.HasKey("start-fast") {
		t.Error("start-fast should not be in global section")
	}
	if global.HasKey("delta") {
		t.Error("delta should not be in global section")
	}

	// Backup section should have backup-only options
	backup := cfg.Section("global:backup")
	if v := backup.Key("start-fast").String(); v != "y" {
		t.Errorf("expected start-fast in backup section, got %s", v)
	}
	if v := backup.Key("repo1-bundle").String(); v != "y" {
		t.Errorf("expected repo1-bundle in backup section, got %s", v)
	}

	// Backup section should NOT have global options
	if backup.HasKey("compress-type") {
		t.Error("compress-type should not be in backup section")
	}

	// Restore section should have restore-only options
	restore := cfg.Section("global:restore")
	if v := restore.Key("delta").String(); v != "y" {
		t.Errorf("expected delta in restore section, got %s", v)
	}

	// Restore section should NOT have other options
	if restore.HasKey("start-fast") {
		t.Error("start-fast should not be in restore section")
	}
}

func TestGenerateBaseConfig_TLSAndPaths(t *testing.T) {
	repo := &apiv1.PgBackRestRepository{
		S3: &apiv1.PgBackRestS3{
			Bucket:             "test-bucket",
			Region:             "us-east-1",
			InheritFromIAMRole: true,
		},
	}

	cfg, err := generateBaseConfig(
		context.Background(), nil, "default",
		repo, "test-cluster", "/pgdata",
	)
	if err != nil {
		t.Fatalf("generateBaseConfig failed: %v", err)
	}

	global := cfg.Section("global")
	stanza := cfg.Section("test-cluster")

	tests := []struct {
		name     string
		section  *ini.Section
		key      string
		expected string
	}{
		{"tls ca file", global, "tls-server-ca-file", "/controller/certificates/server-ca.crt"},
		{"tls cert file", global, "tls-server-cert-file", "/controller/certificates/server.crt"},
		{"tls key file", global, "tls-server-key-file", "/controller/certificates/server.key"},
		{"tls address", global, "tls-server-address", "*"},
		{"tls auth", global, "tls-server-auth", "streaming_replica=*"},
		{"spool path", global, "spool-path", SpoolPath},
		{"pg1 path", stanza, "pg1-path", "/pgdata"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v := tt.section.Key(tt.key).String(); v != tt.expected {
				t.Errorf("expected %s=%s, got %s", tt.key, tt.expected, v)
			}
		})
	}
}

func TestConfigureReplicaStanza(t *testing.T) {
	cfg := ini.Empty()
	stanza := cfg.Section("test-cluster")
	stanza.Key("pg1-path").SetValue("/pgdata")

	configureReplicaStanza(stanza, "test-cluster", "/pgdata")

	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{"pg1-host", "pg1-host", "test-cluster-rw"},
		{"pg1-host-type", "pg1-host-type", "tls"},
		{"pg1-host-ca-file", "pg1-host-ca-file", "/controller/certificates/server-ca.crt"},
		{"pg1-host-cert-file", "pg1-host-cert-file", "/controller/certificates/streaming_replica.crt"},
		{"pg1-host-key-file", "pg1-host-key-file", "/controller/certificates/streaming_replica.key"},
		{"pg2-path", "pg2-path", "/pgdata"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v := stanza.Key(tt.key).String(); v != tt.expected {
				t.Errorf("expected %s=%s, got %s", tt.key, tt.expected, v)
			}
		})
	}
}
