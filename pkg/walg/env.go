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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/postgres"
)

// EnvFilePath is where the instance manager persists the archive environment
// so that `wal-restore`, which runs without a Kubernetes client, can reuse it.
// It lives on the scratch volume, so it never reaches the data directory.
var EnvFilePath = postgres.ScratchDataDirectory + "/walg/env"

// ArchiveEnv builds the environment variables wal-g needs to reach the archive
// described by source. Static credentials are read from the referenced
// Secrets; without them wal-g falls back to the ambient AWS credential chain.
func ArchiveEnv(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	source *apiv1.WalGExternalCluster,
) ([]string, error) {
	if source == nil {
		return nil, fmt.Errorf("wal-g source is nil")
	}

	env := []string{
		"WALG_S3_PREFIX=" + source.S3.Prefix,
		"AWS_REGION=" + source.S3.Region,
		"AWS_S3_FORCE_PATH_STYLE=" + strconv.FormatBool(source.S3.ForcePathStyle),
	}
	if source.S3.Endpoint != "" {
		env = append(env, "AWS_ENDPOINT="+source.S3.Endpoint)
	}

	if source.S3.HasStaticCredentials() {
		accessKey, err := resolveSecretKeyRef(ctx, k8sClient, namespace, source.S3.AccessKeyID)
		if err != nil {
			return nil, err
		}
		secretKey, err := resolveSecretKeyRef(ctx, k8sClient, namespace, source.S3.SecretAccessKey)
		if err != nil {
			return nil, err
		}
		env = append(env,
			"AWS_ACCESS_KEY_ID="+accessKey,
			"AWS_SECRET_ACCESS_KEY="+secretKey,
		)
	}

	return env, nil
}

// ProcessEnv returns the current process environment with the archive
// variables appended, ready for exec
func ProcessEnv(archiveEnv []string) []string {
	return append(append([]string{}, os.Environ()...), archiveEnv...)
}

// WriteEnvFile persists the archive environment to EnvFilePath, one KEY=VALUE
// per line. Returns whether the content changed.
func WriteEnvFile(archiveEnv []string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(EnvFilePath), 0o700); err != nil {
		return false, fmt.Errorf("creating directory %s: %w", filepath.Dir(EnvFilePath), err)
	}

	content := strings.Join(archiveEnv, "\n") + "\n"
	existing, err := os.ReadFile(EnvFilePath)
	if err == nil && string(existing) == content {
		return false, nil
	}

	if err := os.WriteFile(EnvFilePath, []byte(content), 0o600); err != nil {
		return false, fmt.Errorf("writing %s: %w", EnvFilePath, err)
	}
	return true, nil
}

// LoadEnvFile reads the archive environment persisted by WriteEnvFile
func LoadEnvFile() ([]string, error) {
	content, err := os.ReadFile(EnvFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", EnvFilePath, err)
	}

	var env []string
	for _, line := range strings.Split(string(content), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			env = append(env, line)
		}
	}
	return env, nil
}

func resolveSecretKeyRef(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	selector *apiv1.SecretKeySelector,
) (string, error) {
	if selector == nil {
		return "", fmt.Errorf("secret key selector is nil")
	}

	var secret corev1.Secret
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: selector.Name, Namespace: namespace}, &secret); err != nil {
		return "", fmt.Errorf("fetching secret %s/%s: %w", namespace, selector.Name, err)
	}

	value, ok := secret.Data[selector.Key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %s/%s", selector.Key, namespace, selector.Name)
	}

	return string(value), nil
}
