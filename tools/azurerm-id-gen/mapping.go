// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/ast"
	"strings"
	"unicode"
)

// Classes. A type lands in exactly one.
const (
	// classTemplate: every value segment of the ARM id maps to a
	// configuration argument, or to the provider's subscription.
	classTemplate = "template"
	// classParentID: a leading run of segments is supplied whole by one
	// argument whose value is itself an ARM id (a `<parent>_id`, a scope),
	// and the rest map by name.
	classParentID = "parent-id"
	// classIdentitySchema: the provider publishes a resource identity
	// schema for the type, and #1122 rules that route keeps it. The
	// template is still computed and recorded beside it, so the two
	// sources can be compared rather than one silently replacing the other.
	classIdentitySchema = "identity-schema"
	// classComposite: the id is several ARM ids joined by "|" (or, for the
	// subnet associations, one whole id), derived as azurerm builds it and
	// verified part by part ([compositeRules], GitHub issue #2204).
	classComposite = "composite"
	// classUnmapped: no template, with the reason.
	classUnmapped = "unmapped"
)

// cloudSubscription is the account-derived value: the subscription the
// provider configuration targets. AWS's identity table spells the same
// idea identity.CloudAccountID; this is the Azure counterpart a later unit
// will add as a CloudValue, kept as the string it will have.
const cloudSubscription = "subscription-id"

// component is one piece of an ARM id, shaped like identity.Component so a
// later unit can turn a row into a TypeIdentity without reshaping it:
// Literal is a fixed fragment, Attrs the configuration arguments that
// supply a value (first present wins), Cloud a property of the run.
type component struct {
	Literal string   `json:"literal,omitempty"`
	Attrs   []string `json:"attrs,omitempty"`
	Cloud   string   `json:"cloud,omitempty"`
	// ARMID says the argument's value is itself an ARM id (a parent's id or
	// a scope) standing in for a whole leading run of segments.
	ARMID bool `json:"arm_id,omitempty"`
	// Segment is the parser's own name for the value this component
	// supplies, kept so a reader can check the mapping against the source.
	Segment string `json:"segment,omitempty"`
	// Rule names the mapping rule that chose the argument (see the rule*
	// constants), so a later unit can trust or re-check a row by the rule
	// that made it rather than all-or-nothing.
	Rule string `json:"rule,omitempty"`
	// ARMSegment, on a data-plane row (dataplane.go), says the argument
	// holds an ARM id and this component is one path segment of it.
	ARMSegment *armSegment `json:"arm_segment,omitempty"`
	// URLEscape is the escaping azurerm applies as it formats a URL id,
	// "path" or "query"; OmitIfAbsent leaves an optional argument's segment
	// empty when it is unset.
	URLEscape    string `json:"url_escape,omitempty"`
	OmitIfAbsent bool   `json:"omit_if_absent,omitempty"`
}

// Mapping rules, recorded on each component that names an argument.
const (
	// ruleSubscription: the provider configuration's subscription, or the
	// type's own subscription_id read as its validator says.
	ruleSubscription = "subscription"
	// ruleDataPlane: a data-plane URL id's component, read off azurerm's
	// own id code by the family grammar in dataplane.go.
	ruleDataPlane = "data-plane"
	// ruleSegmentName: the argument is the segment's own name in
	// snake_case (storageAccountName -> storage_account_name), or name for
	// the last segment, or a word-suffix of either.
	ruleSegmentName = "segment-name"
	// ruleParentType: the prefix up to this segment is exactly the id of
	// another azurerm type, and the argument is that type's name with
	// _name or _id (azurerm_spring_cloud_service -> spring_cloud_service_id),
	// or a word-suffix of it.
	ruleParentType = "parent-type"
	// ruleTypeName: a scope argument named after a word-prefix or
	// word-suffix of the resource type's own name
	// (azurerm_maintenance_assignment_virtual_machine -> virtual_machine_id).
	ruleTypeName = "type-name"
	// ruleScopeFallback: a scope read from the conventional
	// target_resource_id or resource_id argument.
	ruleScopeFallback = "scope-fallback"
	// ruleComposite: the argument is named by the composite rule for the
	// type and checked against the part's id type ([compositeRules]).
	ruleComposite = "composite-part"
)

