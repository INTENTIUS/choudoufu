// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #1211's half of the manifest mirror: the one
// fact [mirrorManifestComputedFields] needs and the provider cannot
// supply, and the seam it arrives through.
//
// The mirror builds the prior manifest a kubernetes_manifest instance is
// planned against. It cannot mirror the live maps wholesale - that
// proposes deleting every server-written and foreign-manager key, for
// ever, against a server that writes them straight back (#1177's own doc
// comment on that function, and #1211's measured churn). It cannot mirror
// only the configuration's keys either - a key REMOVED from the
// configuration is then in neither the prior nor the configuration, the
// two agree, and nothing plans. The exact set is
//
//	(keys the configuration declares) ∪ (keys our field manager owns)
//
// and the second half is metadata.managedFields, which the provider
// strips from the `object` it hands back. So it takes a read this path
// does not make: one GET against the cluster, made by whoever supplied
// [Options.ManifestOwnedKeys], outside this package because this package
// has no Kubernetes client and should not grow one.

// ManifestOwnedKeysFunc answers, for one live manifest-shaped object,
// which of its metadata map keys this run's own Kubernetes field manager
// wrote. It is [Options.ManifestOwnedKeys]; the implementation the
// commands supply reads metadata.managedFields off the live object
// through the marker sweep's cluster client
// ([internal/live/kubesweep.ManagedMetadataKeys]).
//
// An error is a cluster that could not answer, which is never the same
// thing as an object whose managedFields say we own nothing: the first
// costs the run the removal analysis and is reported, the second is an
// exact answer worth acting on. See [ManifestOwnedKeys].
type ManifestOwnedKeysFunc func(ctx context.Context, req ManifestOwnedKeysRequest) (ManifestOwnedKeys, error)

