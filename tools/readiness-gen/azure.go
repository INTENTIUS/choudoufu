// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// The Azure section (GitHub issue #2119, the Azure column; #1120 unit 14):
// every hashicorp/azurerm 5.10.0 resource type and every ARM resource type
// azure/azapi 2.13.0 carries type data for, each in exactly one tier, from
// evidence already committed for the Azure family:
//
//   - taggability: azurerm's from internal/live/armtypes' azurerm tags
//     table (the types whose schema `tags` is the ARM tags map; the three
//     whose `tags` is not are untaggable here, the way the stamp treats
//     them), azapi's per ARM type at its latest API version from the
//     armtypes table azapi's own embedded type data generates.
//   - admission: live/azurerm-ids.json's class for each azurerm type, and
//     the two guards internal/live/substrate applies before admitting a
//     template ([substrate.AzurermWithheld]). Every azapi ARM type is
//     declared through azapi_resource, whose identity is type + parent_id +
//     name, all three in the block's own arguments.
//   - server-minted identity: no committed source says it per type, and
//     the answer below is the measured one, stated where it is applied in
//     [azurermRow]: no admitted azurerm row has an id segment the
//     configuration cannot set, and azapi_resource refuses a block with no
//     name rather than minting one.
//
// The rule is the AWS one ([classify], [destinedTier]): a taggable type is
// marker-carried; an untaggable type whose identity the declaration
// supplies is declaration-carried; an untaggable type nothing admits is
// record-carried. No Azure type is excluded by design: the credential
// ruling names two AWS types.
//
// What a declaration-carried Azure type has that an AWS one lacks is the
// fence: an estate is a set of resource groups (#1122), and the group's
// RBAC reaches a child whether it is tagged or not. [ARMScope] is read off
// each id template so the rendered section can say, per tier, how many
// types live inside a group and how many at a scope the group's RBAC never
// reaches.

// Committed inputs this section reads, beside the in-process armtypes and
// substrate tables.
const (
	AzurermIDsJSONRel = "live/azurerm-ids.json"
)

// The scopes [armScope] reads off an id template. They name where an
// instance lives, which decides whether a resource-group role assignment
// reaches it.
const (
	ARMScopeResourceGroup = "resource-group"
	ARMScopeSubscription  = "subscription"
	ARMScopeTenant        = "tenant"
	// ARMScopeFromArgument is an id whose leading segments are an argument
	// holding another resource's or a scope's ARM id (a role assignment's
	// scope, an extension resource's target), so the scope is per instance.
	ARMScopeFromArgument = "from-argument"
	// ARMScopeUnknown is a type whose id template the generator could not
	// read at all.
	ARMScopeUnknown = "unknown"
)

// AzureSubstrate is the Azure family's two providers, each tiered apart:
// azurerm's types are Terraform resource types, azapi's are the ARM types
// one azapi_resource block can declare, so the two populations do not sum
// to a meaningful number of anything and are never added together.
type AzureSubstrate struct {
	Azurerm Substrate `json:"azurerm"`
	Azapi   Substrate `json:"azapi"`
}

