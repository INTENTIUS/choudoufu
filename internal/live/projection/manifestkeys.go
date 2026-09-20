// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #1211's half of the manifest mirror: the one
// fact [mirrorManifestComputedFields] needs and neither the provider nor
// the cluster can supply, and the two seams it arrives through.
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
//	(keys the configuration declares) ∪ (keys it USED to declare and no longer does)
//
// and the second half is a fact about this estate's own history, which is
// what a stock state file's last-applied manifest holds and a stateless
// run has to record for itself.
//
// # Why the record, and not metadata.managedFields
//
// managedFields was the first route and it is refuted, measured on kind
// v1.36.1 with hashicorp/kubernetes 3.2.1 (#1211, PR #1259). It answers
// "who wrote this field" exactly, and the question is "did this
// configuration declare it". On this provider the two diverge: the
// computed_fields rule makes PlanResourceChange take the LIVE value of
// metadata.labels and metadata.annotations whenever the configuration
// equals the prior manifest, so the apply resends every key the object
// already had - foreign ones included - and server-side apply then
// records OUR manager as their writer. One apply later, managedFields
// says we own kubernetes.io/metadata.name, which the API server writes
// onto every Namespace and no configuration can declare away. A removal
// rule built on that set proposes deleting it, the server writes it back,
// and the plan churns for ever. After the laundering our manager is its
// ONLY owner, so no co-ownership filter rescues it.
// internal/live/kubesweep's launderedEntries pins that reading verbatim.
//
// managedFields survives here as a SAFETY RAIL and never as the source:
// a key the record names is only proposed for removal if our own field
// manager still owns it. The rail cannot invent a candidate, only decline
// one, so the laundering above makes it permissive rather than wrong.
//
// # Degradation
//
// A MISSING record proposes removing nothing. That is the pre-#1211
// behaviour - quiet, and wrong in the direction everyone has already
// lived with - rather than churn, which is the failure mode #1211's
// scouting measured and which no operator can work around.
//
// A STALE record still says what this estate last declared, which is
// precisely the semantic wanted; and a record naming a key the live
// object no longer carries proposes nothing either, because
// [mirrorMetadataMap] only ever mirrors a key the live object has. So
// there are two independent reasons a stale record cannot churn.

// ManifestOwnedKeysFunc answers, for one live manifest-shaped object,
// which of its metadata map keys this run's own Kubernetes field manager
// wrote. It is [Options.ManifestOwnedKeys]; the implementation the
// commands supply reads metadata.managedFields off the live object
// through the marker sweep's cluster client
// ([internal/live/kubesweep.ManagedMetadataKeys]).
//
// It is the safety rail described above, never the source, and it is
// consulted ONLY when the record has already named a candidate for
// removal - so an estate with nothing removed pays no round trip and
// earns no warning. See [manifestRemovalKeys].
//
// An error is a cluster that could not answer, which is never the same
// thing as an object whose managedFields say we own nothing: the first
// costs the run the removal analysis and is reported, the second is an
// exact answer worth acting on.
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

// SummaryManifestRemovalUndetectable is the warning a run issues when the
// estate's own record says a metadata key was declared, the configuration
// no longer declares it, the live object still carries it - and this run
// still will not propose removing it, because the safety rail could not
// be consulted or says the key is not ours.
//
// It is deliberately about the CONSEQUENCE rather than the mechanism. An
// operator reading it has deleted a label from a configuration and needs
// to know the plan is not going to act on that; which of the rail's four
// failure modes produced it is the sentence's second half.
//
// A warning and not an error. The estate still plans: everything the
// configuration DOES declare is compared against the live object exactly
// as before, and the only analysis lost is the removal one. Refusing the
// whole estate because one cluster would not answer a supplementary GET
// is the failure mode HANDOFF's safety rule names - drop to the weaker
// rung, never refuse the estate.
const SummaryManifestRemovalUndetectable = "A removed label or annotation cannot be removed"

