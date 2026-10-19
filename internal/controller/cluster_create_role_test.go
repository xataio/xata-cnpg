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

package controller

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	schemeBuilder "github.com/xataio/xata-cnpg/internal/scheme"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("createOrPatchRole", func() {
	const namespace = "xata-clusters"

	var (
		ctx     context.Context
		cluster *apiv1.Cluster
	)

	newBackup := func(name, clusterName string, phase apiv1.BackupPhase) *apiv1.Backup {
		return &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			Spec:       apiv1.BackupSpec{Cluster: apiv1.LocalObjectReference{Name: clusterName}},
			Status:     apiv1.BackupStatus{Phase: phase},
		}
	}

	newClient := func(objects ...client.Object) client.Client {
		return fake.NewClientBuilder().
			WithScheme(schemeBuilder.BuildWithAllKnownScheme()).
			WithObjects(objects...).
			WithStatusSubresource(&apiv1.Backup{}).
			WithIndex(&apiv1.Backup{}, clusterNameField, func(obj client.Object) []string {
				return []string{obj.(*apiv1.Backup).Spec.Cluster.Name}
			}).
			Build()
	}

	backupNames := func(cli client.Client) map[string][]string {
		var role rbacv1.Role
		Expect(cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cluster.Name}, &role)).To(Succeed())
		names := map[string][]string{}
		for _, rule := range role.Rules {
			for _, resource := range rule.Resources {
				if resource == "backups" || resource == "backups/status" {
					names[resource] = append(names[resource], rule.ResourceNames...)
				}
			}
		}
		return names
	}

	BeforeEach(func() {
		ctx = context.Background()
		cluster = &apiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "cluster-a"}}
	})

	It("names only the active backups of the cluster", func() {
		cli := newClient(
			cluster,
			newBackup("a-running", "cluster-a", apiv1.BackupPhaseRunning),
			newBackup("a-new", "cluster-a", ""),
			newBackup("a-completed", "cluster-a", apiv1.BackupPhaseCompleted),
			newBackup("a-failed", "cluster-a", apiv1.BackupPhaseFailed),
			newBackup("b-running", "cluster-b", apiv1.BackupPhaseRunning),
		)

		Expect(createOrPatchRole(ctx, cli, record.NewFakeRecorder(10), cluster)).To(BeTrue())

		Expect(backupNames(cli)).To(Equal(map[string][]string{
			"backups":        {"a-new", "a-running"},
			"backups/status": {"a-new", "a-running"},
		}))
	})

	It("grants no access to backups when none is active", func() {
		cli := newClient(
			cluster,
			newBackup("a-completed", "cluster-a", apiv1.BackupPhaseCompleted),
			newBackup("b-running", "cluster-b", apiv1.BackupPhaseRunning),
		)

		Expect(createOrPatchRole(ctx, cli, record.NewFakeRecorder(10), cluster)).To(BeTrue())

		Expect(backupNames(cli)).To(BeEmpty())
	})

	It("drops a backup from the role when it is done", func() {
		backup := newBackup("a-running", "cluster-a", apiv1.BackupPhaseRunning)
		cli := newClient(cluster, backup)
		Expect(createOrPatchRole(ctx, cli, record.NewFakeRecorder(10), cluster)).To(BeTrue())
		Expect(backupNames(cli)).To(HaveKey("backups"))

		backup.Status.Phase = apiv1.BackupPhaseCompleted
		Expect(cli.Status().Update(ctx, backup)).To(Succeed())
		Expect(createOrPatchRole(ctx, cli, record.NewFakeRecorder(10), cluster)).To(BeTrue())

		Expect(backupNames(cli)).To(BeEmpty())
		Expect(createOrPatchRole(ctx, cli, record.NewFakeRecorder(10), cluster)).To(BeFalse())
	})

	It("waits before a backup when the role changes", func() {
		cluster.Spec.Backup = &apiv1.BackupConfiguration{
			PgBackRest: &apiv1.PgBackRestConfiguration{
				Repository: &apiv1.PgBackRestRepository{
					S3: &apiv1.PgBackRestS3{Bucket: "test-bucket", Region: "us-east-1"},
				},
			},
		}
		backup := newBackup("a-new", "cluster-a", "")
		backup.Spec.Method = apiv1.BackupMethodPgBackRest
		cli := newClient(cluster, backup)
		reconciler := &BackupReconciler{Client: cli, Recorder: record.NewFakeRecorder(10)}

		res, err := reconciler.prepareBackup(ctx, *backup, *cluster)
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(Equal(&ctrl.Result{RequeueAfter: instanceRoleSyncDelay}))
		Expect(backupNames(cli)).To(HaveKeyWithValue("backups", []string{"a-new"}))

		res, err = reconciler.prepareBackup(ctx, *backup, *cluster)
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(BeNil())
	})
})
