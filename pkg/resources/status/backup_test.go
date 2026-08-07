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

package status

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	schemeBuilder "github.com/xataio/xata-cnpg/internal/scheme"
	"github.com/xataio/xata-cnpg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FlagBackupAsFailed", func() {
	scheme := schemeBuilder.BuildWithAllKnownScheme()
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&apiv1.Cluster{}, &apiv1.Backup{}).
		Build()

	It("selects the new target primary right away", func(ctx SpecContext) {
		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-example",
				Namespace: "default",
			},
		}

		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{
					Name: cluster.Name,
				},
			},
			Status: apiv1.BackupStatus{
				Phase: apiv1.BackupPhaseRunning,
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		Expect(k8sClient.Create(ctx, backup)).To(Succeed())

		err := FlagBackupAsFailed(ctx, k8sClient, backup, cluster, errors.New("my sample error"))
		Expect(err).NotTo(HaveOccurred())

		// Backup status assertions
		Expect(backup.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseFailed))
		Expect(backup.Status.Error).To(BeEquivalentTo("my sample error"))

		// Cluster status assertions
		Expect(cluster.Status.LastFailedBackup).ToNot(BeEmpty()) //nolint:staticcheck
		for _, condition := range cluster.Status.Conditions {
			if condition.Type == string(apiv1.ConditionBackup) {
				Expect(condition.Status).To(BeEquivalentTo(metav1.ConditionFalse))
				Expect(condition.Reason).To(BeEquivalentTo(string(apiv1.ConditionReasonLastBackupFailed)))
				Expect(condition.Message).To(BeEquivalentTo("my sample error"))
			}
		}
	})

	It("flags the backup as cancelled when the cluster is hibernated", func(ctx SpecContext) {
		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-hibernated",
				Namespace: "default",
				Annotations: map[string]string{
					utils.HibernationAnnotationName: string(utils.HibernationAnnotationValueOn),
				},
			},
		}

		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{
					Name: cluster.Name,
				},
			},
			Status: apiv1.BackupStatus{
				Phase: apiv1.BackupPhaseRunning,
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		Expect(k8sClient.Create(ctx, backup)).To(Succeed())

		err := FlagBackupAsFailed(ctx, k8sClient, backup, cluster, errors.New("pod is gone"))
		Expect(err).NotTo(HaveOccurred())

		// Backup status assertions
		Expect(backup.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseCancelled))
		Expect(backup.Status.Error).To(ContainSubstring("cluster is hibernated"))
		Expect(backup.Status.Error).To(ContainSubstring("pod is gone"))
		Expect(backup.Status.StoppedAt).ToNot(BeNil())

		// The cluster status must not record a failed backup
		var livingCluster apiv1.Cluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &livingCluster)).To(Succeed())
		Expect(livingCluster.Status.LastFailedBackup).To(BeEmpty()) //nolint:staticcheck
		Expect(livingCluster.Status.Conditions).To(BeEmpty())
	})

	It("flags the backup as cancelled when the cluster is gone", func(ctx SpecContext) {
		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backup-orphan",
				Namespace: "default",
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{
					Name: "cluster-deleted",
				},
			},
			Status: apiv1.BackupStatus{
				Phase: apiv1.BackupPhaseRunning,
			},
		}
		Expect(k8sClient.Create(ctx, backup)).To(Succeed())

		err := FlagBackupAsFailed(ctx, k8sClient, backup, nil, errors.New("pod is gone"))
		Expect(err).NotTo(HaveOccurred())

		Expect(backup.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseCancelled))
		Expect(backup.Status.Error).To(ContainSubstring("cluster has been deleted"))
	})

	It("flags the backup as cancelled when the cluster is being deleted", func(ctx SpecContext) {
		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "cluster-deleting",
				Namespace:  "default",
				Finalizers: []string{"test.xata.io/hold"},
			},
		}

		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{
					Name: cluster.Name,
				},
			},
			Status: apiv1.BackupStatus{
				Phase: apiv1.BackupPhaseRunning,
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		Expect(k8sClient.Create(ctx, backup)).To(Succeed())
		// The finalizer keeps the cluster around with a deletion timestamp
		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())

		err := FlagBackupAsFailed(ctx, k8sClient, backup, cluster, errors.New("pod is gone"))
		Expect(err).NotTo(HaveOccurred())

		Expect(backup.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseCancelled))
		Expect(backup.Status.Error).To(ContainSubstring("cluster is being deleted"))
	})

	It("succeeds without patching the cluster when the backup is gone", func(ctx SpecContext) {
		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-backup-gone",
				Namespace: "default",
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		// The backup is never created: it has been deleted before flagging
		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backup-deleted",
				Namespace: "default",
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{
					Name: cluster.Name,
				},
			},
		}

		err := FlagBackupAsFailed(ctx, k8sClient, backup, cluster, errors.New("pod is gone"))
		Expect(err).NotTo(HaveOccurred())

		// The cluster must not record a failed backup for a vanished backup
		var livingCluster apiv1.Cluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &livingCluster)).To(Succeed())
		Expect(livingCluster.Status.LastFailedBackup).To(BeEmpty()) //nolint:staticcheck
	})

	It("flags the backup as cancelled when reading the cluster is forbidden", func(ctx SpecContext) {
		backup := &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backup-forbidden",
				Namespace: "default",
			},
			Spec: apiv1.BackupSpec{
				Cluster: apiv1.LocalObjectReference{
					Name: "cluster-forbidden",
				},
			},
			Status: apiv1.BackupStatus{
				Phase: apiv1.BackupPhaseRunning,
			},
		}

		forbiddenClient := fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&apiv1.Cluster{}, &apiv1.Backup{}).
			WithObjects(backup).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(
					ctx context.Context,
					cli client.WithWatch,
					key client.ObjectKey,
					obj client.Object,
					opts ...client.GetOption,
				) error {
					if _, ok := obj.(*apiv1.Cluster); ok {
						return apierrors.NewForbidden(
							schema.GroupResource{Group: "postgresql.cnpg.io", Resource: "clusters"},
							key.Name,
							errors.New("RBAC is being torn down"),
						)
					}
					return cli.Get(ctx, key, obj, opts...)
				},
			}).
			Build()

		err := FlagBackupAsFailed(ctx, forbiddenClient, backup, nil, errors.New("pod is gone"))
		Expect(err).NotTo(HaveOccurred())

		Expect(backup.Status.Phase).To(BeEquivalentTo(apiv1.BackupPhaseCancelled))
		Expect(backup.Status.Error).To(ContainSubstring("cluster has been deleted"))
	})
})