// manifestKeyLookup is one instance's binding of everything #1211's read
// side needs: the keys the estate's record says were last declared, plus
// the safety rail's hook and the two addresses its request needs. Settled
// by [builder.prepareRead], where the address and the record store are in
// hand, and carried on the [readPrep] to the read itself, which has
// neither.
//
// It is nil for an instance whose type is not manifest-shaped, and for
// every caller of [importAndRead] that builds no such request - the
// package's own tests. A non-nil lookup with a nil declared map is an
// instance with no record, which proposes removing nothing.
type manifestKeyLookup struct {
	addr     addrs.AbsResourceInstance
	provider addrs.AbsProviderConfig
	hook     ManifestOwnedKeysFunc

	// declared is the record's answer, keyed by metadata map attribute
	// name, or nil when this instance has no record to read.
	declared map[string][]string
}

// newManifestKeyLookup returns the binding for one prepared read, or nil
// when the type is not manifest-shaped. Nil is the whole of the
// "everything else is unaffected" guarantee: an AWS instance never
// reaches the rail, never pays a GET, and never earns a warning.
func newManifestKeyLookup(schema providers.Schema, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, hook ManifestOwnedKeysFunc, declared map[string][]string) *manifestKeyLookup {
	if !markers.ManifestSurface(schema.Block) {
		return nil
	}
	return &manifestKeyLookup{addr: addr, provider: provider, hook: hook, declared: declared}
}

// manifestDeclaredKeysFor reads this instance's recorded declared key
// sets, or nil when there is no store, no record, or the type is not
// manifest-shaped.
//
// The type check comes first so that a non-Kubernetes estate pays no
// record-store read at all for this: [builder.prepareRead] already reads
// the same envelope for residue seeding on every instance, so for a
// manifest-shaped one this is a cache hit rather than a second round
// trip, but an AWS estate should not even ask.
//
// A store error is swallowed, for [builder.fillResidueFor]'s reason: the
// same envelope is read again later in this run through
// [builder.fillResidueFor], which raises [SummaryResidueUnreadable] for
// exactly this failure, and a second sentence here would be an echo. The
// consequence of swallowing it is the missing-record degradation, which
// is quiet by design.
func (b *builder) manifestDeclaredKeysFor(ctx context.Context, addr addrs.AbsResourceInstance, schema providers.Schema) map[string][]string {
	if b.opts.RecordStore == nil || !markers.ManifestSurface(schema.Block) {
		return nil
	}
	keys, found, err := b.opts.RecordStore.GetManifestDeclaredKeys(ctx, addr)
	if err != nil || !found {
		return nil
	}
	return keys
}