// attrInfo is what the provider schema says about one argument.
type attrInfo struct {
	Required bool
	Optional bool
	Computed bool
}

func (a attrInfo) configurable() bool { return a.Required || a.Optional }

// typeSchema is one resource type's provider schema, reduced to what the
// mapping checks.
type typeSchema struct {
	Attrs    map[string]attrInfo
	Identity []string // resource identity schema attributes, nil when none
	// IdentityOptional is the identity attributes not required for import
	// (none at azurerm 5.10.0), which the identity-schema route reads
	// differently ([assignRoutes]).
	IdentityOptional map[string]bool
}

// mapping is the outcome for one type, before the class is settled.
type mapping struct {
	Components []component
	ParentID   bool
	Reason     string // set when unmapped: groupable
	Detail     string // set when unmapped: specific
}

// subscriptionForm says how a type's own subscription_id argument is
// spelled, read off the validator the resource's schema declares for it in
// the same file: "guid", "arm-id", or "" when the source does not say.
type subscriptionForm string

// mapCtx is everything mapSegments consults besides the segments.
type mapCtx struct {
	Type    string // the resource type being mapped
	Schema  typeSchema
	SubForm subscriptionForm
	// ArgForms is [argFormsIn] over the resource's file: an argument whose
	// validator parses a full ARM id never supplies one segment's value.
	ArgForms map[string]subscriptionForm
	// ParentTypes returns the azurerm types whose own id template is
	// exactly prefix (static segments compared case-insensitively, value
	// segments by position only). Nil means no index.
	ParentTypes func(prefix []segment) []string
	// TypeTemplate returns another azurerm type's normalized id template,
	// so a candidate named after a type can be refused when that type's id
	// is not the prefix it would stand for. Nil means no index.
	TypeTemplate func(typeName string) (string, bool)
}

type cand struct {
	attr, rule string
	// own marks the segment's own full spelling (image_name for imageName),
	// which is never refused for sharing a name with another type.
	own bool
}

