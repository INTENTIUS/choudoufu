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

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the azurerm half of the Azure family's admission (GitHub
// issue #1120, unit 10; ruled on #1122 2026-10-19): the ARM id template
// for each azurerm type, read from live/azurerm-ids.json, the artifact
// tools/azurerm-id-gen generates from azurerm's own id parsers at the
// pinned 5.10.0.
//
// The artifact is embedded as a byte-for-byte copy, the way
// pkg/registry embeds live/registry.json (go:embed cannot reach
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
	// azurermClassComposite: the id is several ARM ids joined by "|" as
	// azurerm's commonids.CompositeResourceID formats them, or for the
	// subnet associations one whole id; tools/azurerm-id-gen derives it
	// only after checking each part against azurerm's id code (GitHub
	// issue #2204).
	azurermClassComposite = "composite"
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
	Type           string               `json:"type"`
	Class          string               `json:"class"`
	TemplateClass  string               `json:"template_class"`
	Template       string               `json:"template"`
	Components     []azurermIDComponent `json:"components"`
	IdentitySchema []string             `json:"identity_schema"`
	Contract       *azurermContract     `json:"identity_contract"`
	// Composite is a composite row's record of its parts and where azurerm
	// builds the id (tools/azurerm-id-gen composite.go).
	Composite *azurermComposite `json:"composite"`
	// IdentitySources and IdentitySourcesProblem are where a record reads
	// each identity attribute from, or why it cannot (GitHub issue #2138,
	// azurerm_sources.go).
	IdentitySources        map[string]azurermIdentitySource `json:"identity_sources"`
	IdentitySourcesProblem string                           `json:"identity_sources_problem"`
	// CreateCall, on a row whose components were built from azurerm's
	// Create call (GitHub issue #2201 item 1), says where each segment's
	// value came from. Only a constant matters here: a row that ends in
	// one, or an identity attribute that is one, passes the guards below
	// because azurerm's Create states it.
	CreateCall *azurermCreateCall `json:"create_call"`
	// Route is the generator's record of the route this package and the
	// identity-schema route give the type (#1120 unit 17), one of the
	// AzurermRoute* constants. Admission does not read it; the guards
	// below decide, and TestAzurermRoutesAreTheGuardsVerdicts holds the
	// record to them.
	Route string `json:"route"`
}

// azurermCreateCall is the part of the artifact's create_call the guards
// read: the constant segments, by the id's segment name.
type azurermCreateCall struct {
	Sources map[string]struct {
		Constant string `json:"constant"`
	} `json:"sources"`
}

// constants is the Create call's constant segments, segment name to value.
func (c *azurermCreateCall) constants() map[string]string {
	out := map[string]string{}
	if c == nil {
		return out
	}
	for seg, s := range c.Sources {
		if s.Constant != "" {
			out[seg] = s.Constant
		}
	}
	return out
}

// azurermShareKey is the key under which two rows share a template: the
// template with each value segment's name dropped and its letters folded
// ([normalizedARMTemplate]), and with a segment azurerm's Create call
// gives a constant standing as that constant, so the six API Management
// identity providers, whose ids differ only in that constant, are six ids.
func azurermShareKey(t azurermIDType) string {
	tmpl := t.Template
	for seg, v := range t.CreateCall.constants() {
		tmpl = strings.ReplaceAll(tmpl, "{"+seg+"}", v)
	}
	return normalizedARMTemplate(tmpl)
}

// azurermContract is azurerm's own statement, in the resource identity
// acceptance test it generates for a type
// (internal/services/<service>/<type>_resource_identity_gen_test.go), of
// where each identity attribute's value comes from (#1120 unit 17). The
// test fails unless every identity field is checked
// (ExpectAllIdentityFieldsAreChecked), so it covers the whole identity.
type azurermContract struct {
	Source     string                         `json:"source"`
	Attributes map[string]azurermContractAttr `json:"attributes"`
}