// manifestRemovalKeys resolves the set of metadata keys
// [mirrorManifestComputedFields] must widen the prior manifest with: the
// keys this estate's record says it last declared, that the configuration
// no longer declares, that the live object still carries, and that the
// safety rail agrees are ours.
//
// live is the whole value the provider read back - its
// [markers.ManifestSurfaceAttr] is the prior manifest (the seed, so the
// CURRENT configuration) and its [markers.ManifestLiveAttr] is the live
// object.
//
// Returns nil and no diagnostic in the three ordinary quiet cases: not a
// manifest-shaped type, no record, and nothing to remove. The third is
// the overwhelmingly common one and is why the rail costs no round trip
// on a converged estate.
func manifestRemovalKeys(ctx context.Context, live cty.Value, lookup *manifestKeyLookup) (map[string]map[string]bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if lookup == nil || len(lookup.declared) == 0 {
		return nil, diags
	}
	candidates, ok := manifestRemovalCandidates(live, lookup.declared)
	if !ok || len(candidates) == 0 {
		return nil, diags
	}

	undetectable := func(why string) (map[string]map[string]bool, tfdiags.Diagnostics) {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestRemovalUndetectable, fmt.Sprintf(
			"%s no longer declares %s, and the live object still carries %s, but this run will not propose removing %s: %s. Everything the configuration does declare is still compared against the live object.",
			lookup.addr, candidateList(candidates), themOrIt(candidates), themOrIt(candidates), why,
		)))
	}

	if lookup.hook == nil {
		return undetectable("this command supplies no cluster client, so metadata.managedFields could not be read to confirm the key is this estate's own to remove")
	}
	ref, ok := manifestObjectRef(live)
	if !ok {
		return undetectable("the object the provider read back does not name an apiVersion, a kind and a metadata.name, so metadata.managedFields could not be read")
	}
	ref.Addr = lookup.addr
	ref.Provider = lookup.provider

	owned, err := lookup.hook(ctx, ref)
	if err != nil {
		return undetectable(err.Error())
	}
	if !owned.ManagedFieldsPresent || owned.Keys == nil {
		return undetectable("the live object carries no metadata.managedFields, so the API server has no record of which fields this estate's field manager wrote")
	}

	// The rail. It can only narrow: a key the record never named cannot
	// enter here, which is what makes the laundering this file's doc
	// comment describes harmless.
	out := map[string]map[string]bool{}
	declined := map[string]map[string]bool{}
	for field, keys := range candidates {
		for key := range keys {
			if owned.Keys[field][key] {
				if out[field] == nil {
					out[field] = map[string]bool{}
				}
				out[field][key] = true
				continue
			}
			if declined[field] == nil {
				declined[field] = map[string]bool{}
			}
			declined[field][key] = true
		}
	}
	if len(declined) > 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryManifestRemovalUndetectable, fmt.Sprintf(
			"%s no longer declares %s, and the live object still carries %s, but this run will not propose removing %s: the live object's metadata.managedFields says another field manager owns it now, and server-side apply would decline the removal anyway. Reclaim the key first (kubectl label --overwrite, or an apply that declares it again) if this estate should own it.",
			lookup.addr, candidateList(declined), themOrIt(declined), themOrIt(declined),
		)))
	}
	if len(out) == 0 {
		return nil, diags
	}
	return out, diags
}

// manifestRemovalCandidates is [manifestRemovalKeys]' record half, split
// out so it can be tested with no cluster and no hook at all: for each
// metadata map, the recorded declared keys the CURRENT prior manifest no
// longer declares and the LIVE object still carries.
//
// Both filters matter and for different reasons. The first is the whole
// question. The second is what keeps a stale record quiet: a record
// naming a key nothing on the object answers for would otherwise reach
// the rail, buy a round trip and possibly a warning, for a removal
// [mirrorMetadataMap] would decline to make anyway.
//
// ok is false when the value is not the manifest shape this reads - the
// caller then behaves exactly as it does for a type with no record.
func manifestRemovalCandidates(live cty.Value, declared map[string][]string) (map[string]map[string]bool, bool) {
	if live == cty.NilVal || live.IsNull() || !live.IsKnown() || live.IsMarked() || !live.Type().IsObjectType() {
		return nil, false
	}
	if !live.Type().HasAttribute(markers.ManifestSurfaceAttr) || !live.Type().HasAttribute(markers.ManifestLiveAttr) {
		return nil, false
	}
	priorMeta, ok := manifestMetadata(live.GetAttr(markers.ManifestSurfaceAttr))
	if !ok {
		return nil, false
	}
	liveMeta, ok := manifestMetadata(live.GetAttr(markers.ManifestLiveAttr))
	if !ok {
		return nil, false
	}

	out := map[string]map[string]bool{}
	for _, field := range markers.ManifestComputedMetadataAttrs {
		recorded := declared[field]
		if len(recorded) == 0 {
			continue
		}
		current := metadataMapKeys(priorMeta, field)
		liveKeys, ok := liveMetadataMap(liveMeta, field)
		if !ok {
			continue
		}
		for _, key := range recorded {
			if current[key] {
				continue
			}
			if _, stillThere := liveKeys[key]; !stillThere {
				continue
			}
			if out[field] == nil {
				out[field] = map[string]bool{}
			}
			out[field][key] = true
		}
	}
	return out, true
}

