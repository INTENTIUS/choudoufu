// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/sweepconfig"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/pkg/azsweep"
)

// Azure is the Azure Resource Manager family (GitHub issue #1120, ruled on
// #1122 2026-10-19): hashicorp/azurerm and azure/azapi as one family, the
// AWS marker keys in the tags argument both providers already accept,
// written lowercase and read case-insensitively ([markers.SurfaceARMTags]).
//
// Several answers below are facts about Azure's API rather than about this
// fork's design: [azure.MarkerOutlivesObject], [azure.CreateCollidesOnKey],
// [azure.SweepFindsUnadmitted] and [azure.PostCreateNeeded]. In this build
// round they come from Azure's documentation and the two providers' source
// at the pinned versions (azurerm 5.10.0, azapi 2.13.0), and each says so;
// they stand until the maintainer measures them on floci-az
// (live/ADDING-A-PLATFORM.md, requirement 2).
var Azure Substrate = azure{}

type azure struct{}

func (azure) Name() string { return "azurerm" }

// ProviderTypes: both providers serve ARM resources and one object can be
// declared through either, so they are one family (#1122).
func (azure) ProviderTypes() []string { return []string{"azurerm", "azapi"} }

func (azure) Surfaces() []markers.Surface { return []markers.Surface{markers.SurfaceARMTags} }

func (azure) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if markers.ARMTaggable(block) {
		return markers.SurfaceARMTags, true
	}
	return "", false
}

// MarkersOf reads the top-level tags with marker keys case-folded
// ([markers.ARMTagsOf]); azapi copies a body.tags write back to the
// top-level attribute, so it is the one read for both write paths.
func (azure) MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	if surface == markers.SurfaceARMTags {
		return markers.ARMTagsOf(obj)
	}
	return nil, false
}

// Writes: the tags map rides the create call (azurerm's tags argument, or
// azapi's top-level tags or body.tags), and an existing object is marked by
// a plan-then-apply confined to the tags, as on AWS. PostCreate is ARM's
// Tags API ([WriteARMTagsAPI]), for the azurerm types whose create call
// cannot carry tags ([azure.PostCreateNeeded]).
func (azure) Writes(surface markers.Surface) Writes {
	if surface == markers.SurfaceARMTags {
		return Writes{Create: WriteInCreate, Adopt: WriteTagsPlan, PostCreate: WriteARMTagsAPI}
	}
	return Writes{}
}

// WriteARMTagsAPI is the Azure family's post-create write (#1120 unit 12):
// ARM's Tags API, a merge of the marker tags onto the object's tag scope
// ([ARMTagScope]):
//
//	PATCH {resourceManager}{scope}/providers/Microsoft.Resources/tags/default?api-version=2021-04-01
//	{"operation": "Merge", "properties": {"tags": {...}}}
//
// Merge adds and replaces the named tags and leaves every other tag on the
// object as it is (Azure REST API, Tags - Update At Scope). It is the call
// az tag update --operation Merge makes, and the one azurerm itself uses to
// tag a subscription (tagsClient.CreateOrUpdateAtScope).
const WriteARMTagsAPI Write = "arm-tags-api"

// CarriesAddress: the tofu-address tag, with continuations and tofu-slot,
// exactly as on AWS (#1122's capability row).
func (azure) CarriesAddress() bool { return true }

// AddressInMarkers: the address is a key of the tag map.
func (azure) AddressInMarkers() bool { return true }

// AddressCarrier: the tofu-address tag, inside the tag map.
func (azure) AddressCarrier(surface markers.Surface) (key, noun string) {
	if surface == markers.SurfaceARMTags {
		return markers.TagAddress, "tag"
	}
	return "", ""
}

// SweepResourceGraph is the Azure sweep (#1122): one Azure Resource Graph
// query per subscription the estate's resources resolve to, matching the
// marker case-insensitively.
const SweepResourceGraph Sweep = "resource-graph"

// SweepDataPlane is the Azure data-plane listing (GitHub issue #2205): the
// objects Resource Graph does not index (Key Vault keys, secrets and
// certificates), listed from the vault's own endpoint. It runs beside
// [SweepResourceGraph], never instead of it.
const SweepDataPlane Sweep = "data-plane"

func (azure) Sweep() Sweep { return SweepResourceGraph }