// azurermContractAttr is one identity attribute's check, exactly one field
// set:
//
//   - Argument: ExpectIdentityValueMatchesStateAtPath, the value is that
//     argument's.
//   - Within: ExpectStateContainsIdentityValueAtPath, the value is a piece
//     of that argument's, which holds an ARM id.
//   - ProviderSubscription: ExpectIdentityValue with the test account's
//     primary subscription, the one the provider block targets.
//   - Constant: ExpectIdentityValue with a literal (a DNS record type).
//   - Other: any other check, as written; nothing here can satisfy it.
type azurermContractAttr struct {
	Argument             string `json:"argument,omitempty"`
	Within               string `json:"within,omitempty"`
	ProviderSubscription bool   `json:"provider_subscription,omitempty"`
	Constant             string `json:"constant,omitempty"`
	Other                string `json:"other,omitempty"`
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
	return c == azurermClassTemplate || c == azurermClassParentID || c == azurermClassComposite
}

// composite reports whether the row is a composite-class one.
func (t azurermIDType) composite() bool { return t.templateClass() == azurermClassComposite }

type azurermComposite struct {
	Join   string `json:"join"`
	Create string `json:"create"`
	Parts  []struct {
		IDType    string   `json:"id_type"`
		Template  string   `json:"template"`
		Arguments []string `json:"arguments"`
	} `json:"parts"`
}

type azurermIDComponent struct {
	Literal string   `json:"literal"`
	Attrs   []string `json:"attrs"`
	Cloud   string   `json:"cloud"`
	ARMID   bool     `json:"arm_id"`
	Segment string   `json:"segment"`
	// ARMSegment, on a data-plane row (GitHub issue #2206), says the one
	// argument holds an ARM id and this component is the path segment at
	// Index of it, after the segment named After.
	ARMSegment *azurermARMSegment `json:"arm_segment"`
	// URLEscape is "path" or "query" where azurerm escapes the value as it
	// formats the URL; OmitIfAbsent is an optional argument whose absence
	// leaves an empty segment (App Configuration's label).
	URLEscape    string `json:"url_escape"`
	OmitIfAbsent bool   `json:"omit_if_absent"`
}

type azurermARMSegment struct {
	Index int    `json:"index"`
	After string `json:"after"`
}

// The routes the generator records per type (#1120 unit 17), the field
// tools/readiness-gen reads as "route".
const (
	// AzurermRouteIdentitySchema: the provider's resource identity schema
	// admits the type on its own (internal/live/identity's
	// IdentitySchemaRouteAdmits), and its template, where it agrees, is
	// the rendered id.
	AzurermRouteIdentitySchema = "identity-schema"
	// AzurermRouteTemplateFallback: the type serves an identity schema the
	// route cannot satisfy from configuration, and its ARM id template,
	// which passes every guard here including azurerm's own identity test,
	// admits it.
	AzurermRouteTemplateFallback = "template-fallback"
	// AzurermRouteTemplate and AzurermRouteParentID: no identity schema,
	// admitted by the template alone.
	AzurermRouteTemplate = azurermClassTemplate
	AzurermRouteParentID = azurermClassParentID
	// AzurermRouteComposite: the id is the join of the type's linked
	// objects' ids as azurerm joins them (GitHub issue #2204).
	AzurermRouteComposite = azurermClassComposite
	// AzurermRouteWithheld: the type has a template and a guard refuses
	// it, and nothing else admits the type. Refused by name.
	AzurermRouteWithheld = "withheld"
	// AzurermRouteUnmapped: no template and no identity-schema admission.
	AzurermRouteUnmapped = azurermClassUnmapped
)

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
			k := azurermShareKey(t)
			byTemplate[k] = append(byTemplate[k], t.Type)
		}
	}
	for _, t := range f.Types {
		if !t.templateBearing() {
			continue
		}
		if why := azurermSegmentProblem(t); why != "" {
			out.withheld[t.Type] = why
			continue
		}
		if t.composite() {
			if why := azurermCompositeProblem(t); why != "" {
				out.withheld[t.Type] = why
			}
			continue
		}
		if why := azurermRowShapeProblem(t); why != "" {
			out.withheld[t.Type] = why
			continue
		}
		if why := azurermSharedTemplateProblem(t, byTemplate[azurermShareKey(t)]); why != "" {
			out.withheld[t.Type] = why
			continue
		}
		if t.Class == azurermClassIdentitySchema {
			if why := azurermContractProblem(t); why != "" {
				out.withheld[t.Type] = why
			}
		}
	}
	return out, nil
}

