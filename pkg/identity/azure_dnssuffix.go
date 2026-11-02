// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "strings"

// Azure data-plane DNS suffixes (GitHub issue #2206, the maintainer's ruling
// of 2026-10-23 on #2201 item 5: the environment suffix is a provider-block
// value, like the subscription). A data-plane id is a URL whose host is the
// parent's name plus the cloud's suffix for that service, so what a
// configuration states (the vault's name) and what the provider block states
// (the environment) are enough to build it.
//
// The suffixes are the ones go-azure-sdk's sdk/environments package gives
// each cloud, which is what azurerm and azapi resolve `environment` through
// (azsweep reads the same names). They were written here from that package's
// published values and have not been checked against a checkout in this
// worktree; the tests pin them, and a wrong one fails by naming a host that
// matches no object, never by naming a wrong one that exists.

// azureSuffixes maps a canonical environment to each suffix value's text.
var azureSuffixes = map[string]map[CloudValue]string{
	"public": {
		CloudKeyVaultDNSSuffix:         "vault.azure.net",
		CloudManagedHSMDNSSuffix:       "managedhsm.azure.net",
		CloudAppConfigurationDNSSuffix: "azconfig.io",
		CloudStorageDNSSuffix:          "core.windows.net",
	},
	"usgovernment": {
		CloudKeyVaultDNSSuffix:         "vault.usgovcloudapi.net",
		CloudManagedHSMDNSSuffix:       "managedhsm.usgovcloudapi.net",
		CloudAppConfigurationDNSSuffix: "azconfig.azure.us",
		CloudStorageDNSSuffix:          "core.usgovcloudapi.net",
	},
	"china": {
		CloudKeyVaultDNSSuffix:         "vault.azure.cn",
		CloudManagedHSMDNSSuffix:       "managedhsm.azure.cn",
		CloudAppConfigurationDNSSuffix: "azconfig.azure.cn",
		CloudStorageDNSSuffix:          "core.chinacloudapi.cn",
	},
}

// canonicalAzureEnvironment is the lower-case name of an environment this
// package knows suffixes for. Both providers match the name without regard to
// case.
func canonicalAzureEnvironment(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if _, ok := azureSuffixes[n]; !ok {
		return "", false
	}
	return n, true
}

// AzureDNSSuffix is the DNS suffix of one data-plane service in an Azure
// environment: which is one of [CloudKeyVaultDNSSuffix],
// [CloudManagedHSMDNSSuffix], [CloudAppConfigurationDNSSuffix] and
// [CloudStorageDNSSuffix]. ok is false for an environment or a value it has
// no entry for.
func AzureDNSSuffix(environment string, which CloudValue) (string, bool) {
	env, ok := canonicalAzureEnvironment(environment)
	if !ok {
		return "", false
	}
	v, ok := azureSuffixes[env][which]
	return v, ok
}

// azureSuffixService reports whether which is one of the DNS suffix values.
func azureSuffixService(which CloudValue) (CloudValue, bool) {
	switch which {
	case CloudKeyVaultDNSSuffix, CloudManagedHSMDNSSuffix, CloudAppConfigurationDNSSuffix, CloudStorageDNSSuffix:
		return which, true
	}
	return "", false
}