// azurermIDsFile is the subset of live/azurerm-ids.json read here.
type azurermIDsFile struct {
	Provider string `json:"provider"`
	Pin      struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"pin"`
	Types []struct {
		Type          string `json:"type"`
		Class         string `json:"class"`
		Template      string `json:"template"`
		TemplateClass string `json:"template_class"`
	} `json:"types"`
}

// The live/azurerm-ids.json classes this section distinguishes.
const (
	azurermClassTemplate       = "template"
	azurermClassParentID       = "parent-id"
	azurermClassIdentitySchema = "identity-schema"
	azurermClassUnmapped       = "unmapped"
	// azurermClassWithheld is not a class in the artifact: it is a
	// template-bearing row internal/live/substrate refuses to admit.
	azurermClassWithheld = "withheld"
)

// buildAzureSubstrate tiers both providers.
func buildAzureSubstrate(root string) (AzureSubstrate, error) {
	var ids azurermIDsFile
	if err := decodeJSON(root, AzurermIDsJSONRel, &ids); err != nil {
		return AzureSubstrate{}, err
	}
	if v, _ := substrate.AzurermIDSource(); v != ids.Pin.Version {
		return AzureSubstrate{}, fmt.Errorf("%s is azurerm %s and internal/live/substrate embeds %s; refresh the embedded copy", AzurermIDsJSONRel, ids.Pin.Version, v)
	}
	tags := armtypes.DefaultRMTags()
	if tags.Version != ids.Pin.Version {
		return AzureSubstrate{}, fmt.Errorf("armtypes' azurerm tags table is azurerm %s and %s is %s; both must be read at one pin", tags.Version, AzurermIDsJSONRel, ids.Pin.Version)
	}
	notARM := map[string]bool{}
	for _, t := range tags.NotARMTags {
		notARM[t] = true
	}
	withheld := map[string]string{}
	for _, w := range substrate.AzurermWithheld() {
		withheld[w[0]] = w[1]
	}

	rm := Substrate{Provider: ids.Provider + " " + ids.Pin.Version}
	for _, t := range ids.Types {
		_, tagged := tags.Row(t.Type)
		class := t.Class
		if _, ok := withheld[t.Type]; ok {
			class = azurermClassWithheld
		}
		row, err := azurermRow(t.Type, class, t.TemplateClass, armScope(t.Template), tagged && !notARM[t.Type])
		if err != nil {
			return AzureSubstrate{}, err
		}
		rm.Types = append(rm.Types, row)
	}
	rm.Counts = countRows(rm.Types)

	azapiVersion, _ := armtypes.Source()
	tab := armtypes.Default()
	ap := Substrate{Provider: "azure/azapi " + strings.TrimPrefix(azapiVersion, "v")}
	for _, armType := range tab.Types() {
		_, caps, _ := tab.Latest(armType)
		ap.Types = append(ap.Types, azapiRow(armType, caps.Has(armtypes.CapTags), tab.VersionDependent(armType)))
	}
	ap.Counts = countRows(ap.Types)

	sort.Slice(rm.Types, func(i, j int) bool { return rm.Types[i].Type < rm.Types[j].Type })
	sort.Slice(ap.Types, func(i, j int) bool { return ap.Types[i].Type < ap.Types[j].Type })
	return AzureSubstrate{Azurerm: rm, Azapi: ap}, nil
}

// azurermRow is one azurerm type's tier and status.
//
// Admission is the class: "template" and "parent-id" rows the substrate
// does not withhold are admitted by the Azure family's own rule.
// "identity-schema" rows keep the identity-schema route (#1122), which
// answers subscription_id from the provider block (#1120 unit 13) and,
// where the identity schema names an attribute no argument supplies,
// falls back to the generated template. A row whose template the generator
// mapped is admitted one way or the other; the 19 it could not map are
// refused, as the unit's own measurement over the 5.10.0 schema found.
//
// Server-minted identity, #2119's D4: an untaggable type whose identity
// the server mints is held by its record (record-located), and refused by
// name only when no record can hold its identity. No Azure type is minted
// outright: every component of every mapped row is a configuration
// argument or the provider's subscription (measured against the 5.10.0
// schema: no component names a Computed-only attribute), so the AWS veto
// list (identity.MarkerlessTypes) gains no Azure member. Seven admitted
// untaggable types make the naming argument Optional and Computed and mint
// a GUID when it is left out (internal/live/substrate's
// markProviderMintedIfAbsent); an instance that leaves it out is
// record-located through the record fallback, as AWS's name_prefix
// instances are. Which UNADMITTED types take their id from the create
// response, a UUID minted in create or a lookup at apply (17 at 5.10.0,
// GitHub issue #2147, azurerm_sentinel_threat_intelligence_indicator among
// them) is in no committed artifact; those types are held by
// identity.AzureServerMintedTypes (#2205): record-located, and marker
// discovery for the taggable ones. The roster is not a readiness input.
func azurermRow(typeName, class, templateClass, scope string, taggable bool) (Row, error) {
	facts := Facts{Taggable: taggable, ARMIDClass: class, ARMScope: scope}
	status := StatusNeedsEvidence
	switch class {
	case azurermClassTemplate, azurermClassParentID:
		facts.Admitted, status = true, StatusInContract
	case azurermClassIdentitySchema:
		facts.SchemaRoute = true
		if templateClass == azurermClassTemplate || templateClass == azurermClassParentID {
			facts.Admitted, status = true, StatusInContract
		}
	case azurermClassUnmapped, azurermClassWithheld:
	default:
		return Row{}, fmt.Errorf("%s gives %s the class %q, which the Azure readiness rule does not know", AzurermIDsJSONRel, typeName, class)
	}
	tier := TierRecordCarried
	switch {
	case taggable:
		tier = TierMarkerCarried
	case facts.Admitted:
		tier = TierDeclarationCarried
	}
	return Row{Type: typeName, Tier: tier, Status: status, Facts: facts}, nil
}

// azapiRow is one ARM type's tier through azapi_resource. Its identity is
// type + parent_id + name, every part an argument of the block: azapi
// builds the id from the three before its first request
// (parse.NewResourceID in AzapiResource.CreateUpdate, v2.13.0) and refuses
// a block with no name ("Missing required argument", nameWithDefaultNaming)
// unless the provider block's default_naming supplies one, which is
// configuration too. So no ARM type is server-minted through azapi, and an
// untaggable one is declaration-carried. Taggability is the latest API
// version's; a type whose taggability changes between versions is marked,
// because the stamp reads the version the block names.
func azapiRow(armType string, taggable, versionDependent bool) Row {
	tier := TierDeclarationCarried
	if taggable {
		tier = TierMarkerCarried
	}
	return Row{Type: armType, Tier: tier, Status: StatusInContract, Facts: Facts{
		Taggable:         taggable,
		Admitted:         true,
		VersionDependent: versionDependent,
		ARMScope:         ARMScopeFromArgument,
	}}
}

// armScope reads where an instance of a type lives off its ARM id template.
// A parent-id row's template is the one the generator expanded from the
// parent's parser, so it reads like any other; only a template that starts
// with an argument ("{scope}/...") is per instance.
func armScope(template string) string {
	switch {
	case template == "":
		return ARMScopeUnknown
	case strings.HasPrefix(template, "{"):
		return ARMScopeFromArgument
	case strings.HasPrefix(template, "/subscriptions/{"):
		head, _, _ := strings.Cut(template, "/providers/")
		if strings.Contains(strings.ToLower(head), "/resourcegroups/{") {
			return ARMScopeResourceGroup
		}
		return ARMScopeSubscription
	case strings.HasPrefix(template, "/providers/"):
		return ARMScopeTenant
	}
	return ARMScopeUnknown
}

// countRows tallies rows into a [Counts].
func countRows(rows []Row) Counts {
	c := Counts{Types: len(rows), Tiers: map[string]int{}, Statuses: map[string]int{}}
	for _, r := range rows {
		c.Tiers[r.Tier]++
		c.Statuses[r.Status]++
	}
	return c
}
