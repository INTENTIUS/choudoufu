// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// This file is the azurerm half of the Azure family's admission (GitHub
// issue #1120, unit 10; ruled on #1122 2026-10-19): the ARM id template
// for each azurerm type, read from live/azurerm-ids.json, the artifact
// tools/azurerm-id-gen generates from azurerm's own id parsers at the
// pinned 5.10.0.
//
// The artifact is embedded as a byte-for-byte copy, the way
// internal/live/registry embeds live/registry.json (go:embed cannot reach
// outside the package directory), and TestEmbeddedAzurermIDsMatchLive holds
// the copy to the original, so regenerating live/azurerm-ids.json fails the
// suite until the copy here is refreshed:
//
//	cp live/azurerm-ids.json internal/live/substrate/

// AzurermIDsPath is the embedded copy, relative to this package.
const AzurermIDsPath = "azurerm-ids.json"

//go:embed azurerm-ids.json
var embeddedAzurermIDs []byte

// The artifact's classes. Only the two a template was mapped for are
// admitted here; see [azurermAdmits].
const (
	azurermClassTemplate       = "template"
	azurermClassParentID       = "parent-id"
	azurermClassIdentitySchema = "identity-schema"
	azurermClassUnmapped       = "unmapped"
)

// azurermCloudSubscription is the artifact's spelling of the subscription
// the provider configuration targets, which is also
// internal/live/identity.CloudSubscriptionID's string.
const azurermCloudSubscription = "subscription-id"