// mapSegments turns an id template into components against one type's
// schema. Each value segment is tried in order:
//
//   - subscription: the type's own subscription_id when it declares one as
//     a GUID; otherwise the provider configuration's subscription,
//     account-derived. A subscription_id holding a full id is a parent-id.
//   - resourceGroups: resource_group_name, or name when it is the last
//     segment (azurerm_resource_group itself).
//   - the last user segment: name, else the segment's snake_case name.
//   - a parent user segment: `<segment>_name`, or `<parent type>_name`
//     when the prefix is another type's id.
//   - a scope: an argument whose value is the scope's ARM id.
//
// When a value segment maps to nothing, the deepest parent segment at or
// after it whose `<segment>_id` or `<parent type>_id` argument exists
// supplies the whole prefix, and the rest must still map. Only arguments the
// schema declares as required or optional are ever mapped, and no argument
// is used twice.
func mapSegments(segs []segment, ctx mapCtx) mapping {
	for _, s := range segs {
		if s.Kind == segDataPlane {
			return mapping{Reason: "data-plane id (a URL, not an ARM path)", Detail: "segment " + s.Name}
		}
	}
	var values []int
	for i, s := range segs {
		if s.Kind != segStatic && s.Kind != segProvider {
			values = append(values, i)
		}
	}
	if len(values) == 0 {
		return mapping{Reason: "id has no value segments", Detail: "a fixed path"}
	}
	last := values[len(values)-1]

	// namesOtherType reports a candidate `<x>_id` / `<x>_name` whose
	// azurerm_<x> is a type with a different id than segs[:k+1]: it names
	// an ancestor or a sibling, never this parent. azurerm_api_management_gateway's
	// word-prefix api_management_id is the service, not the gateway.
	namesOtherType := func(c cand, k int) bool {
		if ctx.TypeTemplate == nil {
			return false
		}
		base := strings.TrimSuffix(strings.TrimSuffix(c.attr, "_id"), "_name")
		tmpl, ok := ctx.TypeTemplate("azurerm_" + base)
		return ok && tmpl != normalizedTemplate(segs[:k+1])
	}
	filterOther := func(cands []cand, k int) []cand {
		var out []cand
		for _, c := range cands {
			if c.own || !namesOtherType(c, k) {
				out = append(out, c)
			}
		}
		return out
	}

	parentTypes := func(k int) []string {
		if ctx.ParentTypes == nil {
			return nil
		}
		return ctx.ParentTypes(segs[:k+1])
	}

	// pick returns the first candidate the schema declares configurable and
	// that is not already used.
	//
	// A candidate also matches an argument spelled with fewer underscores
	// (load_balancer_id finds azurerm_lb_probe's loadbalancer_id; iot_hub
	// finds iothub), but only when exactly one argument squashes to it.
	squashed := map[string][]string{}
	for a, info := range ctx.Schema.Attrs {
		if info.configurable() {
			k := strings.ReplaceAll(a, "_", "")
			squashed[k] = append(squashed[k], a)
		}
	}
	pick := func(cands []cand, used map[string]bool) (cand, bool) {
		for _, c := range cands {
			if ctx.Schema.Attrs[c.attr].configurable() && !used[c.attr] {
				return c, true
			}
		}
		for _, c := range cands {
			if m := squashed[strings.ReplaceAll(c.attr, "_", "")]; len(m) == 1 && !used[m[0]] {
				return cand{attr: m[0], rule: c.rule}, true
			}
		}
		return cand{}, false
	}

	// direct maps one value segment by name, or reports what it tried.
	direct := func(i int, used map[string]bool) (component, []cand, bool) {
		s := segs[i]
		sn := snake(s.Name)
		var cands []cand
		switch s.Kind {
		case segSubscription:
			if ctx.Schema.Attrs["subscription_id"].configurable() {
				if ctx.SubForm == "guid" {
					// A GUID subscription_id is a property of the object,
					// not the subscription it is created in (#1120 unit
					// 13). At 5.10.0 the three automation connection types
					// are the only ones that declare one with a segment to
					// fill, and each builds its id from the client's
					// subscription and sends subscription_id as a field
					// value (automation_connection_certificate_resource.go
					// lines 80-84 and 108).
					return component{Cloud: cloudSubscription, Segment: s.Name, Rule: ruleSubscription}, nil, true
				}
				// A full-id or undetermined subscription_id is the parent-id
				// pass's, or reported.
				return component{}, []cand{{attr: "subscription_id (" + formOrUnknown(ctx.SubForm) + ")", rule: ruleSubscription}}, false
			}
			return component{Cloud: cloudSubscription, Segment: s.Name, Rule: ruleSubscription}, nil, true
		case segResourceGroup:
			if i == last {
				cands = []cand{{attr: "name", rule: ruleSegmentName}, {attr: "resource_group_name", rule: ruleSegmentName}}
			} else {
				cands = []cand{{attr: "resource_group_name", rule: ruleSegmentName}}
			}
		case segUser, segConstant:
			if i == last {
				cands = []cand{{attr: "name", rule: ruleSegmentName}, {attr: sn, rule: ruleSegmentName}, {attr: withSuffix(sn, "_name"), rule: ruleSegmentName}}
				if strings.HasSuffix(sn, "_id") {
					// A segment the parser calls an Id is matched by its
					// own spelling first: azurerm_role_definition's
					// roleDefinitionId is role_definition_id, and its name
					// argument is the display name. A segment called a Name
					// keeps name first: azurerm_logic_app_integration_account_assembly's
					// assembly_name is a property, not the ARM name.
					cands[0], cands[1] = cands[1], cands[0]
				}
			} else {
				cands = nameCandidates(sn, parentTypes(i), "_name")
				cands = filterOther(append(cands, cand{attr: sn, rule: ruleSegmentName, own: true}), i)
			}
		case segScope, segResourceID:
			cands = scopeCandidates(sn, ctx.Type)
			if c, ok := pick(cands, used); ok {
				return component{Attrs: []string{c.attr}, ARMID: true, Segment: s.Name, Rule: c.rule}, nil, true
			}
			return component{}, cands, false
		}
		// An argument that holds a full ARM id cannot be one segment's
		// value (#1120 unit 13): network_manager_management_group_connection's
		// management_group_id is /providers/Microsoft.Management/managementGroups/<name>,
		// validated by validate.ManagementGroupID, and mapping it to the
		// managementGroupId segment would render that prefix twice. Such an
		// argument is the parent-id pass's to use, as a whole prefix.
		var single []cand
		for _, c := range cands {
			if ctx.ArgForms[c.attr] != "arm-id" {
				single = append(single, c)
			}
		}
		if c, ok := pick(single, used); ok {
			return component{Attrs: []string{c.attr}, Segment: s.Name, Rule: c.rule}, nil, true
		}
		return component{}, cands, false
	}

	// build assembles components for segments from index `from`, prefixed
	// by `head` (nil for the whole id).
	build := func(from int, head []component) (mapping, int, []cand) {
		used := map[string]bool{}
		out := append([]component(nil), head...)
		for _, c := range head {
			for _, a := range c.Attrs {
				used[a] = true
			}
		}
		for i := from; i < len(segs); i++ {
			s := segs[i]
			if s.Kind == segStatic || s.Kind == segProvider {
				out = appendLiteral(out, "/"+s.Value)
				continue
			}
			c, tried, ok := direct(i, used)
			if !ok {
				return mapping{}, i, tried
			}
			for _, a := range c.Attrs {
				used[a] = true
			}
			if !c.ARMID {
				// A scope is a whole ARM id with its own leading slash.
				out = appendLiteral(out, "/")
			}
			out = append(out, c)
		}
		return mapping{Components: out}, -1, nil
	}

	m, failAt, tried := build(0, nil)
	if failAt < 0 {
		for _, c := range m.Components {
			if c.ARMID {
				m.ParentID = true
			}
		}
		return m
	}

	// Parent-id pass: the deepest parent segment at or after the failure
	// whose id argument exists supplies everything up to it.
	for vi := len(values) - 2; vi >= 0; vi-- {
		k := values[vi]
		if k < failAt {
			break // a prefix ending before the failure cannot cover it
		}
		s := segs[k]
		var cands []cand
		switch s.Kind {
		case segUser, segConstant:
			cands = filterOther(nameCandidates(snake(s.Name), parentTypes(k), "_id"), k)
		case segResourceGroup:
			cands = []cand{{attr: "resource_group_id", rule: ruleSegmentName}}
		case segSubscription:
			if ctx.SubForm == "arm-id" {
				cands = []cand{{attr: "subscription_id", rule: ruleSubscription}}
			}
		}
		c, ok := pick(cands, nil)
		if !ok {
			continue
		}
		head := []component{{Attrs: []string{c.attr}, ARMID: true, Segment: s.Name, Rule: c.rule}}
		pm, at, t := build(k+1, head)
		if at < 0 {
			pm.ParentID = true
			return pm
		}
		if at > failAt {
			// Report the furthest any attempt got: a parent id that maps
			// but a singleton child with no name argument is a
			// last-segment failure, not a resource-group one.
			failAt, tried = at, t
		}
	}

	s := segs[failAt]
	var names []string
	for _, c := range tried {
		names = append(names, c.attr)
	}
	return mapping{
		Reason: "no argument for the " + segRole(s, failAt == last),
		Detail: fmt.Sprintf("segment %q; tried %s", s.Name, strings.Join(dedupe(names), ", ")),
	}
}

