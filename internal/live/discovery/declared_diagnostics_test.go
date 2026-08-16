// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestDeclaredDiagnosticsNeedsNoProvider is #224's discovery half: the same
// mismatch [TestDiscoverResolutionNotInConfig] catches through [Discover]
// (which refuses outright without req.Provider) is caught here with
// req.Provider left nil - proving declaredInstances' three diagnostics
// really do come from req.Config and req.Resolutions alone, not from
// anything [Discover]'s provider gate was standing in front of.
func TestDeclaredDiagnosticsNeedsNoProvider(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := []identity.Resolution{{
		Addr:   mustAddr(t, `aws_vpc.ghost`),
		Class:  identity.ClassNeedsDiscovery,
		Reason: "server-assigned",
	}}

	diags := DeclaredDiagnostics(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		// Provider deliberately absent.
	})
	if !hasDiag(diags, "Resolved resource missing from the configuration", "aws_vpc.ghost") {
		t.Errorf("want the mismatch diagnostic with no provider handle, got:\n%s", renderDiags(diags))
	}
}

// TestDeclaredDiagnosticsMatchesDiscover pins the other direction: over a
// resolution set that IS consistent with the configuration,
// DeclaredDiagnostics produces no error, the same as the declared-instances
// half of a real Discover call would.
func TestDeclaredDiagnosticsMatchesDiscover(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	var static []identity.Resolution
	for _, r := range resolveOrFail(t, cfg).All() {
		if r.Class != identity.ClassNeedsDiscovery {
			continue
		}
		static = append(static, r)
	}

	diags := DeclaredDiagnostics(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: static,
	})
	if diags.HasErrors() {
		t.Errorf("a resolution set consistent with the configuration produced errors with no provider:\n%s", renderDiags(diags))
	}
}

// TestDeclaredDiagnosticsRefusesNoConfig: unlike declaredInstances itself,
// which would dereference a nil Config's Module field, this entry point
// refuses before that happens - the same refusal [Discover] gives for a nil
// Config, since this is the half of Discover that runs without its
// provider gate, not a wrapper that get to skip Discover's other checks.
func TestDeclaredDiagnosticsRefusesNoConfig(t *testing.T) {
	diags := DeclaredDiagnostics(context.Background(), Request{
		Estate: estateName,
	})
	if !hasDiag(diags, "No configuration to discover against", "") {
		t.Errorf("want the no-config refusal, got:\n%s", renderDiags(diags))
	}
}