// ManifestOwnedKeysRequest names the one live object being asked about.
//
// Addr and Provider are carried because the implementation needs both: a
// cluster client is per provider configuration (the sweep keeps one each,
// keyed the same way every configured provider instance is), and the
// field manager name is per resource block - a `field_manager { name =
// ... }` override has to be honoured, or the run would ask about
// "Terraform"'s keys on an object a differently-named manager wrote and
// conclude we own nothing.
//
// The four naming fields are the live object's own, read out of the
// provider's `object` attribute rather than out of the configuration, so
// that they name the object that was actually read.
type ManifestOwnedKeysRequest struct {
	Addr     addrs.AbsResourceInstance
	Provider addrs.AbsProviderConfig

	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

// ManifestOwnedKeys is [ManifestOwnedKeysFunc]'s answer.
type ManifestOwnedKeys struct {
	// Keys is the owned key set per metadata map, keyed by the map's
	// attribute name as [markers.ManifestComputedMetadataAttrs] spells it
	// ("labels", "annotations"). A named map with no owned key is an
	// empty set rather than an absent one.
	Keys map[string]map[string]bool

	// ManagedFieldsPresent reports that the live object carried
	// managedFields at all. False means the answer is not "we own
	// nothing", it is "this run could not tell", and the caller says so
	// rather than planning No changes and calling it converged.
	ManagedFieldsPresent bool
}

// SummaryManifestOwnedKeysUnavailable is the warning a run issues when it
// could not learn which metadata keys it owns on a live manifest-shaped
// object, and therefore cannot propose removing a label or an annotation
// the configuration no longer declares.
//
// A warning and not an error, deliberately. The estate still plans:
// everything the configuration DOES declare is compared against the live
// object exactly as before, and the only analysis lost is the removal
// one. Refusing the whole estate because one cluster would not answer a
// supplementary GET is the failure mode HANDOFF's safety rule names -
// drop to the weaker rung, never refuse the estate - and an operator who
// sees this line knows precisely which half of the plan to distrust.
const SummaryManifestOwnedKeysUnavailable = "Removed labels and annotations cannot be detected"

// manifestKeyLookup is one instance's binding of [Options.ManifestOwnedKeys]:
// the hook plus the two addresses the request needs, settled by
// [builder.prepareRead] where they are in hand and carried on the
// [readPrep] to the read itself, which has neither.
//
// It is nil for an instance whose type is not manifest-shaped, and for
// every caller of [importAndRead] that builds no such request - the
// package's own tests. A non-nil lookup with a nil hook is the case the
// warning above is for: a run that reached a manifest-shaped instance
// with no way to ask the cluster anything.
type manifestKeyLookup struct {
	addr     addrs.AbsResourceInstance
	provider addrs.AbsProviderConfig
	hook     ManifestOwnedKeysFunc
}

// newManifestKeyLookup returns the binding for one prepared read, or nil
// when the type is not manifest-shaped. Nil is the whole of the
// "everything else is unaffected" guarantee: an AWS instance never
// reaches the hook, never pays a GET, and never earns a warning.
func newManifestKeyLookup(schema providers.Schema, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, hook ManifestOwnedKeysFunc) *manifestKeyLookup {
	if !markers.ManifestSurface(schema.Block) {
		return nil
	}
	return &manifestKeyLookup{addr: addr, provider: provider, hook: hook}
}

// ownedManifestKeys resolves the owned key set for one instance's live
// object, or nil with a warning saying why it could not.
//
// live is the whole value the provider read back, whose
// [markers.ManifestLiveAttr] attribute is the live object. A nil lookup
// (not a manifest-shaped type) returns nil and no diagnostic at all,
// because there is nothing for the mirror to widen.
//
// Every other nil return carries the warning. That is the degradation
// rule this file exists to state: the three ways this can fail - no hook
// was supplied, the cluster could not answer, the object carries no
// managedFields - all produce today's narrow prior, and today's narrow
// prior plans No changes over a removed key. Returning the narrow prior
// silently is what #1211 IS.
func ownedManifestKeys(ctx context.Context, live cty.Value, lookup *manifestKeyLookup) (map[string]map[string]bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if lookup == nil {
		return nil, diags
	}
	unavailable := func(why string) (map[string]map[string]bool, tfdiags.Diagnostics) {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestOwnedKeysUnavailable, fmt.Sprintf(
			"This run could not read which metadata.labels and metadata.annotations keys it owns on the live object behind %s: %s. Everything the configuration declares is still compared against the live object, but a label or an annotation DELETED from the configuration cannot be proposed for removal on this run, so the plan may report no changes over a key that is still on the object.",
			lookup.addr, why,
		)))
	}
	if lookup.hook == nil {
		return unavailable("this command supplies no cluster client to read metadata.managedFields with")
	}
	ref, ok := manifestObjectRef(live)
	if !ok {
		return unavailable("the object the provider read back does not name an apiVersion, a kind and a metadata.name")
	}
	ref.Addr = lookup.addr
	ref.Provider = lookup.provider

	owned, err := lookup.hook(ctx, ref)
	if err != nil {
		return unavailable(err.Error())
	}
	if !owned.ManagedFieldsPresent {
		return unavailable("the live object carries no metadata.managedFields, so the API server has no record of which fields this estate's field manager wrote")
	}
	if owned.Keys == nil {
		// An answer that says managedFields were present and then names
		// no map at all is not an answer; treating it as "we own
		// nothing" would be the silent wrong answer again.
		return unavailable("the cluster read produced no key sets")
	}
	return owned.Keys, diags
}

// manifestObjectRef reads apiVersion, kind, metadata.namespace and
// metadata.name off the provider's live `object` attribute.
//
// The live object rather than the configuration's manifest, because the
// question is about the object that was read: an object the run reached
// by some other spelling of the same identity is still the object whose
// managedFields answer. namespace is legitimately empty for a
// cluster-scoped kind, so it is not part of ok.
func manifestObjectRef(v cty.Value) (ManifestOwnedKeysRequest, bool) {
	var out ManifestOwnedKeysRequest
	if v == cty.NilVal || v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.Type().IsObjectType() {
		return out, false
	}
	if !v.Type().HasAttribute(markers.ManifestLiveAttr) {
		return out, false
	}
	obj := v.GetAttr(markers.ManifestLiveAttr)
	if obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() {
		return out, false
	}
	out.APIVersion = manifestString(obj, "apiVersion")
	out.Kind = manifestString(obj, "kind")
	meta, ok := manifestMetadata(obj)
	if !ok {
		return out, false
	}
	out.Namespace = manifestString(meta, "namespace")
	out.Name = manifestString(meta, "name")
	return out, out.APIVersion != "" && out.Kind != "" && out.Name != ""
}

// manifestString reads one string attribute off a dynamic object value,
// or "" for anything that is not a known, unmarked, non-null string -
// including an attribute the object does not have, which for a
// cluster-scoped object's metadata.namespace is the ordinary case.
func manifestString(obj cty.Value, name string) string {
	if !obj.Type().HasAttribute(name) {
		return ""
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || v.IsMarked() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}
