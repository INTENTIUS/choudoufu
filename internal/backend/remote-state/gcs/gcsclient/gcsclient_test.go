// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcsclient

import "testing"

// TestClientConfigFromEnvKeepsTheSchemaOrder: an argument that is set wins,
// and an unset one takes the first environment variable the gcs backend's
// schema names for it.
func TestClientConfigFromEnvKeepsTheSchemaOrder(t *testing.T) {
	t.Setenv("GOOGLE_OAUTH_ACCESS_TOKEN", "env-token")
	t.Setenv("GOOGLE_BACKEND_IMPERSONATE_SERVICE_ACCOUNT", "backend@sa")
	t.Setenv("GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "general@sa")
	t.Setenv("GOOGLE_BACKEND_STORAGE_CUSTOM_ENDPOINT", "")
	t.Setenv("GOOGLE_STORAGE_CUSTOM_ENDPOINT", "http://localhost/storage/v1/")
	t.Setenv("GOOGLE_BACKEND_UNIVERSE_DOMAIN", "")

	got := ClientConfigFromEnv(ClientConfig{AccessToken: "set-token"})
	if got.AccessToken != "set-token" {
		t.Errorf("AccessToken = %q, want the argument over the environment", got.AccessToken)
	}
	if got.ImpersonateServiceAccount != "backend@sa" {
		t.Errorf("ImpersonateServiceAccount = %q, want the GOOGLE_BACKEND_ variable first", got.ImpersonateServiceAccount)
	}
	if got.StorageCustomEndpoint != "http://localhost/storage/v1/" {
		t.Errorf("StorageCustomEndpoint = %q, want the second variable when the first is empty", got.StorageCustomEndpoint)
	}
	if got.UniverseDomain != "" {
		t.Errorf("UniverseDomain = %q, want empty", got.UniverseDomain)
	}
}

func TestDecodeEncryptionKey(t *testing.T) {
	if k, err := DecodeEncryptionKey(""); k != nil || err != nil {
		t.Errorf("DecodeEncryptionKey(\"\") = %v, %v; want nil, nil", k, err)
	}
	if _, err := DecodeEncryptionKey("not base64!"); err == nil {
		t.Error("an undecodable key was accepted")
	}
}
