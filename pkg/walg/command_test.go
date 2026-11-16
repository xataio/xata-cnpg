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

package walg

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
)

func TestIsWALNotFound(t *testing.T) {
	for _, output := range []string{
		"ERROR: Archive '000000010000000000000003' does not exist.",
		"NoSuchKey: The specified key does not exist",
		"object not found",
	} {
		if !isWALNotFound(output) {
			t.Errorf("expected %q to be classified as WAL not found", output)
		}
	}
	for _, output := range []string{
		"AccessDenied: Access Denied",
		"dial tcp: connection refused",
	} {
		if isWALNotFound(output) {
			t.Errorf("expected %q not to be classified as WAL not found", output)
		}
	}
}

func TestCommandError_Error(t *testing.T) {
	err := &CommandError{Command: "wal-fetch", Args: []string{"wal-fetch", "a", "b"}, Output: "boom\n", ExitCode: 1}
	if got := err.Error(); got != "wal-g wal-fetch failed with exit code 1: boom" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func testSource() *apiv1.WalGExternalCluster {
	return &apiv1.WalGExternalCluster{
		S3: apiv1.WalGS3{
			Prefix:         "s3://pgbucket",
			Region:         "us-west-2",
			Endpoint:       "https://s3.us-west-2.amazonaws.com",
			ForcePathStyle: true,
		},
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

func TestArchiveEnv_NoStaticCredentials(t *testing.T) {
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	env, err := ArchiveEnv(context.Background(), cli, "default", testSource())
	if err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]string{
		"WALG_S3_PREFIX":          "s3://pgbucket",
		"AWS_REGION":              "us-west-2",
		"AWS_ENDPOINT":            "https://s3.us-west-2.amazonaws.com",
		"AWS_S3_FORCE_PATH_STYLE": "true",
	} {
		if got, ok := envValue(env, key); !ok || got != want {
			t.Errorf("%s = %q (present=%v), want %q", key, got, ok, want)
		}
	}
	if _, ok := envValue(env, "AWS_ACCESS_KEY_ID"); ok {
		t.Error("did not expect static credentials in env")
	}
}

func TestArchiveEnv_StaticCredentials(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "walg-creds", Namespace: "default"},
		Data:       map[string][]byte{"id": []byte("AKIA"), "key": []byte("s3cr3t")},
	}
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()

	src := testSource()
	src.S3.AccessKeyID = &apiv1.SecretKeySelector{LocalObjectReference: apiv1.LocalObjectReference{Name: "walg-creds"}, Key: "id"}
	src.S3.SecretAccessKey = &apiv1.SecretKeySelector{LocalObjectReference: apiv1.LocalObjectReference{Name: "walg-creds"}, Key: "key"}

	env, err := ArchiveEnv(context.Background(), cli, "default", src)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := envValue(env, "AWS_ACCESS_KEY_ID"); got != "AKIA" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q", got)
	}
	if got, _ := envValue(env, "AWS_SECRET_ACCESS_KEY"); got != "s3cr3t" {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q", got)
	}
}

func TestArchiveEnv_MissingSecretKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "walg-creds", Namespace: "default"},
		Data:       map[string][]byte{"id": []byte("AKIA")},
	}
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(secret).Build()

	src := testSource()
	src.S3.AccessKeyID = &apiv1.SecretKeySelector{LocalObjectReference: apiv1.LocalObjectReference{Name: "walg-creds"}, Key: "id"}
	src.S3.SecretAccessKey = &apiv1.SecretKeySelector{LocalObjectReference: apiv1.LocalObjectReference{Name: "walg-creds"}, Key: "key"}

	if _, err := ArchiveEnv(context.Background(), cli, "default", src); err == nil ||
		!strings.Contains(err.Error(), `key "key" not found`) {
		t.Errorf("expected missing key error, got %v", err)
	}
}

func TestEnvFileRoundTrip(t *testing.T) {
	orig := EnvFilePath
	EnvFilePath = filepath.Join(t.TempDir(), "walg", "env")
	t.Cleanup(func() { EnvFilePath = orig })

	want := []string{"WALG_S3_PREFIX=s3://b", "AWS_REGION=eu-west-1"}
	changed, err := WriteEnvFile(want)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	changed, err = WriteEnvFile(want)
	if err != nil || changed {
		t.Fatalf("second write should be a no-op: changed=%v err=%v", changed, err)
	}

	got, err := LoadEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("LoadEnvFile = %v, want %v", got, want)
	}
}
