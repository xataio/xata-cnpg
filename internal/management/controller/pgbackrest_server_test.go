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
})