// azurermSegmentProblem is the first guard (#1120 unit 17): every value
// segment of the template has to be resolvable from this configuration
// alone, statically and exactly, before anything else about the row is
// worth asking. A segment qualifies in one of three ways, and in no other:
//
//   - one configuration argument whose value is the segment's value;
//   - the provider block's subscription ([azurermCloudSubscription]),
//     answered and refused by name as an AWS region is;
//   - at the very start of the id, one argument holding a parent's or a
//     scope's whole ARM id, which supplies every segment up to the one it
//     is recorded against, verbatim. Nothing is ever parsed out of such an
//     argument here: the parent's id is the prefix, as written.
//
// So a segment that reads several arguments, reads none, is a provider
// value this family does not answer, or takes a whole ARM id after the id
// has begun (the id would be two ids joined, not one) is refused, by the
// segment's name. azurerm_application_insights_analytics_item at 5.10.0 is
// the last of these: its {scopePath} was mapped to the scope argument,
// whose value is "shared" or "user", not an id.
func azurermSegmentProblem(t azurermIDType) string {
	dataPlane := false
	for _, c := range t.Components {
		if c.ARMSegment != nil || azurermDNSSuffixCloud(c.Cloud) {
			dataPlane = true
		}
	}
	if dataPlane {
		if why := azurermDataPlaneProblem(t); why != "" {
			return why
		}
	}
	for i, c := range t.Components {
		switch {
		case c.ARMSegment != nil || azurermDNSSuffixCloud(c.Cloud):
			// Held to the family grammar above.
		case c.Cloud != "":
			if c.Cloud != azurermCloudSubscription || len(c.Attrs) > 0 {
				return fmt.Sprintf("segment {%s} is the provider configuration's %q, which this family does not answer", c.Segment, c.Cloud)
			}
		case len(c.Attrs) == 0:
			if c.Literal == "" {
				return fmt.Sprintf("segment {%s} names no argument and no provider configuration value, so nothing in a configuration supplies it", c.Segment)
			}
		case len(c.Attrs) > 1:
			return fmt.Sprintf("segment {%s} reads whichever of %s is set, so which argument names the object is a choice this table would be guessing", c.Segment, strings.Join(c.Attrs, ", "))
		case c.ARMID && i > 0 && t.composite() && strings.HasSuffix(t.Components[i-1].Literal, "|"):
			// A composite id is its parts joined by "|"; each part may
			// begin with a whole id argument (azurermCompositeProblem).
		case c.ARMID && i > 0:
			return fmt.Sprintf("segment {%s} is read from %s, an argument holding a whole ARM id, after the id has begun, so the id would be two ids joined rather than one", c.Segment, c.Attrs[0])
		}
	}
	return ""
}

