// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
	"github.com/intentius/choudoufu/pkg/adoption"
	pkgidentity "github.com/intentius/choudoufu/pkg/identity"
	"github.com/intentius/choudoufu/pkg/kubesweep"
	"github.com/intentius/choudoufu/pkg/registry"
)

// Adoption from an index (GitHub issue #2287; designed and ruled on #2284).
//
// `live-import -adopt FILE` starts from an `adopt: v1` adoption file in
// place of a tfstate: the file names an index version and, per selection,
// the index node to adopt, the Terraform address to adopt it as and, where
// the inventory type joins to more than one Terraform type, which one. The
// index only says which object. Everything a refusal is decided on comes
// from reading the object again through its provider here:
//
//  1. the Terraform type, from the selection or the registry's reverse join
//     (an ambiguous join with no type in the selection is refused, naming
//     every candidate);
//  2. a type pkg/identity classes as not importable is refused, and on aws
//     so is a type whose tags can be set only at create
//     ([registry.Roster.TagsCreateOnly]): a marker write would replace it.
//     Both are refused by name before any provider call;
//  3. the import id, from the node's native id through pkg/identity
//     (Cloud Control identifier translation on aws, the Asset name on GCP,
//     the ARM id as returned on Azure, the object's name on Kubernetes);
//  4. the provider's ImportResourceState then ReadResource of that id;
//  5. the markers on what that read returned: another estate's, a plunge
//     stack's or chant's marker is refused, and so is this estate's marker
//     naming another address (a rename is live-mv's job).
//
// What survives becomes one resource instance of an in-memory state, which
// [Ratify] then verifies as it would a tfstate's, and [Ratification.Approve]
// stamps unchanged: at -approve, one tags-only write per resource, with the
// replace refusal ([notATagsOnlyPlan]) as the backstop behind step 2.

// AdoptRequest is [Request] with an adoption file resolved against its index
// version in place of a state. Request.State is ignored: it is built here
// from the live reads.
type AdoptRequest struct {
	Request

	// Adoption is the adoption file resolved against the index version it
	// names ([adoption.Open] or [adoption.Resolve]).
	Adoption *adoption.Adoption

	// Roster is the aws registry roster the reverse type join and the
	// create-only table come from. nil means [registry.Embedded].
	Roster *registry.Roster
}

// AdoptRefusal is one selection that will not be adopted, and why.
type AdoptRefusal struct {
	Node       string
	As         string
	ListedType string
	// TypeName is the Terraform type the selection settled on, or "" when
	// the refusal came before one was settled.
	TypeName string
	// ImportID is the id the object was read by, or "" when the refusal came
	// before the read.
	ImportID string
	Reason   string
}

// AdoptImport says where one adopted instance came from.
type AdoptImport struct {
	Node     string
	ImportID string
}

// MarkerWrite is one write -approve would make for an adopted instance. It is
// always a tags-only write: Tags is every key it sets, and it changes
// nothing else on the object (Q3 of the ruling on #2284).
type MarkerWrite struct {
	Addr     addrs.AbsResourceInstance
	TypeName string
	Node     string
	ImportID string
	Tags     map[string]string
}

// AdoptRatification is [Ratification] over the selections that passed every
// refusal, plus the ones that did not. Approve is the embedded
// Ratification's and is unchanged.
type AdoptRatification struct {
	*Ratification

	Index    adoption.IndexRef
	Refused  []AdoptRefusal
	Imported map[string]AdoptImport
}

