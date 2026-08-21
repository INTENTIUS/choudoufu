// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestKubernetesConfigMapIdentity proves the ratified kubernetes_config_map
// row (GitHub issue #326: the type blocking corpus-eks-basic's test_plan
// stage) against the real, current hashicorp/kubernetes provider docs
// (docs/resources/config_map.md, fetched live - the offline cache has no
// Kubernetes provider data): "Config Map can be imported using its
// namespace and name, e.g. $ terraform import kubernetes_config_map.example
// default/my-config" - a NAMESPACE/NAME composite read out of the required
// metadata block via [Component.Block] (#310), the same mechanism
// aws_autoscaling_traffic_source_attachment's own ruling already exercises
// (see TestNestedBlockComponent).
//
// This is also the "admitted-but-unstamped" half remaining-work item 3
// asked for, at the identity layer: kubernetes_config_map's ratified row
// leaves [TypeIdentity.ServerAssigned] false, because both namespace and
// name are operator-supplied in metadata, not minted by the Kubernetes API
// server - unlike a ServerAssigned row, present never classifies
// [ClassNeedsDiscovery] and never asks a marker/discovery question at all.
// internal/live/stamp's own TestKubernetesConfigMapIsAdmittedButUntaggable
// pins the other half: the type carries no top-level tags attribute, so the
// stamp layer skips it as untaggable even though it resolves and plans.
func TestKubernetesConfigMapIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "kubernetes-config-map"), nil)
	result, diags := Resolve(context.Background(), cfg)

	present := resolutionAt(t, result, "kubernetes_config_map.present")
	if present.Class != ClassConcrete {
		t.Fatalf("present resolved %s, want concrete (diagnostics: %s)", present.Class, renderDiags(diags))
	}
	// The provider's own documented import example shape, NAMESPACE/NAME,
	// verbatim.
	if want := "default/my-config"; present.ImportID != want {
		t.Errorf("present rendered %q, want %q", present.ImportID, want)
	}
	if got, want := present.IdentityValues["namespace"], "default"; got != want {
		t.Errorf("present's namespace identity value = %q, want %q", got, want)
	}
	if got, want := present.IdentityValues["name"], "my-config"; got != want {
		t.Errorf("present's name identity value = %q, want %q", got, want)
	}

	// The mutation check: metadata.namespace is Optional in the provider's
	// own schema (it defaults server-side to "default"), but the ratified
	// row reads it as a required identity component with no Default set. A
	// resolver that silently treated an absent namespace as the literal
	// string "default" would fabricate an identity the configuration never
	// stated - the "wrong marker outranks a missing one" trap - so this must
	// refuse with the same diagnostic a missing top-level required argument
	// gets, not resolve to a guessed identity.
	if !diags.HasErrors() {
		t.Fatalf("no error diagnostics for the namespace-absent instance; resolution produced %d instances", result.Len())
	}
	if !hasDiag(diags, "Identity argument not set", `kubernetes_config_map.no_namespace`) {
		t.Errorf("no diagnostic naming the namespace-absent instance. got:\n%s", renderDiags(diags))
	}
	if _, ok := result.Get(mustAddr(t, "kubernetes_config_map.no_namespace")); ok {
		t.Errorf("no_namespace was resolved anyway; a missing required identity component must be refused, not guessed as the provider's own server-side default")
	}
}