// SweepFindsUnadmitted is true, from the sweep's design rather than a
// measurement: a Resource Graph query over the Resources table returns every
// ARM resource in the subscription whose tags match, whatever its type, and
// its universe is the service's index, never internal/live/identity's
// admission table. A type with no row is therefore still found once its last
// block is removed, as on Kubernetes. Taken from Azure Resource Graph's
// documentation (the Resources table and its tags column); it awaits
// measurement on floci-az, which has to serve the same query.
func (azure) SweepFindsUnadmitted() bool { return true }

// MarkerOutlivesObject is true: Azure Resource Graph is an index refreshed
// from ARM change notifications, and its documentation describes deletes
// reaching it after a lag, during which a deleted resource and its tags are
// still listed. A record tombstone is how the estate tells that lingering
// marker from a second claimant, as on AWS. From Azure's docs; it awaits
// measurement on floci-az.
func (azure) MarkerOutlivesObject() bool { return true }

// Unrecorded is [UnrecordedParentOwned], the answer every family gave
// before the 2026-10-20 ruling, so the Azure lane's measured behaviour is
// unchanged by the merge that brought the ruling to it. The markerless
// types the lane exercises are role assignments and associations whose
// ARM ids are built from a tagged parent's id plus declared arguments
// (live/READINESS.md, Azure), the shape [aws]'s answer covers; whether a
// colliding ARM PUT at such an id calls for [UnrecordedNameHeld] instead
// is a ruling still to make for this family.
func (azure) Unrecorded(string) Unrecorded { return UnrecordedParentOwned }

// NewSweeper is the Resource Graph client the provider block's own
// connection arguments build ([AzureSweepAttrs], with the provider's
// environment fallbacks applied by azsweep.New), so the sweep calls as the
// provider does and covers the subscription the block names (#1120 unit
// 11). On an error the client is a nil [Sweeper], never a
// [ResourceGraphSweeper] holding a nil client.
func (azure) NewSweeper(providerConfig cty.Value, ok bool) (Sweeper, error) {
	client, err := sweepconfig.AzureSweep(context.Background(), AzureSweepAttrs(providerConfig, ok))
	if err != nil {
		return nil, err
	}
	return ResourceGraphSweeper{Client: client}, nil
}

// ResourceGraphSweeper is the Azure family's client: the Resource Graph
// client built from one azurerm or azapi provider block, whose methods it
// carries (azsweep.Searcher, and Subscription, the subscription the block
// names). internal/command's leg for [SweepResourceGraph] asks for those
// capabilities, never for this type.
type ResourceGraphSweeper struct {
	*azsweep.Client
}

// SweepKind is [SweepResourceGraph].
func (ResourceGraphSweeper) SweepKind() Sweep { return SweepResourceGraph }

// AzureSweepAttrs reads the connection arguments the Resource Graph sweep
// understands off an evaluated azurerm or azapi provider block
// (azsweep.ProviderAttrs), before the environment fallbacks. Which of the
// two providers the block configures is read off its shape, since
// [Substrate.NewSweeper] is handed the value alone: azurerm's provider
// schema has a features block and azapi's has none, and azapi's has an
// endpoint block and default_tags, which azurerm's has not (both providers'
// schemas at the pinned versions, azurerm 5.10.0 and azapi 2.13.0). A
// value of neither shape, or none at all, reads as azurerm, the family's
// name.
func AzureSweepAttrs(val cty.Value, ok bool) azsweep.Attrs {
	return azsweep.ProviderAttrs(azureProviderOf(val, ok), val, ok)
}

func azureProviderOf(val cty.Value, ok bool) string {
	if !ok || val == cty.NilVal || !val.Type().IsObjectType() {
		return azsweep.ProviderAzureRM
	}
	ty := val.Type()
	if !ty.HasAttribute("features") && (ty.HasAttribute("endpoint") || ty.HasAttribute("default_tags")) {
		return azsweep.ProviderAzAPI
	}
	return azsweep.ProviderAzureRM
}

// ---- GitHub issue #1584 ----

// CreateCollidesOnKey is false: an ARM create is a PUT, and a PUT at a name
// that already exists updates the object there instead of answering 409
// (Azure Resource Manager's REST guidance; azurerm's own requiresImport
// check is a read before the PUT, not a server conflict). A declared
// natural-key object that already exists without this estate's marker is
// refused by name before any create through the DeclaredUntagged path
// (internal/live/policy), never left to the PUT (#1122). From Azure's docs
// and the provider source; it awaits measurement on floci-az.
func (azure) CreateCollidesOnKey(markers.Surface) bool { return false }

