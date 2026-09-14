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

package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	schemeBuilder "github.com/xataio/xata-cnpg/internal/scheme"
	"github.com/xataio/xata-cnpg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("failBackup", func() {
	const (
		namespace   = "default"
		clusterName = "cluster-example"
		podName     = "cluster-example-2"
	)

	var (
		k8sClient client.Client
		command   *PgBackRestBackupCommand
		cause     error
	)

	// newCommand builds a backup command whose instance is a standby or a
	// primary, according to whether standby.signal exists in its PGDATA.
	newCommand := func(ctx context.Context, isStandby bool, annotations map[string]string) {
		pgData := GinkgoT().TempDir()
		if isStandby {
			Expect(os.WriteFile(filepath.Join(pgData, "standby.signal"), nil, 0o600)).To(Succeed())
		}

		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: namespace},
		}
		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "backup-example",
				Namespace:   namespace,
				Annotations: annotations,
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{Name: clusterName},
				Method:  apiv1.BackupMethodPgBackRest,
			},
			Status: apiv1.BackupStatus{
				Phase:      apiv1.BackupPhaseRunning,
				InstanceID: &apiv1.InstanceID{PodName: podName},
			},
		}

		k8sClient = fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			WithStatusSubresource(&apiv1.Cluster{}, &apiv1.Backup{}).
			WithObjects(cluster, backup).
			Build()

		command = &PgBackRestBackupCommand{
			Cluster:  cluster,
			Backup:   backup,
			Client:   k8sClient,
			Recorder: record.NewFakeRecorder(10),
			Instance: (&Instance{PgData: pgData}).WithPodName(podName),
			Log:      log.FromContext(ctx),
		}
	}

	storedBackup := func(ctx context.Context) *apiv1.Backup {
		var stored apiv1.Backup
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: namespace, Name: "backup-example",
		}, &stored)).To(Succeed())
		return &stored
	}

	BeforeEach(func() {
		cause = errors.New("pgbackrest backup failed (exit code 56)")
	})

	It("hands the backup back for a retry on the primary when running on a standby",
		func(ctx SpecContext) {
			newCommand(ctx, true, nil)

			command.failBackup(ctx, cause)

			stored := storedBackup(ctx)
			Expect(stored.Annotations).To(HaveKey(utils.PgBackRestPrimaryFallback))
			Expect(stored.Status.Phase).To(BeEmpty())
			Expect(stored.Status.InstanceID).To(BeNil())
			// The error text is kept so the reason for the retry is visible.
			Expect(stored.Status.Error).To(ContainSubstring("exit code 56"))
		})

	It("does not stamp the cluster while a retry is still possible", func(ctx SpecContext) {
		newCommand(ctx, true, nil)

		command.failBackup(ctx, cause)

		var stored apiv1.Cluster
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: namespace, Name: clusterName,
		}, &stored)).To(Succeed())
		Expect(stored.Status.LastFailedBackup).To(BeEmpty()) //nolint:staticcheck
	})

	It("fails the backup when running on the primary", func(ctx SpecContext) {
		newCommand(ctx, false, nil)

		command.failBackup(ctx, cause)

		stored := storedBackup(ctx)
		Expect(stored.Annotations).ToNot(HaveKey(utils.PgBackRestPrimaryFallback))
		Expect(stored.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseFailed))
	})

	It("fails the backup when the standby retry has already happened", func(ctx SpecContext) {
		newCommand(ctx, true, map[string]string{utils.PgBackRestPrimaryFallback: "true"})

		command.failBackup(ctx, cause)

		stored := storedBackup(ctx)
		Expect(stored.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseFailed))
	})
})

var _ = Describe("waitForAppliedConfig", func() {
	var (
		instance         *Instance
		command          *PgBackRestBackupCommand
		origWaitTimeout  time.Duration
		origWaitInterval time.Duration
	)

	newCommand := func(generation int64) *PgBackRestBackupCommand {
		return &PgBackRestBackupCommand{
			Cluster: &apiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Generation: generation},
			},
			Instance: instance,
			Log:      log.FromContext(context.Background()),
		}
	}

	BeforeEach(func() {
		origWaitTimeout = appliedConfigWaitTimeout
		origWaitInterval = appliedConfigWaitInterval
		appliedConfigWaitTimeout = 200 * time.Millisecond
		appliedConfigWaitInterval = 10 * time.Millisecond

		instance = &Instance{}
	})

	AfterEach(func() {
		appliedConfigWaitTimeout = origWaitTimeout
		appliedConfigWaitInterval = origWaitInterval
	})

	It("returns immediately when the generation is already applied", func() {
		instance.PgBackRestAppliedGeneration.Store(3)
		command = newCommand(3)
		Expect(command.waitForAppliedConfig(context.Background())).To(Succeed())
	})

	It("returns immediately when a newer generation is applied", func() {
		instance.PgBackRestAppliedGeneration.Store(5)
		command = newCommand(3)
		Expect(command.waitForAppliedConfig(context.Background())).To(Succeed())
	})

	It("succeeds once the reconciler applies the generation", func() {
		command = newCommand(4)
		go func() {
			time.Sleep(50 * time.Millisecond)
			instance.PgBackRestAppliedGeneration.Store(4)
		}()
		Expect(command.waitForAppliedConfig(context.Background())).To(Succeed())
	})

	It("fails with an explicit error when the generation is never applied", func() {
		instance.PgBackRestAppliedGeneration.Store(2)
		command = newCommand(4)
		err := command.waitForAppliedConfig(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("generation 4 not applied"))
		Expect(err.Error()).To(ContainSubstring("last applied generation 2"))
	})

	It("stops waiting when the context is cancelled", func() {
		command = newCommand(4)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()
		err := command.waitForAppliedConfig(ctx)
		Expect(err).To(MatchError(context.Canceled))
	})
})
