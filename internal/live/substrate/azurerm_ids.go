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
	Type       string               `json:"type"`
	Class      string               `json:"class"`
	Template   string               `json:"template"`
	Components []azurermIDComponent `json:"components"`
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
		if t.Class == azurermClassTemplate || t.Class == azurermClassParentID {
			byTemplate[t.Template] = append(byTemplate[t.Template], t.Type)
		}
	}
	for _, t := range f.Types {
		if t.Class != azurermClassTemplate && t.Class != azurermClassParentID {
			continue
		}
		if why := azurermRowShapeProblem(t); why != "" {
			out.withheld[t.Type] = why
			continue
		}
		for _, other := range byTemplate[t.Template] {
			if other != t.Type && strings.HasPrefix(t.Type, other+"_") {
				out.withheld[t.Type] = fmt.Sprintf("its id template is the one %s's id parser gives, and %s is named as a part of %s, so the parser named is the parent's and the id would be the parent's object", other, t.Type, other)
				break
			}
		}
	}
	return out, nil
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
// The second, in [parseAzurermIDs], is a sub-resource borrowing its parent's
// parser: a row whose template is shared with a type whose name is a prefix
// of its own (azurerm_lb_nat_rule and azurerm_lb_nat_pool share
// azurerm_lb's LoadBalancerId, because their importer parses the rule's id
// and returns the load balancer's). Variants of one ARM type that share a
// template legitimately (the data factory datasets, the automation
// connections) are withheld too when one's name extends another's; that
// costs admission, never a marker.
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

// azurermAdmits is the azurerm answer to [Substrate.SynthesizeIdentity]:
// the row's ARM id template as identity components, for a type the artifact
// classes "template" (every segment mapped to an argument or to the
// subscription) or "parent-id" (a leading run of segments supplied by an
// argument holding a parent's or a scope's ARM id), that passes the guards
// above.
//
// Every other class answers false, deliberately:
//
//   - "identity-schema": the provider serves a resource identity schema for
//     the type, and #1122 rules that route keeps it. The identity-schema
//     route is the AWS family's answer, which [SynthesizeIdentity] reaches
//     next.
//   - "unmapped": no template. The type gets what an AWS type with no row
//     and no identity schema gets: the identity-schema route's refusal,
//     which names the type (internal/live/identity's synthesizeTypeIdentity,
//     "serves no resource identity schema"), and so lint's unadmitted-type.
//     Never a guess from resource_group_name and name.
func azurermAdmits(typeName string) (SynthesizedIdentity, bool) {
	tab, err := loadAzurermIDs()
	if err != nil {
		return SynthesizedIdentity{}, false
	}
	row, ok := tab.rows[typeName]
	if !ok || (row.Class != azurermClassTemplate && row.Class != azurermClassParentID) {
		return SynthesizedIdentity{}, false
	}
	if _, withheld := tab.withheld[typeName]; withheld {
		return SynthesizedIdentity{}, false
	}
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
	}, true
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
// each answer: "template" and "parent-id" for the two routes this family
// admits by, "withheld" for the template-bearing rows the guards above
// refuse, "identity-schema" and "unmapped" for the two it leaves to the
// identity-schema route.
func AzurermAdmission() map[string]int {
	tab, err := loadAzurermIDs()
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for name, row := range tab.rows {
		if _, withheld := tab.withheld[name]; withheld {
			out["withheld"]++
			continue
		}
		out[row.Class]++
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