// CreateOverwritesAtKey is true on the arm-tags surface: the same PUT that
// never collides updates whatever object is at its id, so a declared
// object found there without this estate's marker is refused by name
// before any create (#1122; internal/live/projection's
// SummaryOverwriteAtKey). From ARM's REST guidance (PUT is
// create-or-update) and azapi's CreateUpdate, which sends one PUT for both
// (internal/services/azapi_resource.go at 2.13.0); it awaits measurement
// on floci-az.
//
// This answers what ARM does, not what the provider in front of it does
// (GitHub issue #2133, finding 5). Both providers read the id before the
// PUT and fail the create when an object is there: azapi_resource always
// (azapi 2.13.0 internal/services/azapi_resource.go:873-893, no switch),
// azurerm for most types unless its features block sets
// skip_import_check_on_create_and_allow_overwriting_existing_resources
// (azurerm 5.10.0 internal/provider/features.go:26-31, 544-546; e.g.
// internal/services/resource/resource_group_resource.go:76-87), and not
// for a few types whose create does no read. The refusal stays true for
// both providers on purpose: it is a plan-time stop, before anything is
// applied, where the providers' is an apply-time error after whatever
// was ordered before the resource has been created; and under azurerm it
// is the only guard where the flag is set or the type skips the read.
// internal/live/projection's overwriteProviderGuard puts the per-provider
// answer in the refusal itself.
func (azure) CreateOverwritesAtKey(surface markers.Surface) bool {
	return surface == markers.SurfaceARMTags
}

func (azure) CarrierPhrase(surface markers.Surface) string {
	if surface == markers.SurfaceARMTags {
		return "tags attribute"
	}
	return ""
}

// NotACarrier: an Azure type with no settable top-level tags map. It never
// borrows the AWS sentence, which describes tag-vocabulary refusals that do
// not exist on ARM.
func (azure) NotACarrier(_ *configschema.Block, typeName string) string {
	return fmt.Sprintf("%s has no tags map this configuration can set, so there is nowhere on it to carry an ownership marker.", typeName)
}

// ---- GitHub issue #1587 ----

// MarkerWriter is [WriteARMTagsAPI] for every azurerm and azapi provider
// configuration: internal/command builds the Tags API client with the
// credential chain the Resource Graph sweep uses (pkg/azsweep).
func (azure) MarkerWriter() Write { return WriteARMTagsAPI }

// ---- GitHub issue #1642 ----

// PostCreateNeeded is #1122's ruling, answered from the generated azurerm
// tag facts (internal/live/armtypes, azurerm_tags_generated.json, read off
// azurerm's Create functions at the pin): true for an azurerm type whose
// create call does not carry tags and whose Create writes them in a later
// call (after-create; two types at azurerm 5.10.0), and for a type the
// analysis could not classify (unknown; none at 5.10.0), the conservative
// answer the facts ask for. False for every in-create type, for azapi,
// whose create PUT carries the tags it is given (internal/live/projection's
// armTagTarget), and for a type with no ARM tags map. The facts are
// embedded, so the run's [Facts] carries no Azure entry.
func (azure) PostCreateNeeded(surface markers.Surface, created Created, _ Facts) (string, bool) {
	if surface != markers.SurfaceARMTags {
		return "", false
	}
	facts := armtypes.DefaultRMTags()
	row, ok := facts.Row(created.Type())
	if !ok {
		return "", false
	}
	source := fmt.Sprintf("internal/live/armtypes/%s, azurerm %s", armtypes.RMTagsPath, facts.Version)
	switch row.Create {
	case armtypes.CreateAfterCreate:
		return fmt.Sprintf("%s does not send tags in its create call (%s) and writes them afterwards with %s (%s)", row.Type, row.CreateCall, row.Call, source), true
	case armtypes.CreateUnknown:
		return fmt.Sprintf("this build could not tell whether %s sends tags in its create call (%s: %s)", row.Type, source, row.Reason), true
	}
	return "", false
}