// RatifyAdoption reads every selection of req.Adoption live and ratifies
// what it may adopt. Like [Ratify] it writes nothing: its provider calls are
// GetProviderSchema, ImportResourceState and ReadResource.
func RatifyAdoption(ctx context.Context, req AdoptRequest) (*AdoptRatification, tfdiags.Diagnostics) {
	ctx, span := tracing.Tracer().Start(ctx, "live-adopt.ratify-adoption",
		tracing.SpanAttributes(traceattrs.String("live.estate", req.Estate)),
	)
	defer span.End()

	var diags tfdiags.Diagnostics
	switch {
	case req.Adoption == nil || req.Adoption.File == nil:
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No adoption file",
			"live-import -adopt needs an adoption file resolved against its index version, and none was given."))
	case req.Providers == nil:
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No provider access",
			"Reading an indexed object from the live system needs a configured provider, and none was given."))
	case !discovery.ValidEstateName(req.Estate):
		// ratify says this too; said here first so no provider is called
		// for an estate whose marker could never be written.
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid estate name",
			fmt.Sprintf("An adoption needs the estate's name, matching the tofu-estate marker grammar in live/MARKERS.md. Got %q.", req.Estate)))
	}
	roster := req.Roster
	if roster == nil {
		r, err := registry.Embedded()
		if err != nil {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No registry roster", err.Error()))
		}
		roster = r
	}

	state := states.NewState()
	out := &AdoptRatification{Index: req.Adoption.File.Index, Imported: map[string]AdoptImport{}}
	for _, e := range req.Adoption.Entries {
		imp, refusal := adoptOne(ctx, req, roster, e, state)
		if refusal != nil {
			out.Refused = append(out.Refused, *refusal)
			continue
		}
		out.Imported[imp.addr.String()] = AdoptImport{Node: e.Node.ID, ImportID: imp.importID}
	}

	inner := req.Request
	inner.State = state
	rat, ratDiags := ratify(ctx, inner)
	diags = diags.Append(ratDiags)
	if rat == nil {
		return nil, diags
	}
	// The in-memory state carries no root outputs: there is no prior
	// estate whose outputs a migration would carry over.
	rat.rootOutputs = nil
	out.Ratification = rat
	span.SetAttributes(
		traceattrs.Int64("live.entries", int64(len(rat.Entries))),
		traceattrs.Int64("live.refusals", int64(len(out.Refused))),
	)
	return out, diags
}

