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
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