type azurermIDsFile struct {
	Pin struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"pin"`
	Counts map[string]int  `json:"counts"`
	Types  []azurermIDType `json:"types"`
}

type azurermIDType struct {
	Type          string               `json:"type"`
	Class         string               `json:"class"`
	TemplateClass string               `json:"template_class"`
	Template      string               `json:"template"`
	Components    []azurermIDComponent `json:"components"`
}

// templateClass is what the row's template alone classifies as: the class
// itself, or for an identity-schema row the template_class recorded beside
// it.
func (t azurermIDType) templateClass() string {
	if t.Class == azurermClassIdentitySchema {
		return t.TemplateClass
	}
	return t.Class
}

// templateBearing reports whether the row maps a whole ARM id.
func (t azurermIDType) templateBearing() bool {
	c := t.templateClass()
	return c == azurermClassTemplate || c == azurermClassParentID
}

type azurermIDComponent struct {
	Literal string   `json:"literal"`
	Attrs   []string `json:"attrs"`
	Cloud   string   `json:"cloud"`
	ARMID   bool     `json:"arm_id"`
}

// azurermIDs is the parsed artifact: every row by type, and the reason a
// template-bearing row is withheld, by type.
type azurermIDs struct {
	version, commit string
	rows            map[string]azurermIDType
	withheld        map[string]string
}

var (
	azurermOnce sync.Once
	azurermTab  *azurermIDs
	azurermErr  error
)

func loadAzurermIDs() (*azurermIDs, error) {
	azurermOnce.Do(func() {
		azurermTab, azurermErr = parseAzurermIDs(embeddedAzurermIDs)
	})
	return azurermTab, azurermErr
}

func parseAzurermIDs(raw []byte) (*azurermIDs, error) {
	var f azurermIDsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("substrate: parsing the embedded %s: %w", AzurermIDsPath, err)
	}
	out := &azurermIDs{
		version:  f.Pin.Version,
		commit:   f.Pin.Commit,
		rows:     make(map[string]azurermIDType, len(f.Types)),
		withheld: map[string]string{},
	}
	byTemplate := map[string][]string{}
	for _, t := range f.Types {
		out.rows[t.Type] = t
		if t.templateBearing() {
			k := normalizedARMTemplate(t.Template)
			byTemplate[k] = append(byTemplate[k], t.Type)
		}
	}
	for _, t := range f.Types {
		if !t.templateBearing() {
			continue
		}
		if why := azurermRowShapeProblem(t); why != "" {
			out.withheld[t.Type] = why
			continue
		}
		if why := azurermSharedTemplateProblem(t, byTemplate[normalizedARMTemplate(t.Template)]); why != "" {
			out.withheld[t.Type] = why
		}
	}
	return out, nil
}

// normalizedARMTemplate is a template with every value segment's name
// dropped and its letters folded: two parsers that spell one id's segments
// differently ({resourceGroup}, {resourceGroupName}) state one id.
func normalizedARMTemplate(template string) string {
	var b strings.Builder
	in := false
	for _, r := range strings.ToLower(template) {
		switch {
		case r == '{':
			in = true
			b.WriteRune(r)
		case r == '}':
			in = false
			b.WriteRune(r)
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// azurermSharedTemplateProblem is the second guard (#1120 unit 13). Two
// types share one ARM id template legitimately when they are variants of
// one ARM resource type, each naming the object with its own name argument:
// the data factory datasets, the automation connections, the Oracle
// autonomous database and its clone types. A wrong marker is the case where
// the type's own object is not at that id at all and the id is borrowed
// from the type it configures, which the provider marks by giving the
// borrowed id's last segment to an argument other than name:
// azurerm_api_management_api_operation_policy's id is its operation's
// (operation_id), azurerm_eventgrid_partner_configuration's is its
// resource group's (resource_group_name). Such a row is withheld unless it
// is the type the others extend (azurerm_api_management_api_operation,
// whose operation_id is the operation's own name), because only that one
// owns the object at the id.
//
// It replaces unit 10's coarser rule, which withheld every row whose type
// name extended another's on the same template: that caught the load
// balancer rules, whose rows recorded azurerm_lb's id because the
// generator read a wrapper's signature (tools/azurerm-id-gen now reads the
// wrapped parser and checks it against Read), and it withheld fourteen
// variants that were never wrong.
func azurermSharedTemplateProblem(t azurermIDType, group []string) string {
	if len(group) < 2 {
		return ""
	}
	last := t.Components[len(t.Components)-1]
	if len(last.Attrs) == 1 && last.Attrs[0] == "name" {
		return ""
	}
	var others []string
	for _, other := range group {
		if other == t.Type {
			continue
		}
		if strings.HasPrefix(other, t.Type+"_") {
			return ""
		}
		others = append(others, other)
	}
	sort.Strings(others)
	return fmt.Sprintf("its id template is also %s's, and its last segment is %s rather than its own name, so the id is the object it configures and not one it owns", strings.Join(others, " and "), strings.Join(last.Attrs, " or "))
}

// azurermRowShapeProblem is the first of two guards a template-bearing row
// passes before it is admitted. Both exist because a wrong id is a wrong
// marker (HANDOFF's safety rule), and the generator records what a parser
// formats, not whether that parser is the one for this type's own object.
//
// This one is the shape: the id has to end in an argument (the object's own
// name or key), and it has to carry at least one typed literal segment. A
// row that is a single argument ("{scope}", read from a scope argument) is
// the id of the scope, not of an object in it: azurerm_pim_active_role_assignment,
// azurerm_pim_eligible_role_assignment and azurerm_role_management_policy
// at 5.10.0, whose parsers have their own grammar the generator does not
// read.
//
// The second is [azurermSharedTemplateProblem]: a type whose id is another
// type's object.
func azurermRowShapeProblem(t azurermIDType) string {
	if len(t.Components) == 0 {
		return "it has no components"
	}
	if len(t.Components[len(t.Components)-1].Attrs) == 0 {
		return "its id does not end in an argument naming the object"
	}
	for _, c := range t.Components {
		if c.Literal != "" && len(c.Attrs) == 0 && c.Cloud == "" {
			return ""
		}
	}
	return "its id is a single argument with no typed segment, the id of a scope rather than of an object in it"
}

// azurermAdmits is the azurerm answer to [Substrate.SynthesizeIdentity].
//
//   - A type the artifact classes "template" (every segment mapped to an
//     argument or to the subscription) or "parent-id" (a leading run of
//     segments supplied by an argument holding a parent's or a scope's ARM
//     id), that passes the guards above: the row's ARM id template as
//     identity components.
//   - Every other azurerm type: the identity-schema route
//     ([SynthesizedIdentity.FromIdentitySchema]), told the one thing it
//     cannot read from the schemas, that an identity attribute named
//     subscription_id with no argument by that name is the provider
//     configuration's subscription ([azurermCloudIdentityAttrs], #1120 unit
//     13), and handed the row's template where it has one that passes the
//     guards. #1122 rules that the 196 types serving a resource identity
//     schema keep that route; an "unmapped" or withheld type with none gets
//     the route's refusal, which names the type ("serves no resource
//     identity schema"), and so lint's unadmitted-type, as an AWS type with
//     no row and no identity schema does. Never a guess from
//     resource_group_name and name.
func azurermAdmits(typeName string) (SynthesizedIdentity, bool) {
	route := SynthesizedIdentity{
		FromIdentitySchema: true,
		NonAWSProvider:     true,
		CloudIdentityAttrs: azurermCloudIdentityAttrs(),
	}
	tab, err := loadAzurermIDs()
	if err != nil {
		return route, true
	}
	row, ok := tab.rows[typeName]
	if !ok || !row.templateBearing() {
		return route, true
	}
	if _, withheld := tab.withheld[typeName]; withheld {
		return route, true
	}
	synth := azurermTemplate(row)
	if row.Class == azurermClassIdentitySchema {
		route.ARMTemplate = &synth
		return route, true
	}
	return synth, true
}

// azurermCloudIdentityAttrs is the azurerm provider's answer to the
// identity-schema route's one open question (#1120 unit 13): every
// identity schema azurerm 5.10.0 serves requires subscription_id for
// import, and the subscription is the provider block's subscription_id,
// the one azurerm builds every id it creates with
// (meta.(*clients.Client).Account.SubscriptionId). It is the AWS identity
// schemas' account_id and region, answered from the same place AWS's
// region is.
func azurermCloudIdentityAttrs() map[string]string {
	return map[string]string{"subscription_id": azurermCloudSubscription}
}

// azurermTemplate is a row's ARM id template as a synthesized identity.
func azurermTemplate(row azurermIDType) SynthesizedIdentity {
	comps := make([]IdentityComponent, 0, len(row.Components))
	for _, c := range row.Components {
		comps = append(comps, IdentityComponent{
			Literal: c.Literal,
			Attrs:   append([]string(nil), c.Attrs...),
			Cloud:   c.Cloud,
		})
	}
	return SynthesizedIdentity{
		NonAWSProvider: true,
		Components:     comps,
		ImportSyntax:   row.Template,
		// azurerm sets a resource's id to the id its parser formats
		// (d.SetId(id.ID())), the one this template is read from, so a
		// reference to .id is a reference to this identity.
		IdentityAttrs: []string{"id"},
		ARMResourceID: true,
	}
}

// AzurermIDSource is the azurerm version and commit the embedded artifact
// was generated against.
func AzurermIDSource() (version, commit string) {
	tab, err := loadAzurermIDs()
	if err != nil {
		return "", ""
	}
	return tab.version, tab.commit
}

// AzurermAdmission is how many azurerm types the embedded artifact gives
// each answer: "template" and "parent-id" for the two classes this family
// admits by template alone, "withheld" for the template-bearing rows of
// those classes the guards above refuse, "unmapped" for the rows with no
// template, and for the types with a resource identity schema
// "identity-schema+template" (handed a template that passes the guards)
// or "identity-schema" (none, or one the guards withhold).
func AzurermAdmission() map[string]int {
	tab, err := loadAzurermIDs()
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for name, row := range tab.rows {
		_, withheld := tab.withheld[name]
		switch {
		case row.Class == azurermClassIdentitySchema && row.templateBearing() && !withheld:
			out["identity-schema+template"]++
		case row.Class == azurermClassIdentitySchema:
			out[azurermClassIdentitySchema]++
		case withheld:
			out["withheld"]++
		default:
			out[row.Class]++
		}
	}
	return out
}

// AzurermWithheld is every template-bearing azurerm type the guards refuse,
// sorted, with the reason.
func AzurermWithheld() [][2]string {
	tab, err := loadAzurermIDs()
	if err != nil {
		return nil
	}
	out := make([][2]string, 0, len(tab.withheld))
	for name, why := range tab.withheld {
		out = append(out, [2]string{name, why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