// MarkerWrites is every write -approve would make for the adopted instances
// Ratify made eligible, sorted by address. Each is tags only.
func (a *AdoptRatification) MarkerWrites() []MarkerWrite {
	if a == nil || a.Ratification == nil {
		return nil
	}
	var out []MarkerWrite
	for _, entry := range a.Entries {
		e, ok := a.eligible[entry.Addr.String()]
		if !ok || (entry.Status != StatusVerified && entry.Status != StatusDrifted) {
			continue
		}
		tags := map[string]string{discovery.TagEstate: a.Estate}
		if !e.labelled && !e.manifested && !e.fieldGranular {
			for i, chunk := range discovery.SplitAddress(discovery.EscapeAddress(entry.Addr.String())) {
				tags[discovery.AddressTagKey(i)] = chunk
			}
		}
		imp := a.Imported[entry.Addr.String()]
		out = append(out, MarkerWrite{
			Addr:     entry.Addr,
			TypeName: entry.TypeName,
			Node:     imp.Node,
			ImportID: imp.ImportID,
			Tags:     tags,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}

type adoptedInstance struct {
	addr     addrs.AbsResourceInstance
	importID string
}

// adoptOne settles one selection's type and import id, reads it live, and
// writes what the read returned into state; or says why it will not.
func adoptOne(ctx context.Context, req AdoptRequest, roster *registry.Roster, e adoption.Entry, state *states.State) (adoptedInstance, *AdoptRefusal) {
	node := e.Node
	ref := &AdoptRefusal{Node: node.ID, As: e.As, ListedType: node.ListedType}
	refuse := func(format string, args ...any) (adoptedInstance, *AdoptRefusal) {
		ref.Reason = fmt.Sprintf(format, args...)
		return adoptedInstance{}, ref
	}

	addr, addrDiags := addrs.ParseAbsResourceInstanceStr(e.As)
	if addrDiags.HasErrors() {
		return refuse("%q is not a resource instance address: %s", e.As, addrDiags.Err())
	}
	if addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
		return refuse("%s is not a managed resource address; only a managed resource can be adopted.", e.As)
	}

	sub, scopeID := nodeSubstrate(node.ID)
	var tfType, cfnType string
	switch sub {
	case "aws":
		cfnType = node.ListedType
		t, err := e.Selection.EngineType(roster.TFTypesForCFNType(cfnType))
		if err != nil {
			return refuse("%s.", err)
		}
		tfType = t
	case "azure", "gcp", "kubernetes":
		if e.Type == "" {
			return refuse("choudoufu has no join from the %s inventory type %s to a Terraform type, so the selection must name one as its type.", sub, node.ListedType)
		}
		tfType = e.Type
	default:
		return refuse("The node's id names substrate %q, which choudoufu does not adopt from (aws, azure, gcp, kubernetes).", sub)
	}
	ref.TypeName = tfType

	if got := addr.Resource.Resource.Type; got != tfType {
		return refuse("The selection adopts it as %s, a %s, but its type is %s.", e.As, got, tfType)
	}
	if pkgidentity.NotImportable(tfType) {
		return refuse("%s cannot be imported by its provider, so it cannot be adopted from an index.", tfType)
	}
	if cfnType != "" {
		if createOnly, _ := roster.TagsCreateOnly(cfnType); createOnly {
			return refuse("%s (%s) takes tags only when it is created, so writing this estate's markers onto the existing object would replace it. Adoption never replaces an object to mark it; nothing was read or written.", cfnType, tfType)
		}
	}

	importID, why := adoptImportID(sub, scopeID, tfType, cfnType, node, roster)
	if why != "" {
		return refuse("%s", why)
	}
	ref.ImportID = importID

	providerAddr := configProviderAddr(req.Config, tfType, "")
	provider, err := req.Providers.ConfiguredProvider(ctx, providerAddr)
	if err != nil {
		return refuse("Provider %s could not be used, so the object could not be read: %s.", providerAddr, err)
	}
	schema, err := resourceSchema(ctx, provider, tfType)
	if err != nil {
		return refuse("%s.", err)
	}

	imp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: tfType,
		Target:   providers.ImportTarget{ID: importID},
	})
	if imp.Diagnostics.HasErrors() {
		return refuse("The provider could not import %s by id %q: %s.", tfType, importID, imp.Diagnostics.Err())
	}
	var imported *providers.ImportedResource
	for i := range imp.ImportedResources {
		if imp.ImportedResources[i].TypeName == tfType {
			imported = &imp.ImportedResources[i]
			break
		}
	}
	if imported == nil {
		return refuse("The provider's import of %q returned no %s object.", importID, tfType)
	}

	read := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:      tfType,
		PriorState:    imported.State,
		Private:       imported.Private,
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: imported.Identity,
	})
	if read.Diagnostics.HasErrors() {
		return refuse("The provider failed while reading %q from the live system: %s.", importID, read.Diagnostics.Err())
	}
	if read.NewState == cty.NilVal || read.NewState.IsNull() {
		return refuse("The live system reports that %q no longer exists. The index version is older than the object's deletion.", importID)
	}
	live, _ := read.NewState.UnmarkDeep()

	// The live read decides: what the index recorded about this object's
	// tags is never consulted here.
	if surface, ok := substrate.SurfaceOf(providerAddr.Provider.Type, schema.Block); ok {
		if tags, ok := substrate.MarkersOf(surface, live); ok {
			if why := foreignOwner(tags, req.Estate, addr); why != "" {
				return refuse("%s Nothing was written.", why)
			}
		}
	}

	identity := read.NewIdentity
	if identity == cty.NilVal {
		identity = imported.Identity
	}
	obj := &states.ResourceInstanceObject{
		Value:    live,
		Private:  read.Private,
		Status:   states.ObjectReady,
		Identity: identity,
	}
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
	if err != nil {
		return refuse("The object the provider read does not fit its own schema for %s: %s.", tfType, err)
	}
	mod := state.EnsureModule(addr.Module)
	mod.SetResourceInstanceCurrent(addr.Resource, src, providerAddr, addrs.NoKey)
	return adoptedInstance{addr: addr, importID: importID}, nil
}

// nodeSubstrate is the substrate and scope id a listed node's id starts
// with: <substrate>/<scopeId>/<location>/<listedType>/<nativeId>, the first
// four parts with "%" and "/" escaped (behold src/cloud-lister/ids.ts).
func nodeSubstrate(id string) (sub, scopeID string) {
	parts := strings.SplitN(id, "/", 3)
	unescape := func(s string) string {
		s = strings.ReplaceAll(s, "%2F", "/")
		s = strings.ReplaceAll(s, "%2f", "/")
		return strings.ReplaceAll(s, "%25", "%")
	}
	if len(parts) > 0 {
		sub = unescape(parts[0])
	}
	if len(parts) > 1 {
		scopeID = unescape(parts[1])
	}
	return sub, scopeID
}

