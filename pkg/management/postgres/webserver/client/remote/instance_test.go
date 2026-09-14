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

package remote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

type trackedRequestBody struct {
	closeCalls int
	closeError error
}

func (b *trackedRequestBody) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (b *trackedRequestBody) Close() error {
	b.closeCalls++
	return b.closeError
}

type eofTransport struct{}

func (eofTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Body.Close(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func TestUpgradeInstanceManagerLeavesRequestBodyOwnershipToTransport(t *testing.T) {
	t.Parallel()

	body := &trackedRequestBody{}
	client := &instanceClientImpl{
		Client: &http.Client{Transport: eofTransport{}},
	}
	pod := &corev1.Pod{Status: corev1.PodStatus{PodIP: "127.0.0.1"}}

	if err := client.upgradeInstanceManager(context.Background(), pod, body); err != nil {
		t.Fatalf("expected EOF to indicate a successful upgrade, got %v", err)
	}
	if body.closeCalls != 1 {
		t.Fatalf("expected transport to close request body once, got %d calls", body.closeCalls)
	}
}

func TestUpgradeInstanceManagerReturnsRequestBodyCloseError(t *testing.T) {
	t.Parallel()

	closeError := errors.New("close request body")
	body := &trackedRequestBody{closeError: closeError}
	client := &instanceClientImpl{
		Client: &http.Client{Transport: eofTransport{}},
	}
	pod := &corev1.Pod{Status: corev1.PodStatus{PodIP: "127.0.0.1"}}

	err := client.upgradeInstanceManager(context.Background(), pod, body)
	if !errors.Is(err, closeError) {
		t.Fatalf("expected request body close error, got %v", err)
	}
	if body.closeCalls != 1 {
		t.Fatalf("expected transport to close request body once, got %d calls", body.closeCalls)
	}
}
