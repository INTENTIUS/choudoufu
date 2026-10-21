// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package auth

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

// TestInstanceDiscoveryOff: a credential for a loopback authority (an
// emulator such as floci-az) never asks login.microsoftonline.com's
// instance discovery about it (GitHub issue #2133, the endpoint lock), and
// one for Azure's own authorities is left as MSAL has it unless the caller
// turns discovery off.
func TestInstanceDiscoveryOff(t *testing.T) {
	for authority, want := range map[string]bool{
		"https://localhost:45770/":           true,
		"https://LOCALHOST:45770":            true,
		"https://blob.localhost:45770/":      true,
		"https://127.0.0.1:4577/":            true,
		"https://[::1]:4577/":                true,
		"https://login.microsoftonline.com/": false,
		"https://login.microsoftonline.us/":  false,
		"https://localhost.example.com/":     false,
		"":                                   false,
		"::not a url":                        false,
	} {
		c := &Config{StorageAddresses: StorageAddresses{CloudConfig: cloud.Configuration{ActiveDirectoryAuthorityHost: authority}}}
		if got := instanceDiscoveryOff(c); got != want {
			t.Errorf("instanceDiscoveryOff(%q) = %v, want %v", authority, got, want)
		}
	}
	c := &Config{DisableInstanceDiscovery: true, StorageAddresses: StorageAddresses{CloudConfig: cloud.AzurePublic}}
	if !instanceDiscoveryOff(c) {
		t.Error("DisableInstanceDiscovery did not turn discovery off for the public cloud")
	}
}