// ManifestDeclaredKeys is #1211's WRITE half: the keys an applied
// kubernetes_manifest object's own `manifest` argument declares at
// metadata.labels and metadata.annotations, in the shape
// [residueFields.ManifestMetadataKeys] holds.
//
// The applied manifest rather than the configuration source, because
// that is what write-back has in hand and because it is the value that
// was actually sent - the estate's tofu-estate marker included, which is
// declared by this fork on the configuration's behalf and must be
// recorded as declared or the next run would propose removing the marker
// it just wrote.
//
// Exported for GitHub issue #1391's second writer. live-import migrates a
// manifest-shaped instance out of a stock state file and has to seed the
// same record from the state's own recorded object, and two writers
// computing the same key set two ways is how they drift. The migrate
// caller is internal/live/liveimport's seedManifestKeys, which hands the
// STATE's object rather than a live read and adds the marker key this
// migration writes by merge patch.
//
// nil (and false) for anything that is not the manifest shape. An object
// that declares neither map records an empty entry for each, which is a
// real answer - "this configuration declared no labels" - and is how a
// removal that empties a map stays visible.
func ManifestDeclaredKeys(v cty.Value) (map[string][]string, bool) {
	if v == cty.NilVal || v.IsNull() || !v.IsKnown() || !v.Type().IsObjectType() {
		return nil, false
	}
	v, _ = v.Unmark()
	if !v.Type().HasAttribute(markers.ManifestSurfaceAttr) {
		return nil, false
	}
	manifest := v.GetAttr(markers.ManifestSurfaceAttr)
	manifest, _ = manifest.Unmark()
	meta, ok := manifestMetadata(manifest)
	if !ok {
		return nil, false
	}
	out := make(map[string][]string, len(markers.ManifestComputedMetadataAttrs))
	for _, field := range markers.ManifestComputedMetadataAttrs {
		keys := metadataMapKeys(meta, field)
		list := make([]string, 0, len(keys))
		for key := range keys {
			list = append(list, key)
		}
		sort.Strings(list)
		out[field] = list
	}
	return out, true
}

// metadataMapKeys reads the key set of one metadata string map off a
// manifest's metadata object. An absent, null, unknown, marked or
// non-iterable map is an empty set: on the read side that means "the
// configuration declares no such key", which is exactly what an absent
// `annotations` attribute means after the last annotation is deleted.
func metadataMapKeys(meta cty.Value, field string) map[string]bool {
	out := map[string]bool{}
	if !meta.Type().HasAttribute(field) {
		return out
	}
	m := meta.GetAttr(field)
	if m.IsNull() || !m.IsKnown() || m.IsMarked() || !m.CanIterateElements() {
		return out
	}
	for it := m.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		if k.Type() != cty.String || k.IsNull() {
			continue
		}
		out[k.AsString()] = true
	}
	return out
}

// candidateList renders a per-field key set for one diagnostic sentence,
// as `metadata.labels.squad` and `metadata.annotations.owner`, sorted so
// the same situation always produces the same line.
func candidateList(sets map[string]map[string]bool) string {
	var names []string
	for _, field := range markers.ManifestComputedMetadataAttrs {
		keys := make([]string, 0, len(sets[field]))
		for key := range sets[field] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			names = append(names, fmt.Sprintf("metadata.%s.%s", field, key))
		}
	}
	switch len(names) {
	case 0:
		return "any metadata key"
	case 1:
		return names[0]
	default:
		return fmt.Sprintf("%s and %s", joinComma(names[:len(names)-1]), names[len(names)-1])
	}
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func countKeys(sets map[string]map[string]bool) int {
	n := 0
	for _, keys := range sets {
		n += len(keys)
	}
	return n
}

// themOrIt is the pronoun for one candidate set, so that a one-key and a
// two-key warning are both grammatical English rather than one of them
// reading "still carries them" about a single label.
func themOrIt(sets map[string]map[string]bool) string {
	if countKeys(sets) == 1 {
		return "it"
	}
	return "them"
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