// nameCandidates lists the arguments a parent segment may be supplied by,
// with the given suffix (_name or _id): the segment's own base name, then
// each parent type's name, then the word-suffixes of each (a parent
// segment flexibleServerName may surface as server_name; a parent type
// azurerm_new_relic_monitor as monitor_id). Exact spellings come before
// any suffix so a longer, more specific match always wins.
func nameCandidates(sn string, parentTypes []string, suffix string) []cand {
	base := strings.TrimSuffix(strings.TrimSuffix(sn, "_name"), "_id")
	var exact, partial []cand
	add := func(words []string, rule string) {
		for i := range words {
			c := cand{attr: strings.Join(words[i:], "_") + suffix, rule: rule, own: i == 0 && rule == ruleSegmentName}
			if i == 0 {
				exact = append(exact, c)
			} else {
				partial = append(partial, c)
			}
		}
	}
	add(strings.Split(base, "_"), ruleSegmentName)
	var prefixes []cand
	for _, pt := range parentTypes {
		words := strings.Split(strings.TrimPrefix(pt, "azurerm_"), "_")
		add(words, ruleParentType)
		// Word-prefixes of a parent type, longest first, last of all:
		// azurerm_logic_app_workflow's children take logic_app_id.
		for i := len(words) - 1; i >= 1; i-- {
			prefixes = append(prefixes, cand{attr: strings.Join(words[:i], "_") + suffix, rule: ruleParentType})
		}
	}
	return dedupeCands(append(append(exact, partial...), prefixes...))
}

