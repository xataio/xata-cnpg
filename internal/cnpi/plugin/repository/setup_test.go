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

package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/puddle/v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Set Plugin Protocol", func() {
	var repository *data

	BeforeEach(func() {
		repository = &data{}
	})

	It("creates connection pool for new plugin", func() {
		err := repository.setPluginProtocol("plugin1", newUnitTestProtocol("test"), pluginSetupOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.pluginConnectionPool).To(HaveKey("plugin1"))
	})

	It("fails when adding same plugin name without forceRegistration", func() {
		err := repository.setPluginProtocol("plugin1", newUnitTestProtocol("/tmp/socket"), pluginSetupOptions{})
		Expect(err).NotTo(HaveOccurred())

		err = repository.setPluginProtocol("plugin1", newUnitTestProtocol("/tmp/socket2"), pluginSetupOptions{})
		Expect(err).To(BeEquivalentTo(&ErrPluginAlreadyRegistered{Name: "plugin1"}))
	})

	It("overwrites existing plugin when forceRegistration is true", func() {
		first := newUnitTestProtocol("/tmp/socket")
		err := repository.setPluginProtocol("plugin1", first, pluginSetupOptions{})
		Expect(err).NotTo(HaveOccurred())
		pool1 := repository.pluginConnectionPool["plugin1"]

		ctx1, cancel := context.WithCancel(context.Background())
		conn1, err := pool1.Acquire(ctx1)
		Expect(err).NotTo(HaveOccurred())
		Expect(conn1).NotTo(BeNil())
		cancel()
		conn1.Release()

		second := newUnitTestProtocol("/tmp/socket2")
		err = repository.setPluginProtocol("plugin1", second, pluginSetupOptions{forceRegistration: true})
		Expect(err).NotTo(HaveOccurred())
		pool2 := repository.pluginConnectionPool["plugin1"]

		ctx2, cancel := context.WithCancel(context.Background())
		conn2, err := pool2.Acquire(ctx2)
		Expect(err).NotTo(HaveOccurred())
		Expect(conn2).NotTo(BeNil())
		cancel()
		conn2.Release()

		Expect(pool1).NotTo(Equal(pool2))
		Expect(first.mockHandlers).To(HaveLen(1))
		Expect(first.mockHandlers[0].closed).To(BeTrue())
		Expect(second.mockHandlers).To(HaveLen(1))
		Expect(second.mockHandlers[0].closed).To(BeFalse())
	})
})

var _ = Describe("GetConnection", func() {
	var repository *data

	BeforeEach(func() {
		repository = &data{}
	})

	It("returns ErrUnknownPlugin for a plugin that was never registered", func(ctx SpecContext) {
		_, err := repository.GetConnection(ctx, "no-such-plugin")
		var errUnknownPlugin *ErrUnknownPlugin
		Expect(errors.As(err, &errUnknownPlugin)).To(BeTrue())
		Expect(errUnknownPlugin.Name).To(Equal("no-such-plugin"))
	})

	It("returns a working connection for a registered plugin", func(ctx SpecContext) {
		err := repository.setPluginProtocol("plugin1", newUnitTestProtocol("test"), pluginSetupOptions{})
		Expect(err).NotTo(HaveOccurred())

		conn, err := repository.GetConnection(ctx, "plugin1")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Name()).To(Equal("testing-service"))
		Expect(conn.Close()).To(Succeed())
	})

	It("fails with the closed pool error when the registered pool has been closed", func(ctx SpecContext) {
		err := repository.setPluginProtocol("plugin1", newUnitTestProtocol("test"), pluginSetupOptions{})
		Expect(err).NotTo(HaveOccurred())
		repository.pluginConnectionPool["plugin1"].Close()

		_, err = repository.GetConnection(ctx, "plugin1")
		Expect(errors.Is(err, puddle.ErrClosedPool)).To(BeTrue())
	})

	It("recovers when the plugin is re-registered while connections are being acquired", func(ctx SpecContext) {
		err := repository.setPluginProtocol("plugin1", newUnitTestProtocol("test"), pluginSetupOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Exercise concurrent GetConnection and forced re-registration:
		// the acquisitions must never observe a missing plugin, and the
		// map accesses must be race-free (this test is meaningful under
		// the -race detector).
		var wg sync.WaitGroup
		errored := make(chan error, 16)
		for range 8 {
			wg.Go(func() {
				defer GinkgoRecover()
				conn, err := repository.GetConnection(ctx, "plugin1")
				if err != nil {
					errored <- err
					return
				}
				errored <- conn.Close()
			})
		}
		for range 4 {
			wg.Go(func() {
				defer GinkgoRecover()
				err := repository.setPluginProtocol(
					"plugin1", newUnitTestProtocol("test"), pluginSetupOptions{forceRegistration: true})
				Expect(err).NotTo(HaveOccurred())
			})
		}
		wg.Wait()
		close(errored)

		// Connections may transiently fail while a pool is being replaced
		// (that is the retried "closed pool" window), but no goroutine may
		// see the plugin as unknown, since it stays registered throughout.
		for err := range errored {
			if err != nil {
				var errUnknownPlugin *ErrUnknownPlugin
				Expect(errors.As(err, &errUnknownPlugin)).To(BeFalse())
			}
		}

		conn, err := repository.GetConnection(ctx, "plugin1")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Close()).To(Succeed())
	}, NodeTimeout(time.Minute))
})
