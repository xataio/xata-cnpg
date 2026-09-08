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
	"errors"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/internal/cnpi/plugin/repository"
	"github.com/xataio/xata-cnpg/pkg/management/postgres"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakePgBackRestTLSServer struct {
	running     bool
	startCalls  int
	reloadCalls int
	startErr    error
	reloadErr   error
}

type fakeCertificateRefresher struct {
	changed bool
	err     error
}

func (r *fakeCertificateRefresher) RefreshSecrets(context.Context, *apiv1.Cluster) (bool, error) {
	return r.changed, r.err
}

func (s *fakePgBackRestTLSServer) Start(context.Context) error {
	s.startCalls++
	if s.startErr == nil {
		s.running = true
	}
	return s.startErr
}

func (s *fakePgBackRestTLSServer) Reload() error {
	s.reloadCalls++
	return s.reloadErr
}

func (s *fakePgBackRestTLSServer) IsRunning() bool {
	return s.running
}

var _ = Describe("pgbackrest TLS server configuration", func() {
	It("reloads a running server when the configuration changes", func() {
		server := &fakePgBackRestTLSServer{running: true}
		reconciler := &InstanceReconciler{pgBackRestTLSServer: server}

		err := reconciler.reconcilePgBackRestTLSServer(context.Background(), true)

		Expect(err).ToNot(HaveOccurred())
		Expect(server.startCalls).To(Equal(0))
		Expect(server.reloadCalls).To(Equal(1))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeFalse())
	})

	It("does not reload a running server when the configuration is unchanged", func() {
		server := &fakePgBackRestTLSServer{running: true}
		reconciler := &InstanceReconciler{pgBackRestTLSServer: server}

		err := reconciler.reconcilePgBackRestTLSServer(context.Background(), false)

		Expect(err).ToNot(HaveOccurred())
		Expect(server.startCalls).To(Equal(0))
		Expect(server.reloadCalls).To(Equal(0))
	})

	It("starts a stopped server with the current configuration", func() {
		server := &fakePgBackRestTLSServer{}
		reconciler := &InstanceReconciler{pgBackRestTLSServer: server}

		err := reconciler.reconcilePgBackRestTLSServer(context.Background(), true)

		Expect(err).ToNot(HaveOccurred())
		Expect(server.startCalls).To(Equal(1))
		Expect(server.reloadCalls).To(Equal(0))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeFalse())
	})

	It("retries a failed reload after the file stops reporting a change", func() {
		reloadErr := errors.New("reload failed")
		server := &fakePgBackRestTLSServer{running: true, reloadErr: reloadErr}
		reconciler := &InstanceReconciler{pgBackRestTLSServer: server}

		err := reconciler.reconcilePgBackRestTLSServer(context.Background(), true)
		Expect(err).To(MatchError(ContainSubstring(reloadErr.Error())))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeTrue())

		server.reloadErr = nil
		err = reconciler.reconcilePgBackRestTLSServer(context.Background(), false)

		Expect(err).ToNot(HaveOccurred())
		Expect(server.reloadCalls).To(Equal(2))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeFalse())
	})

	It("retries a failed start", func() {
		startErr := errors.New("start failed")
		server := &fakePgBackRestTLSServer{startErr: startErr}
		reconciler := &InstanceReconciler{pgBackRestTLSServer: server}

		err := reconciler.reconcilePgBackRestTLSServer(context.Background(), true)
		Expect(err).To(MatchError(ContainSubstring(startErr.Error())))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeTrue())

		server.startErr = nil
		err = reconciler.reconcilePgBackRestTLSServer(context.Background(), false)

		Expect(err).ToNot(HaveOccurred())
		Expect(server.startCalls).To(Equal(2))
		Expect(server.reloadCalls).To(Equal(0))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeFalse())
	})

	It("retains a reload after a partial secret refresh fails", func(ctx SpecContext) {
		cluster := &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: apiv1.ClusterSpec{
				Backup: &apiv1.BackupConfiguration{
					PgBackRest: &apiv1.PgBackRestConfiguration{Repository: &apiv1.PgBackRestRepository{}},
				},
			},
		}
		scheme := runtime.NewScheme()
		Expect(apiv1.AddToScheme(scheme)).To(Succeed())
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
		instance := postgres.NewInstance().WithClusterName(cluster.Name).WithNamespace(cluster.Namespace)
		instance.SetWaitingForPGData(true)
		reconciler := NewInstanceReconciler(instance, cli, nil, repository.New())
		refreshErr := errors.New("reading a later secret failed")
		refresher := &fakeCertificateRefresher{changed: true, err: refreshErr}
		reconciler.certificateReconciler = refresher
		server := &fakePgBackRestTLSServer{running: true}
		reconciler.pgBackRestTLSServer = server

		result, err := reconciler.Reconcile(ctx, reconcile.Request{})
		Expect(err).To(MatchError(ContainSubstring(refreshErr.Error())))
		Expect(result.IsZero()).To(BeTrue())
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeTrue())
		Expect(server.reloadCalls).To(Equal(0))

		// The next refresh reports unchanged files. Waiting for PGDATA must
		// preserve both the pending reload and the existing PGDATA retry.
		refresher.changed, refresher.err = false, nil
		result, err = reconciler.Reconcile(ctx, reconcile.Request{})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeTrue())

		Expect(reconciler.reconcilePgBackRestTLSServer(ctx, false)).To(Succeed())
		Expect(server.reloadCalls).To(Equal(1))
		Expect(reconciler.pgBackRestReloadPending.Load()).To(BeFalse())
	})
})