// scopeCandidates lists the arguments a scope segment may be supplied by.
// The type-name rule covers the families that name the scope after the
// object they attach to (azurerm_function_app_connection's function_app_id,
// azurerm_consumption_budget_resource_group's resource_group_id); the
// fallbacks are the provider's two conventional spellings, last because a
// type may carry one of them for a different purpose.
func scopeCandidates(sn, typeName string) []cand {
	base := strings.TrimSuffix(sn, "_id")
	cands := []cand{{attr: "scope", rule: ruleSegmentName}, {attr: sn, rule: ruleSegmentName}, {attr: base + "_id", rule: ruleSegmentName}}
	words := strings.Split(strings.TrimPrefix(typeName, "azurerm_"), "_")
	for i := len(words) - 1; i >= 1; i-- {
		cands = append(cands, cand{attr: strings.Join(words[:i], "_") + "_id", rule: ruleTypeName})
	}
	for i := 1; i < len(words); i++ {
		cands = append(cands, cand{attr: strings.Join(words[i:], "_") + "_id", rule: ruleTypeName})
	}
	cands = append(cands, cand{attr: "target_resource_id", rule: ruleScopeFallback}, cand{attr: "resource_id", rule: ruleScopeFallback})
	return dedupeCands(cands)
}

func dedupeCands(in []cand) []cand {
	seen := map[string]bool{}
	var out []cand
	for _, c := range in {
		if c.attr != "" && !seen[c.attr] {
			seen[c.attr] = true
			out = append(out, c)
		}
	}
	return out
}

func formOrUnknown(f subscriptionForm) string {
	if f == "" {
		return "form undetermined"
	}
	return string(f)
}

// segRole names a segment's role for a groupable reason.
func segRole(s segment, isLast bool) string {
	switch s.Kind {
	case segSubscription:
		return "subscription segment"
	case segResourceGroup:
		return "resource group segment"
	case segScope, segResourceID:
		return "scope segment"
	}
	if isLast {
		return "last (name) segment"
	}
	return "parent segment"
}