// azurermContractProblem is the last guard, for a type that serves a
// resource identity schema (#1120 unit 17). Such a type's template is
// rendered beside, or in place of, the identity the provider imports by,
// so it has to read each identity attribute from exactly where azurerm's
// own identity test says the provider reads it ([azurermContract]): a
// segment reading an argument, the argument the test matches that
// attribute to; a leading ARM id argument, the argument the test finds the
// attributes inside; the provider's subscription, an attribute the test
// expects to be the provider's. And every attribute the test checks has
// to be read by some segment. A type azurerm publishes no such test for is
// withheld too, because then nothing in the provider's source beyond its
// id parser says the template is exact. The contract and the template come
// from one tools/azurerm-id-gen run over the same checkout, so this guard
// is that run's consistency with azurerm's identity tests, not an
// independent check of it (#2140). This is the condition the template fallback was approved on:
// a storage_account_name read out of storage_account_id is admitted
// because azurerm_storage_container's test states it is in there, not
// because the names look alike.
func azurermContractProblem(t azurermIDType) string {
	if t.CreateCall != nil && t.Contract == nil {
		// No identity test, but azurerm's Create call builds the very
		// components the segment names gave (the generator checks, and
		// records create_call only then; GitHub issue #2201 item 1).
		return ""
	}
	if t.Contract == nil || len(t.Contract.Attributes) == 0 {
		return "azurerm publishes no resource identity test for it, so nothing outside this table says its ARM id template reads its identity attributes from configuration exactly"
	}
	attrs := t.Contract.Attributes
	for _, name := range t.IdentitySchema {
		if _, ok := attrs[name]; !ok {
			return fmt.Sprintf("azurerm's identity test (%s) does not say where identity attribute %s comes from", t.Contract.Source, name)
		}
	}
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	covered := map[string]bool{}
	for _, c := range t.Components {
		switch {
		case c.Cloud != "":
			n := 0
			for _, name := range names {
				if attrs[name].ProviderSubscription {
					covered[name] = true
					n++
				}
			}
			if n == 0 {
				return fmt.Sprintf("segment {%s} is the provider configuration's subscription, and azurerm's identity test (%s) expects no identity attribute to be", c.Segment, t.Contract.Source)
			}
		case len(c.Attrs) == 1 && c.ARMID:
			n := 0
			for _, name := range names {
				if strings.EqualFold(attrs[name].Within, c.Attrs[0]) {
					covered[name] = true
					n++
				}
			}
			if n == 0 {
				return fmt.Sprintf("segments up to {%s} are read whole from %s, and azurerm's identity test (%s) finds no identity attribute inside it", c.Segment, c.Attrs[0], t.Contract.Source)
			}
		case len(c.Attrs) == 1:
			var reads []string
			for _, name := range names {
				if strings.EqualFold(attrs[name].Argument, c.Attrs[0]) {
					reads = append(reads, name)
				}
			}
			if len(reads) != 1 {
				return fmt.Sprintf("segment {%s} reads %s, and azurerm's identity test (%s) matches %d identity attributes to that argument, not one", c.Segment, c.Attrs[0], t.Contract.Source, len(reads))
			}
			if covered[reads[0]] {
				return fmt.Sprintf("segment {%s} reads %s, identity attribute %s, a second time", c.Segment, c.Attrs[0], reads[0])
			}
			covered[reads[0]] = true
		}
	}
	leadingID := len(t.Components) > 0 && t.Components[0].ARMID
	for _, name := range names {
		if covered[name] {
			continue
		}
		a := attrs[name]
		switch {
		case a.ProviderSubscription && leadingID:
			// The parent's id carries its own subscription, which the test
			// asserts is the provider's.
		case a.Constant != "" && azurermTemplateHasLiteral(t, a.Constant):
		case a.Within == "id" && azurermCreateConstantNamed(t, name):
			// azurerm's identity test finds the attribute in the object's
			// own id, and its Create call gives that segment the constant
			// (a DNS record's type): the template carries the constant.
		default:
			return fmt.Sprintf("identity attribute %s is read by no segment of %s, where azurerm's identity test (%s) reads it from %s", name, t.Template, t.Contract.Source, a.describe())
		}
	}
	return ""
}

// azurermCreateConstantNamed reports whether the row's Create call gives a
// segment the constant, and that segment is the identity attribute attr
// (record_type for the segment recordType).
func azurermCreateConstantNamed(t azurermIDType, attr string) bool {
	for seg := range t.CreateCall.constants() {
		if strings.EqualFold(strings.ReplaceAll(attr, "_", ""), seg) {
			return true
		}
	}
	return false
}

// azurermTemplateHasLiteral reports whether one of the row's literal
// segments is value, compared as ARM compares it.
func azurermTemplateHasLiteral(t azurermIDType, value string) bool {
	for _, c := range t.Components {
		if len(c.Attrs) > 0 || c.Cloud != "" {
			continue
		}
		for _, seg := range strings.Split(c.Literal, "/") {
			if strings.EqualFold(seg, value) {
				return true
			}
		}
	}
	return false
}

func (a azurermContractAttr) describe() string {
	switch {
	case a.Argument != "":
		return "argument " + a.Argument
	case a.Within != "":
		return "inside argument " + a.Within
	case a.ProviderSubscription:
		return "the provider's subscription"
	case a.Constant != "":
		return fmt.Sprintf("the constant %q", a.Constant)
	}
	return a.Other
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
	if len(t.Components[len(t.Components)-1].Attrs) == 0 && !azurermEndsInCreateConstant(t) && !azurermDataPlaneSingleton(t) {
		return "its id does not end in an argument naming the object"
	}
	for _, c := range t.Components {
		if c.Literal != "" && len(c.Attrs) == 0 && c.Cloud == "" {
			return ""
		}
	}
	return "its id is a single argument with no typed segment, the id of a scope rather than of an object in it"
}