// ARMTagScope is the ARM id the Tags API write addresses for an object the
// provider returned: its id, the ARM resource id in both providers. One
// object is the exception, by rule rather than by type: an id outside any
// subscription (a tenant-level "/providers/..." path) names no taggable
// resource, and ARM writes tags only on a resource, a resource group or a
// subscription. Such an object that carries a subscription_id is tagged on
// that subscription, which is what azurerm_subscription does with its own
// tags: its id is the Microsoft.Subscription alias, and its Create tags
// commonids.NewSubscriptionID(subscriptionId) (subscription_resource.go,
// azurerm 5.10.0). Empty when the object names neither.
func ARMTagScope(obj cty.Value) string {
	id := ObjectString(obj, "id")
	if id == "" {
		return ""
	}
	lower := strings.ToLower(id)
	if strings.HasPrefix(lower, "/subscriptions/") {
		return id
	}
	if strings.HasPrefix(lower, "/providers/") {
		if sub := ObjectString(obj, "subscription_id"); sub != "" {
			return "/subscriptions/" + sub
		}
	}
	return ""
}

// ---- GitHub issue #1653 ----

// ManualMarkFix is the Azure CLI's merge of the marker tags onto the
// object's tag scope ([ARMTagScope]), the same Merge the post-create write
// makes; the generic sentence when the object names no scope.
func (azure) ManualMarkFix(created Created, want map[string]string, _ Facts) string {
	id := ARMTagScope(created.Object)
	if id == "" {
		return genericMarkFix(want)
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, "'"+k+"="+want[k]+"'")
	}
	return fmt.Sprintf("Mark it, then plan again:\n\n  az tag update --resource-id %s --operation Merge --tags %s", id, strings.Join(pairs, " "))
}

// ---- GitHub issue #1708 ----

// CreatedObject is the object's ARM id.
func (azure) CreatedObject(created Created) string {
	return idPhrase(created.Object)
}

// ---- GitHub issue #1649 ----

// CarrierPaths: the top-level tags map, the one [markers.ARMTagsOf] reads.
func (azure) CarrierPaths(surface markers.Surface) []cty.Path {
	if surface == markers.SurfaceARMTags {
		return []cty.Path{cty.GetAttrPath("tags")}
	}
	return nil
}

func (azure) MarkerNoun(surface markers.Surface) string {
	if surface == markers.SurfaceARMTags {
		return "tag"
	}
	return ""
}

// ---- GitHub issue #1706: controller-held ----

// AKSManagedTagPrefix is the prefix of the tags AKS writes on its node
// resource group and the resources in it. The AKS FAQ ("Can I modify tags
// and other properties of the AKS resources in the node resource group?",
// learn.microsoft.com/azure/aks/faq, read 2026-10-19) names the aks-managed
// tags as Azure-created and not to be modified. Neither provider's source
// writes or names them.
const AKSManagedTagPrefix = "aks-managed-"

// LifecycleOwnerTypes are the ARM resource types whose id in an object's
// managedBy means that resource owns the object's lifecycle: it created the
// object and deletes it with itself, so the object is held, never swept
// (#1122, #2133 finding 2). Lowercased; ARM compares types
// case-insensitively.
//
//   - Microsoft.ContainerService/managedClusters on its node resource
//     group. The AKS FAQ ("Why are two resource groups created with AKS?",
//     learn.microsoft.com/azure/aks/faq, read 2026-10-20): "The AKS resource
//     provider automatically creates the second resource group during
//     deployment ... AKS automatically deletes the node resource group
//     whenever you delete the cluster."
//   - Microsoft.Solutions/applications on its managed resource group. The
//     Managed Applications overview ("Managed resource group",
//     learn.microsoft.com/azure/azure-resource-manager/managed-applications/overview,
//     read 2026-10-20): "When the customer deletes the managed application,
//     the managed resource group is also deleted."
//
// A resource group's managedBy is "The ID of the resource that manages this
// resource group" (Resource Groups - Get, ResourceGroup definition,
// learn.microsoft.com/rest/api/resources/resource-groups/get, api-version
// 2021-04-01). Neither page prints the managedBy value of a node or
// managed resource group; that is the maintainer's to measure.
//
// Left out because no Microsoft page found on 2026-10-20 states their
// lifecycle tie, though azurerm creates each through a
// managed_resource_group_name argument: Microsoft.Databricks/workspaces,
// Microsoft.Synapse/workspaces, Microsoft.RedHatOpenShift/openShiftClusters,
// Microsoft.Purview/accounts and Microsoft.Workloads/sapVirtualInstances. An
// owner missing here costs a held group being read as an ordinary one, which
// matters only if it carries this estate's marker.
var LifecycleOwnerTypes = map[string]bool{
	"microsoft.containerservice/managedclusters": true,
	"microsoft.solutions/applications":           true,
}

