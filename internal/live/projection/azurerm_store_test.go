// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
)

// TestResolveAzureRMFollowsStocksEnvironment: an unset argument falls back
// to the ARM_* variable stock's azurerm backend reads, a set one wins over
// it, and use_cli is true when nothing says otherwise, as in stock.
func TestResolveAzureRMFollowsStocksEnvironment(t *testing.T) {
	env := map[string]string{
		"ARM_SUBSCRIPTION_ID":          "from-env",
		"ARM_TENANT_ID":                "tenant-env",
		"ACTIONS_ID_TOKEN_REQUEST_URL": "https://actions",
		"ARM_USE_AZUREAD":              "true",
	}
	f := false
	a, bools, err := resolveAzureRM(configs.LiveRecordStoreAzureRM{TenantID: "tenant-cfg", UseMSI: &f}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if a.SubscriptionID != "from-env" || a.TenantID != "tenant-cfg" || a.OIDCRequestURL != "https://actions" {
		t.Errorf("resolved %+v", a)
	}
	if !bools["use_cli"] || !bools["use_azuread_auth"] || bools["use_msi"] || bools["use_oidc"] {
		t.Errorf("booleans %v", bools)
	}
	if _, _, err := resolveAzureRM(configs.LiveRecordStoreAzureRM{}, func(k string) string {
		if k == "ARM_USE_CLI" {
			return "maybe"
		}
		return ""
	}); err == nil {
		t.Error("a non-boolean ARM_USE_CLI was accepted")
	}
}