// azurermCompositeProblem is the composite class's replacement for the
// shape and shared-template guards (GitHub issue #2204). Those guards ask
// whether an id is the id of the object the type owns; a composite id is
// not one ARM id at all, it is azurerm's own join of the parts, and the
// generator has already accounted for every segment of every part against
// azurerm's id types (composite.go). What is held here is that the record
// says so: the parts are recorded, the template is exactly the parts'
// templates joined as azurerm joins them, and the components render
// that many parts, each beginning where the previous "|" ended.
func azurermCompositeProblem(t azurermIDType) string {
	c := t.Composite
	if c == nil || len(c.Parts) == 0 || c.Create == "" {
		return "its composite id has no recorded parts or no record of where azurerm builds it"
	}
	if (len(c.Parts) > 1) != (c.Join == "|") || len(c.Parts) > 2 {
		return fmt.Sprintf("its composite id joins %d parts with %q, which is not azurerm's CompositeResourceID (two ids, %q) or a single id", len(c.Parts), c.Join, "|")
	}
	var tmpls []string
	for _, p := range c.Parts {
		if p.Template == "" || len(p.Arguments) == 0 {
			return fmt.Sprintf("part %s records no template or no argument", p.IDType)
		}
		tmpls = append(tmpls, p.Template)
	}
	if strings.Join(tmpls, "|") != t.Template {
		return "its template is not its parts' templates joined with |"
	}
	joins := 0
	for _, comp := range t.Components {
		joins += strings.Count(comp.Literal, "|")
	}
	if joins != len(c.Parts)-1 {
		return fmt.Sprintf("its components join %d times, its parts %d", joins, len(c.Parts)-1)
	}
	return ""
}

// azurermEndsInCreateConstant reports whether a row's id ends in a segment
// azurerm's Create call gives a constant ("default", "current", a channel
// name): the object's own name is then that constant, which azurerm states
// and which is the same for every object of the type.
func azurermEndsInCreateConstant(t azurermIDType) bool {
	last := t.Components[len(t.Components)-1]
	if last.Literal == "" || len(last.Attrs) > 0 || last.Cloud != "" {
		return false
	}
	tail := last.Literal[strings.LastIndex(last.Literal, "/")+1:]
	for _, v := range t.CreateCall.constants() {
		if strings.EqualFold(v, tail) {
			return true
		}
	}
	return false
}

// azurermAdmits is the azurerm answer to [Substrate.SynthesizeIdentity].
//
//   - A type the artifact classes "template" (every segment mapped to an
//     argument or to the subscription) or "parent-id" (a leading run of
//     segments supplied by an argument holding a parent's or a scope's ARM
//     id), that passes the guards above and whose every argument the
//     served schema lets a configuration set ([azurermServedSchemaProblem]):
//     the row's ARM id template as identity components.
//   - Every other azurerm type: the identity-schema route
//     ([SynthesizedIdentity.FromIdentitySchema]), told the one thing it
//     cannot read from the schemas, that an identity attribute named
//     subscription_id with no argument by that name is the provider
//     configuration's subscription ([azurermCloudIdentityAttrs], #1120 unit
//     13), and handed the row's template where it has one that passes the
//     guards. #1122 rules that the 196 types serving a resource identity
//     schema keep that route; an "unmapped" type with none gets the
//     route's refusal, which names the type ("serves no resource identity
//     schema"), and so lint's unadmitted-type, as an AWS type with no row
//     and no identity schema does. A withheld template goes to the route
//     as [SynthesizedIdentity.TemplateWithheld] and is the route's refusal
//     wherever the route does not admit the type itself, so the refusal
//     names the segment (#1120 unit 17). Never a guess from
//     resource_group_name and name.
func azurermAdmits(typeName string, schema providers.Schema) (SynthesizedIdentity, bool) {
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
	if ok && row.Class == azurermClassIdentitySchema {
		route.IdentitySources = row.identitySources()
		route.IdentitySourcesProblem = row.IdentitySourcesProblem
	}
	if !ok || !row.templateBearing() {
		return route, true
	}
	if why, withheld := tab.withheld[typeName]; withheld {
		route.TemplateWithheld = fmt.Sprintf("its ARM id template %s is withheld: %s", row.Template, why)
		return route, true
	}
	if why := azurermServedSchemaProblem(row, schema); why != "" {
		route.TemplateWithheld = fmt.Sprintf("its ARM id template %s is withheld: %s", row.Template, why)
		return route, true
	}
	synth := azurermTemplate(row)
	if row.Class == azurermClassIdentitySchema {
		route.ARMTemplate = &synth
		return route, true
	}
	// The template is the type's identity here, and its subscription
	// segment is the provider configuration's. Said for the identity
	// schema's attribute of that name too, so the record rung reads it from
	// the same place for a template-routed type as for one on the
	// identity-schema route (GitHub issue #2135); nothing reads it off a
	// template answer otherwise.
	synth.CloudIdentityAttrs = azurermCloudIdentityAttrs()
	return synth, true
}

