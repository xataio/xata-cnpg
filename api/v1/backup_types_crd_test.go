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
	"os"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests pin the generated Cluster CRD schema for the pgbackrest
// repository. The validation itself (exactly-one-of, credential rules) is
// enforced by the API server through CEL rules, which have no in-repo
// evaluation harness, so we assert the generated schema instead: required
// fields, enums, defaults, and the exact rule expressions.
var _ = Describe("PgBackRest repository CRD schema", func() {
	loadRepositorySchema := func() apiextensionsv1.JSONSchemaProps {
		data, err := os.ReadFile("../../config/crd/bases/postgresql.cnpg.io_clusters.yaml")
		Expect(err).ToNot(HaveOccurred())

		var crd apiextensionsv1.CustomResourceDefinition
		Expect(yaml.Unmarshal(data, &crd)).To(Succeed())
		Expect(crd.Spec.Versions).To(HaveLen(1))

		repository := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.
			Properties["spec"].
			Properties["backup"].
			Properties["pgBackRest"].
			Properties["repository"]
		return repository
	}

	It("requires exactly one of s3, gcs, or azure", func() {
		repository := loadRepositorySchema()

		Expect(repository.XValidations).To(HaveLen(1))
		Expect(repository.XValidations[0].Rule).To(Equal(
			"(has(self.s3) ? 1 : 0) + (has(self.gcs) ? 1 : 0) + (has(self.azure) ? 1 : 0) == 1"))
		Expect(repository.Properties).To(HaveKey("s3"))
		Expect(repository.Properties).To(HaveKey("gcs"))
		Expect(repository.Properties).To(HaveKey("azure"))
	})

	It("describes the gcs repository", func() {
		gcs := loadRepositorySchema().Properties["gcs"]

		Expect(gcs.Required).To(ConsistOf("bucket"))
		Expect(gcs.Properties).To(HaveKey("bucket"))
		Expect(gcs.Properties).To(HaveKey("endpoint"))
		Expect(gcs.Properties).To(HaveKey("keyRef"))

		keyType := gcs.Properties["keyType"]
		Expect(keyType.Enum).To(HaveLen(3))
		Expect(keyType.Default.Raw).To(BeEquivalentTo(`"auto"`))

		// keyRef is what carries credentials for the non-auto key types, so
		// the schema must reject auto+keyRef and service/token without keyRef.
		Expect(gcs.XValidations).To(HaveLen(1))
		Expect(gcs.XValidations[0].Rule).To(Equal(
			"(has(self.keyType) && (self.keyType == 'service' || self.keyType == 'token')) == has(self.keyRef)"))
	})

	It("describes the azure repository", func() {
		azure := loadRepositorySchema().Properties["azure"]

		Expect(azure.Required).To(ConsistOf("account", "container"))
		Expect(azure.Properties).To(HaveKey("account"))
		Expect(azure.Properties).To(HaveKey("container"))
		Expect(azure.Properties).To(HaveKey("endpoint"))
		Expect(azure.Properties).To(HaveKey("keyRef"))

		keyType := azure.Properties["keyType"]
		Expect(keyType.Enum).To(HaveLen(3))
		Expect(keyType.Default.Raw).To(BeEquivalentTo(`"auto"`))

		// keyRef is what carries credentials for the non-auto key types, so
		// the schema must reject auto+keyRef and shared/sas without keyRef.
		Expect(azure.XValidations).To(HaveLen(1))
		Expect(azure.XValidations[0].Rule).To(Equal(
			"(has(self.keyType) && (self.keyType == 'shared' || self.keyType == 'sas')) == has(self.keyRef)"))
	})

	It("describes the s3 credential providers", func() {
		s3 := loadRepositorySchema().Properties["s3"]

		Expect(s3.Required).To(ConsistOf("bucket", "region"))
		Expect(s3.Properties).To(HaveKey("accessKeyId"))
		Expect(s3.Properties).To(HaveKey("secretAccessKey"))
		Expect(s3.Properties).To(HaveKey("inheritFromIAMRole"))
		Expect(s3.Properties).ToNot(HaveKey("processCommand"))

		keyType := s3.Properties["keyType"]
		Expect(keyType.Enum).To(ConsistOf(
			apiextensionsv1.JSON{Raw: []byte(`"shared"`)},
			apiextensionsv1.JSON{Raw: []byte(`"auto"`)},
			apiextensionsv1.JSON{Raw: []byte(`"web-id"`)},
			apiextensionsv1.JSON{Raw: []byte(`"pod-id"`)},
		))
		Expect(keyType.Default).To(BeNil())

		Expect(s3.XValidations).To(HaveLen(1))
		Expect(s3.XValidations[0].Rule).To(Equal(
			"has(self.accessKeyId) == has(self.secretAccessKey)"))
	})
})
