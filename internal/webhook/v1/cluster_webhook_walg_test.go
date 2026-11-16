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

package v1

import (
	"k8s.io/apimachinery/pkg/util/validation/field"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("wal-g external cluster validation", func() {
	var v *ClusterCustomValidator
	path := field.NewPath("spec", "externalClusters").Index(0)

	BeforeEach(func() {
		v = &ClusterCustomValidator{}
	})

	walG := func() *apiv1.WalGExternalCluster {
		return &apiv1.WalGExternalCluster{
			S3: apiv1.WalGS3{Prefix: "s3://pg-ubid", Region: "us-west-2"},
		}
	}

	It("accepts an external cluster that only has a wal-g archive", func() {
		ec := &apiv1.ExternalCluster{Name: "ubicloud", WalG: walG()}
		Expect(v.validateExternalCluster(ec, path)).To(BeEmpty())
	})

	It("accepts wal-g as a valid recovery source", func() {
		cluster := &apiv1.Cluster{Spec: apiv1.ClusterSpec{
			Bootstrap:        &apiv1.BootstrapConfiguration{Recovery: &apiv1.BootstrapRecovery{Source: "ubicloud"}},
			ExternalClusters: []apiv1.ExternalCluster{{Name: "ubicloud", WalG: walG()}},
		}}
		Expect(v.validateBootstrapRecoverySource(cluster)).To(BeEmpty())
	})

	It("rejects wal-g combined with a barman object store", func() {
		ec := &apiv1.ExternalCluster{
			Name:              "ubicloud",
			WalG:              walG(),
			BarmanObjectStore: &apiv1.BarmanObjectStoreConfiguration{},
		}
		errs := v.validateExternalCluster(ec, path)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.externalClusters[0].walG"))
	})

	It("rejects wal-g combined with pgBackRest", func() {
		ec := &apiv1.ExternalCluster{
			Name:       "ubicloud",
			WalG:       walG(),
			PgBackRest: &apiv1.PgBackRestExternalCluster{},
		}
		Expect(v.validateExternalCluster(ec, path)).To(HaveLen(1))
	})

	It("rejects a lone static credential", func() {
		ec := &apiv1.ExternalCluster{Name: "ubicloud", WalG: walG()}
		ec.WalG.S3.AccessKeyID = &apiv1.SecretKeySelector{Key: "id"}
		errs := v.validateExternalCluster(ec, path)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.externalClusters[0].walG.s3"))
	})

	It("accepts both static credentials together", func() {
		ec := &apiv1.ExternalCluster{Name: "ubicloud", WalG: walG()}
		ec.WalG.S3.AccessKeyID = &apiv1.SecretKeySelector{Key: "id"}
		ec.WalG.S3.SecretAccessKey = &apiv1.SecretKeySelector{Key: "key"}
		Expect(v.validateExternalCluster(ec, path)).To(BeEmpty())
	})
})