// ARMIDType is the ARM resource type an ARM id names: the namespace after
// its last /providers/ segment and every type segment after it, with the
// names dropped ("Microsoft.ContainerService/managedClusters" for a
// cluster's id, "Microsoft.Compute/virtualMachineScaleSets/virtualMachines"
// for a scale set instance's). Matched case-insensitively and returned as
// spelled. False when the id names no provider namespace, or the segments
// after it do not pair into type and name.
func ARMIDType(id string) (string, bool) {
	segs := strings.Split(strings.Trim(id, "/"), "/")
	at := -1
	for i, s := range segs {
		if strings.EqualFold(s, "providers") {
			at = i
		}
	}
	if at < 0 || at+1 >= len(segs) {
		return "", false
	}
	rest := segs[at+2:]
	if len(rest) == 0 || len(rest)%2 != 0 {
		return "", false
	}
	parts := []string{segs[at+1]}
	for i := 0; i < len(rest); i += 2 {
		parts = append(parts, rest[i])
	}
	return strings.Join(parts, "/"), true
}

// ControllerHeld reads two fixed things off an ARM object (#1122):
//
//   - its managedBy property, when it names a resource of a type in
//     [LifecycleOwnerTypes]: an AKS cluster on its node resource group, a
//     managed application on its managed resource group. Any other managedBy
//     is not a hold. ARM sets managedBy on every attached managed disk to
//     the id of its virtual machine ("A relative URI containing the ID of
//     the VM that has the disk attached", Disks - Get, Disk definition,
//     learn.microsoft.com/rest/api/compute/disks/get, read 2026-10-20;
//     azurerm's managed_disk_resource.go parses it as a virtual machine id),
//     and an attachment is not ownership: the estate declares that disk and
//     removing its block must still propose its removal (#2133 finding 2).
//     azurerm_resource_group's own managed_by argument lets a configuration
//     set the property to anything, which is one more reason the type, not
//     the presence, decides.
//   - an aks-managed- tag ([AKSManagedTagPrefix]), compared
//     case-insensitively as every ARM tag key is.
//
// Virtual machine scale sets (#2133 finding 11). "Use Azure tags in AKS"
// (learn.microsoft.com/azure/aks/use-tags, read 2026-10-20) says tags set
// on a cluster go to the cluster's related resources and its node resource
// group "but not the node pools", and tags set on a node pool go to "The
// virtual machine scale set and each virtual machine scale set instance
// associated with the node pool". So a stamped
// azurerm_kubernetes_cluster_node_pool puts its marker on its scale set and
// on every instance. The instances need no rule: Resource Graph keeps
// microsoft.compute/virtualmachinescalesets/virtualmachines in its
// computeresources table, not resources (Supported tables,
// learn.microsoft.com/azure/governance/resource-graph/reference/supported-tables-resources,
// read 2026-10-20), and the sweep queries resources and resource groups
// alone ([azsweep.Query]), so it never lists one. The scale set itself is
// in resources and has no managedBy. It is held when it carries an
// aks-managed- tag; no Microsoft page found on 2026-10-20 lists which
// aks-managed- tags AKS writes on a node pool's scale set, only that
// aks-managed tags exist on resources in the node resource group (AKS FAQ),
// so no rule is added on the docs' evidence and the maintainer measures it.
// The same holds for a "Virtual Machines" node pool's standalone virtual
// machines, which are in resources.
func (azure) ControllerHeld(ev HoldEvidence) (Hold, bool) {
	if ev.ManagedBy != "" {
		if t, ok := ARMIDType(ev.ManagedBy); ok && LifecycleOwnerTypes[strings.ToLower(t)] {
			return Hold{Controller: "ARM managedBy", HeldBy: "the ARM resource " + ev.ManagedBy + " (managedBy)"}, true
		}
	}
	var aks []string
	for k, v := range ev.ARMTags {
		if strings.HasPrefix(strings.ToLower(k), AKSManagedTagPrefix) {
			aks = append(aks, k+"="+v)
		}
	}
	if len(aks) == 0 {
		return Hold{}, false
	}
	sort.Strings(aks)
	return Hold{Controller: "AKS", HeldBy: "AKS node resource group (" + strings.Join(aks, ", ") + ")"}, true
}