// adoptImportID is the Terraform import id of node, or why there is none.
func adoptImportID(sub, scopeID, tfType, cfnType string, node *adoption.Node, roster *registry.Roster) (string, string) {
	native := node.NativeID
	if native == "" {
		return "", fmt.Sprintf("The index node %s carries no native id to import it by.", node.ID)
	}
	switch sub {
	case "aws":
		tr, reason := pkgidentity.CloudControlTranslation(tfType, cfnType, roster.PrimaryIdentifier(cfnType))
		if tr == nil {
			return "", fmt.Sprintf("There is no translation from %s's Cloud Control identifier to %s's import id: %s.", cfnType, tfType, reason)
		}
		if tr.NeedsAccount() {
			tr = tr.WithAccount(scopeID)
		}
		if tr.WantsModel(native, node.Attrs) {
			return "", fmt.Sprintf("%s's import id needs the %s property of the object's Cloud Control model, which the index node does not carry.", tfType, tr.ModelProperty())
		}
		id, ok := tr.Translate(native, node.Attrs)
		if !ok {
			return "", fmt.Sprintf("The Cloud Control identifier %q does not translate to %s.", native, tr.Describe())
		}
		return id, ""
	case "gcp":
		id, ok := pkgidentity.GoogleAssetImportID(tfType, native)
		if !ok {
			return "", fmt.Sprintf("The Asset name %q does not give a %s import id: the type has no Asset row, or the name has another service or shape.", native, tfType)
		}
		return id, ""
	case "azure":
		// The ARM id as the inventory returned it, never a lowercased copy.
		return native, ""
	case "kubernetes":
		if tfType == "kubernetes_manifest" {
			apiVersion, kind := splitKubernetesListedType(node.ListedType)
			ns, name, found := strings.Cut(native, "/")
			if !found {
				ns, name = "", native
			}
			return kubesweep.ManifestImportID(apiVersion, kind, ns, name), ""
		}
		return native, ""
	}
	return "", fmt.Sprintf("choudoufu does not adopt from substrate %q.", sub)
}

// splitKubernetesListedType splits a group/version/kind listed type into its
// apiVersion and kind: "apps/v1/Deployment" to "apps/v1" and "Deployment",
// "v1/ConfigMap" to "v1" and "ConfigMap".
func splitKubernetesListedType(listed string) (apiVersion, kind string) {
	i := strings.LastIndex(listed, "/")
	if i < 0 {
		return "", listed
	}
	return listed[:i], listed[i+1:]
}

// chantMarkerKeySets are chant's managed-by and stack keys per surface, the
// sets behold's lister reads (src/cloud-lister/markers.ts).
var chantMarkerKeySets = [][2]string{
	{"chant:managed-by", "chant:stack"},
	{"chant-managed-by", "chant-stack"},
	{"managed-by", "chant-stack"},
	{"app.kubernetes.io/managed-by", "chant.intentius.io/stack"},
}

// foreignOwner is why the live object's markers forbid adopting it into
// estate as addr, or "". Keys are matched without regard to case, because
// Azure tag names are case-insensitive.
func foreignOwner(tags map[string]string, estate string, addr addrs.AbsResourceInstance) string {
	lower := make(map[string]string, len(tags))
	for k, v := range tags {
		lower[strings.ToLower(k)] = v
	}
	if got := lower[discovery.TagEstate]; got != "" && got != estate {
		return fmt.Sprintf("The live object carries tofu-estate = %q: another estate owns it, and adoption never takes another estate's resource.", got)
	}
	if got, ok := lower["plunge-stack"]; ok {
		return fmt.Sprintf("The live object carries plunge-stack = %q: a plunge stack owns it.", got)
	}
	for _, keys := range chantMarkerKeySets {
		if lower[keys[0]] == "chant" {
			return fmt.Sprintf("The live object carries chant's marker (%s = chant, stack %q): a chant stack owns it.", keys[0], lower[keys[1]])
		}
	}
	if lower[discovery.TagEstate] == estate {
		raw, corrupt := discovery.GatherAddress(lower)
		switch {
		case corrupt:
			return "The live object carries this estate's tofu-address with a gap in its continuation tags, so this run cannot tell which address it names."
		case raw != "" && !discovery.AddressMatches(discovery.EscapeAddress(raw), addr.String()):
			return fmt.Sprintf("The live object already carries this estate's tofu-address = %q. Adopting it as %s would rename it, which is live-mv's job.", raw, addr)
		}
	}
	return ""
}