// azurermServedSchemaProblem is the segment guard's other half, the one
// that needs the schema of the provider actually in use rather than the
// pinned one the table was generated from (#1120 unit 17): every argument
// a segment reads has to be one that provider declares and lets a
// configuration set. An argument a later azurerm dropped, or made
// computed-only, is a segment no configuration can supply, refused here by
// name rather than left for each instance to fail on.
func azurermServedSchemaProblem(row azurermIDType, schema providers.Schema) string {
	for _, c := range row.Components {
		for _, arg := range c.Attrs {
			var a *configschema.Attribute
			if schema.Block != nil {
				a = schema.Block.Attributes[arg]
			}
			if a == nil || !(a.Required || a.Optional) {
				return fmt.Sprintf("segment {%s} reads %s, which the %s schema this provider serves does not declare as an argument a configuration sets", c.Segment, arg, row.Type)
			}
		}
	}
	return ""
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

// AzurermCloudIdentityAttrs is [azurermCloudIdentityAttrs] for
// tools/azurerm-id-gen, which asks the identity-schema route the same
// question this family does when it records each type's route (#1120 unit
// 17).
func AzurermCloudIdentityAttrs() map[string]string {
	return azurermCloudIdentityAttrs()
}

// AzurermWithheldIn is the guards above run over an artifact that is not
// (yet) the embedded one: every template-bearing type in raw they refuse,
// with the reason. tools/azurerm-id-gen calls it on the artifact it is
// writing, so the route it records for a withheld row is this package's
// verdict rather than a copy of its rules (#1120 unit 17).
func AzurermWithheldIn(raw []byte) (map[string]string, error) {
	tab, err := parseAzurermIDs(raw)
	if err != nil {
		return nil, err
	}
	return tab.withheld, nil
}

// azurermTemplate is a row's ARM id template as a synthesized identity.
func azurermTemplate(row azurermIDType) SynthesizedIdentity {
	comps := make([]IdentityComponent, 0, len(row.Components))
	for _, c := range row.Components {
		ic := IdentityComponent{
			Literal: c.Literal,
			Attrs:   append([]string(nil), c.Attrs...),
			Cloud:   c.Cloud,
		}
		ic.URLEscape, ic.OmitIfAbsent = c.URLEscape, c.OmitIfAbsent
		if c.ARMSegment != nil {
			ic.ARMSegmentIndex, ic.ARMSegmentAfter = c.ARMSegment.Index, c.ARMSegment.After
		}
		comps = append(comps, ic)
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

// azurermDataPlaneSingleton reports whether the row is a data-plane id that
// ends in a fixed path: the one object a vault holds, such as its
// certificate contacts, named by the parent whose host the id carries. The
// grammar ([azurermDataPlaneProblem]) has already held the host to an
// argument, so the id is not a constant.
func azurermDataPlaneSingleton(t azurermIDType) bool {
	return len(t.Components) > 1 && t.Components[1].ARMSegment != nil
}