func (azure) HoldRecognition() HoldRecognition {
	return HoldRecognition{
		Mechanism: "the object's ARM managedBy property when it names an AKS managed cluster or a managed application (the lifecycle owners of a node or managed resource group; an attached disk's managedBy is its VM and is not a hold), or a tag AKS writes on its node resource group (aks-managed- prefix, case-insensitive; AKS FAQ)",
		Keys:      []string{"managedBy", AKSManagedTagPrefix + "*"},
	}
}

// ---- GitHub issue #1586: admission ----

// SynthesizeIdentity is the Azure family's identity rule (#1122, #1120
// unit 10). Every identity it builds is an ARM resource id, which ARM
// compares case-insensitively, so each answer sets
// [SynthesizedIdentity.ARMResourceID].
//
//   - azurerm: the ARM id template generated from azurerm's own id parsers
//     (live/azurerm-ids.json, embedded), or for the rest the identity-schema
//     route told that subscription_id is the provider's; see
//     [azurermAdmits]. The subscription is the provider configuration's
//     subscription_id, the way an AWS region is the provider
//     configuration's region.
//   - azapi_resource: type + parent_id + name, the natural key inside the
//     resource's own arguments ([azapiResourceIdentity]).
//
// A type of neither answers false and reaches the identity-schema route
// exactly as it did before the family existed.
func (azure) SynthesizeIdentity(typeName string, schema providers.Schema) (SynthesizedIdentity, bool) {
	if strings.HasPrefix(typeName, "azapi_") {
		if AzapiResourceShape(schema.Block) {
			return azapiResourceIdentity(), true
		}
		return SynthesizedIdentity{}, false
	}
	if strings.HasPrefix(typeName, "azurerm_") {
		synth, ok := azurermAdmits(typeName, schema)
		if ok {
			markProviderMintedIfAbsent(synth.Components, schema.Block)
		}
		return synth, ok
	}
	return SynthesizedIdentity{}, false
}

// AzapiResourceShape reports whether block is azapi_resource's shape: a
// required string type, settable string parent_id and name, a settable tags
// map ([markers.ARMTaggable]), and no resource_id. Read from the schema
// rather than the type name alone, because azapi serves three more types
// carrying type and a parent or name (azapi 2.13.0): azapi_update_resource
// and azapi_resource_action act on an object they do not create, named by
// resource_id, and azapi_data_plane_resource is a data-plane object with no
// tags and no ARM id. None of them owns the object its arguments name, so
// none may be identified by it.
func AzapiResourceShape(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	typ, ok := block.Attributes["type"]
	if !ok || typ == nil || typ.Type != cty.String || !typ.Required {
		return false
	}
	for _, name := range []string{"parent_id", "name"} {
		a, ok := block.Attributes[name]
		if !ok || a == nil || a.Type != cty.String || (!a.Required && !a.Optional) {
			return false
		}
	}
	if _, has := block.Attributes["resource_id"]; has {
		return false
	}
	return markers.ARMTaggable(block)
}

// AzapiImportSyntax documents azapi_resource's identity: the ARM id azapi
// builds from its three arguments (internal/services/parse/resource.go's
// newResourceID at azapi 2.13.0), which is also its import id.
const AzapiImportSyntax = "PARENT_ID/providers/NAMESPACE/TYPE/NAME, or PARENT_ID/CHILDTYPE/NAME for a child type"

// azapiResourceIdentity is azapi_resource's natural key, read from its own
// arguments the way kubernetes_manifest's is read from its manifest
// (#1079): parent_id, then the segment azapi joins parent_id and name with,
// derived from type, then name. The middle component is
// [IdentityComponent.ARMTypeSegment]; internal/live/identity's
// armTypeSegment is the rule, cited line by line.
func azapiResourceIdentity() SynthesizedIdentity {
	return SynthesizedIdentity{
		NonAWSProvider: true,
		Components: []IdentityComponent{
			{Attrs: []string{"parent_id"}},
			{Attrs: []string{"type"}, ARMTypeSegment: true},
			{Attrs: []string{"name"}},
		},
		ImportSyntax: AzapiImportSyntax,
		// azapi_resource's id is the ARM id it built (parse.ResourceId.ID),
		// so a reference to .id is a reference to this identity.
		IdentityAttrs: []string{"id"},
		ARMResourceID: true,
	}
}