// normalizedTemplate keys the parent-type index: static segments
// lowercased (ARM path keys are case-insensitive, and the hand parsers and
// resourceids types do not always agree on casing), value segments by kind
// position only.
func normalizedTemplate(segs []segment) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.Kind {
		case segStatic, segProvider:
			b.WriteString("/" + strings.ToLower(s.Value))
		default:
			b.WriteString("/{}")
		}
	}
	return b.String()
}

func withSuffix(s, suffix string) string {
	if strings.HasSuffix(s, suffix) {
		return s
	}
	return s + suffix
}

func appendLiteral(out []component, lit string) []component {
	if n := len(out); n > 0 && out[n-1].Literal != "" && len(out[n-1].Attrs) == 0 && out[n-1].Cloud == "" {
		out[n-1].Literal += lit
		return out
	}
	return append(out, component{Literal: lit})
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// snake converts a parser's camelCase segment name to the provider's
// snake_case argument spelling: storageAccountName -> storage_account_name,
// VMName -> vm_name.
func snake(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) {
			prevLower := i > 0 && (unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]))
			nextLower := i+1 < len(r) && unicode.IsLower(r[i+1])
			prevUpper := i > 0 && unicode.IsUpper(r[i-1])
			if i > 0 && (prevLower || (prevUpper && nextLower)) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// template renders segments as the ARM id template string the artifact
// records, with value segments as {name}.
func template(segs []segment) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.Kind {
		case segStatic, segProvider:
			b.WriteString("/" + s.Value)
		case segScope, segResourceID, segDataPlane:
			b.WriteString("{" + s.Name + "}")
		default:
			b.WriteString("/{" + s.Name + "}")
		}
	}
	return b.String()
}

// subscriptionFormIn is [argFormsIn]'s answer for subscription_id.
func subscriptionFormIn(f *ast.File) subscriptionForm {
	return argFormsIn(f)["subscription_id"]
}

// argFormsIn reads the validator a resource's schema declares for each
// argument in one file, `"arg": {..., ValidateFunc: X}`, as the form of the
// value it accepts: a UUID validator means a bare GUID ("guid"); any other
// validator whose last name ends in ID or Id (commonids.ValidateSubscriptionID,
// validate.ManagementGroupID, api.ValidateApiID) parses a full ARM id
// ("arm-id"). An argument with no validator, or one this does not
// recognise, is absent. The first declaration of a key in the file wins.
func argFormsIn(f *ast.File) map[string]subscriptionForm {
	out := map[string]subscriptionForm{}
	if f == nil {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := stringLit(kv.Key)
		if !ok {
			return true
		}
		if _, done := out[key]; done {
			return true
		}
		var form subscriptionForm
		found := false
		if cl, ok := kv.Value.(*ast.CompositeLit); ok {
			for _, e := range cl.Elts {
				inner, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				id, ok := inner.Key.(*ast.Ident)
				if !ok || (id.Name != "ValidateFunc" && id.Name != "ValidateDiagFunc") {
					continue
				}
				found = true
				form = validatorForm(exprText(inner.Value))
				break
			}
		}
		if found && form != "" {
			out[key] = form
		}
		return true
	})
	return out
}

// validatorForm classifies a validator by its rendered names ([exprText]).
func validatorForm(text string) subscriptionForm {
	if strings.Contains(text, "UUID") {
		return "guid"
	}
	names := strings.Split(strings.TrimSuffix(text, "."), ".")
	last := names[len(names)-1]
	if strings.HasSuffix(last, "ID") || strings.HasSuffix(last, "Id") {
		return "arm-id"
	}
	return ""
}

// exprText is a cheap rendering of an expression's identifiers, enough to
// recognise a validator by name.
func exprText(e ast.Expr) string {
	var b strings.Builder
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			b.WriteString(id.Name)
			b.WriteByte('.')
		}
		return true
	})
	return b.String()
}
