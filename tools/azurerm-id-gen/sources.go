// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"
)

// GitHub issue #2138, the maintainer's option (a), 2026-10-20: for an
// identity-schema type, where the record rung reads each identity attribute
// from. An identity schema names attributes; a record has to hold a value
// for every one of them, and 93 of azurerm 5.10.0's 196 identity-schema
// types name one their block does not declare: the parent's name, which
// the configuration states inside a parent's ARM id (storage_account_name
// inside storage_account_id), a renamed argument (dns_zone_name is
// zone_name) or a constant (record_type is "CNAME").
//
// Each source here is stated by azurerm twice, and recorded only where the
// two agree:
//
//   - which segment of the type's own id an attribute is, by azurerm's own
//     rule for naming identity attributes after id segments
//     (internal/tf/pluginsdk/resource_identity.go's identitySchema and
//     SegmentName, ported as [identityAttrNames]), checked against the
//     identity schema the provider serves;
//   - where the value comes from, by the resource identity test azurerm
//     generates for the type ([contract]): an argument, the provider's
//     subscription, a constant, or a value inside an argument holding an
//     ARM id. For the last, the id type that argument is parsed as is the
//     one the argument's own validator parses ([loader.argIDType]), and the
//     attribute's segment has to sit at the same position, under the same
//     path, in that parent id as in the type's own.
//
// A type for which either statement is missing or they disagree carries
// [entry.IdentitySourcesProblem] instead, and stays refused by name.

// identitySource is one identity attribute's source. Segment is always set;
// exactly one of Argument, Within, Provider and Constant is.
type identitySource struct {
	// Segment is the type's own id segment this attribute is, under the
	// parser's name ({storageAccountName}).
	Segment string `json:"segment"`
	// Argument: the value is this argument's (dns_zone_name's is
	// zone_name's).
	Argument string `json:"argument,omitempty"`
	// Within: the value is segment ParentSegment of this argument's value,
	// an ARM id parsed as ParentIDType, whose template is ParentTemplate.
	Within         string `json:"within,omitempty"`
	ParentIDType   string `json:"parent_id_type,omitempty"`
	ParentTemplate string `json:"parent_template,omitempty"`
	ParentSegment  string `json:"parent_segment,omitempty"`
	// ParentSegments is ParentTemplate segment by segment, each with its
	// kind as the parser declares it ([parentPart]), and ParentParse is how
	// azurerm parses the argument: "strict" through its generated
	// ParseXxxID (every ValidateXxxID, and commonschema's
	// ResourceIDReference helpers, which parse with insensitively false),
	// "insensitive" where the argument's validator names a
	// ParseXxxIDInsensitively (validation.AsGeneratedID(commonids.ParseKubernetesClusterIDInsensitively)
	// on azurerm_kubernetes_cluster_node_pool's kubernetes_cluster_id at
	// 5.10.0). The record rung parses the argument's value as
	// go-azure-helpers' resourceids.Parser.Parse does with that flag
	// (substrate.ARMIDSegment), GitHub issue #2140.
	ParentSegments []parentPart `json:"parent_segments,omitempty"`
	ParentParse    string       `json:"parent_parse,omitempty"`
	// Provider: the provider configuration's value ([cloudSubscription]).
	Provider string `json:"provider,omitempty"`
	// Constant: a fixed value, the same for every object of the type.
	Constant string `json:"constant,omitempty"`
}

// parentPart is one path segment of a parent id as resourceids parses it:
// Kind "fixed" (a StaticSegment or ResourceProviderSegment, whose text is
// Value), "constant" (a ConstantSegment, which must be one of Values) or
// "value" (a subscription, resource group or user-specified segment, any
// non-empty text, named Name).
type parentPart struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name,omitempty"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

const (
	partFixed    = "fixed"
	partConstant = "constant"
	partValue    = "value"

	parseStrict      = "strict"
	parseInsensitive = "insensitive"
)

// parentParts is segs as [parentPart]s, or why they cannot be: a scope, a
// resource id or a data-plane segment spans several path segments, which a
// positional parse cannot place, and a constant whose possible values were
// not read cannot be checked.
func parentParts(segs []segment) ([]parentPart, string) {
	out := make([]parentPart, 0, len(segs))
	for _, s := range segs {
		switch s.Kind {
		case segStatic, segProvider:
			out = append(out, parentPart{Kind: partFixed, Value: s.Value})
		case segConstant:
			if len(s.Values) == 0 {
				return nil, fmt.Sprintf("its constant segment {%s} states no possible values this generator reads", s.Name)
			}
			out = append(out, parentPart{Kind: partConstant, Name: s.Name, Values: s.Values})
		case segSubscription, segResourceGroup, segUser:
			out = append(out, parentPart{Kind: partValue, Name: s.Name})
		default:
			return nil, fmt.Sprintf("its %s segment {%s} can span several path segments", s.Kind, s.Name)
		}
	}
	return out, ""
}

// ruleIdentityTest names a component a template took from
// [identitySources] rather than from the segment's name: azurerm's identity
// test is what says which argument supplies it.
const ruleIdentityTest = "identity-test"

// identityAttrNames is azurerm's resource identity schema for an id type,
// by segment index: what pluginsdk.GenerateIdentitySchema names each
// segment it keeps (resource_identity.go, identitySchema and SegmentName at
// 5.10.0). It keeps subscription, resource group, user-specified and
// constant segments; names each by its snake_case name
// ([azurermToSnakeCase]) with two fixed renames; and, unless the resource
// asks for the "virtual" form, names a last segment ending in Name "name".
// Static and provider segments are names of nothing. A scope, a resource
// id or a data-plane segment answers ok false: no identity attribute holds
// it, so an identity cannot name such an object whole.
func identityAttrNames(segs []segment, virtual bool) (map[int]string, string) {
	out := map[int]string{}
	for i, s := range segs {
		switch s.Kind {
		case segStatic, segProvider:
			continue
		case segSubscription, segResourceGroup, segUser, segConstant:
		default:
			return nil, fmt.Sprintf("its id has a %s segment {%s}, which azurerm's identity schema holds in no attribute", s.Kind, s.Name)
		}
		name := azurermToSnakeCase(s.Name)
		if !virtual && i == len(segs)-1 && strings.HasSuffix(s.Name, "Name") {
			name = "name"
		}
		switch name {
		case "signal_r_name":
			name = "signalr_name"
		case "web_pub_sub_name":
			name = "web_pubsub_name"
		}
		out[i] = name
	}
	return out, ""
}

// azurermToSnakeCase is pluginsdk.ToSnakeCase at azurerm 5.10.0
// (internal/tf/pluginsdk/resource_identity.go), byte for byte in
// behaviour: strcase's snake case with a version token kept whole
// (Gen2v2 is gen2_v2). It is not [snake], which is this generator's own
// guess at argument spellings and is allowed to differ.
func azurermToSnakeCase(input string) string {
	const delimiter = '_'
	input = strings.TrimSpace(input)
	var n strings.Builder
	last := func() byte {
		s := n.String()
		if s == "" {
			return 0
		}
		return s[len(s)-1]
	}
	for i := 0; i < len(input); i++ {
		v := input[i]
		vIsCap := v >= 'A' && v <= 'Z'
		vIsLow := v >= 'a' && v <= 'z'
		if vIsCap {
			v += 'a' - 'A'
		}
		if i+1 < len(input) {
			next := input[i+1]
			vIsNum := v >= '0' && v <= '9'
			nextIsCap := next >= 'A' && next <= 'Z'
			nextIsLow := next >= 'a' && next <= 'z'
			nextIsNum := next >= '0' && next <= '9'
			if (vIsLow || vIsCap) && nextIsNum && (v == 'v' || v == 'V') {
				if n.Len() > 0 && last() != delimiter {
					n.WriteByte(delimiter)
				}
				n.WriteByte(v)
				continue
			}
			if (vIsCap && (nextIsLow || nextIsNum)) || (vIsLow && (nextIsCap || nextIsNum)) || (vIsNum && (nextIsCap || nextIsLow)) {
				if vIsCap && nextIsLow {
					if i > 0 && input[i-1] >= 'A' && input[i-1] <= 'Z' {
						n.WriteByte(delimiter)
					}
				}
				n.WriteByte(v)
				if vIsLow || vIsNum || nextIsNum {
					n.WriteByte(delimiter)
				}
				continue
			}
		}
		if v == ' ' || v == '_' || v == '-' || v == '.' {
			n.WriteByte(delimiter)
		} else {
			n.WriteByte(v)
		}
	}
	return n.String()
}

// identitySegments maps each identity attribute ts serves to the index of
// the segment of segs it is, by [identityAttrNames] in whichever of its two
// forms reproduces the served schema exactly. Neither reproducing it is a
// problem: the schema is then not this id's segments, and nothing says
// which segment an attribute is.
func identitySegments(segs []segment, identity []string) (map[string]int, string) {
	want := map[string]bool{}
	for _, a := range identity {
		want[a] = true
	}
	var tried []string
	for _, virtual := range []bool{false, true} {
		names, problem := identityAttrNames(segs, virtual)
		if problem != "" {
			return nil, problem
		}
		out := map[string]int{}
		for i, n := range names {
			out[n] = i
		}
		if len(out) == len(names) && len(out) == len(want) {
			match := true
			for n := range out {
				if !want[n] {
					match = false
				}
			}
			if match {
				return out, ""
			}
		}
		var got []string
		for _, n := range names {
			got = append(got, n)
		}
		sort.Strings(got)
		tried = append(tried, strings.Join(got, ", "))
	}
	return nil, fmt.Sprintf("the identity schema azurerm serves (%s) is not its id's segments as azurerm names them (%s)", strings.Join(identity, ", "), tried[0])
}

// identitySources is [entry.IdentitySources] for one identity-schema type:
// every identity attribute's source, or why there is none. segs is the
// type's own id, the one its row's template is.
func (l *loader) identitySources(ts typeSchema, r registration, segs []segment, c *contract) (map[string]identitySource, string) {
	if segs == nil {
		return nil, "azurerm's id parser for it states no segments this generator reads, so no identity attribute can be placed in its id"
	}
	if c == nil {
		return nil, "azurerm publishes no resource identity test for it, so nothing says where its identity attributes come from"
	}
	at, problem := identitySegments(segs, ts.Identity)
	if problem != "" {
		return nil, problem
	}
	out := map[string]identitySource{}
	for _, attr := range ts.Identity {
		i := at[attr]
		seg := segs[i]
		check, ok := c.Attributes[attr]
		if !ok {
			return nil, fmt.Sprintf("azurerm's identity test (%s) does not say where %s comes from", c.Source, attr)
		}
		src := identitySource{Segment: seg.Name}
		switch {
		case check.Argument != "":
			if !ts.Attrs[check.Argument].configurable() {
				return nil, fmt.Sprintf("azurerm's identity test (%s) reads %s from %s, which a configuration does not set", c.Source, attr, check.Argument)
			}
			src.Argument = check.Argument
		case check.ProviderSubscription:
			if seg.Kind != segSubscription {
				return nil, fmt.Sprintf("azurerm's identity test (%s) expects %s to be the provider's subscription, and its segment {%s} is a %s segment", c.Source, attr, seg.Name, seg.Kind)
			}
			src.Provider = cloudSubscription
		case check.Constant != "":
			if seg.Kind != segConstant {
				return nil, fmt.Sprintf("azurerm's identity test (%s) expects %s to be %q, and its segment {%s} is a %s segment, not a constant one", c.Source, attr, check.Constant, seg.Name, seg.Kind)
			}
			src.Constant = check.Constant
		case check.Within == "id":
			return nil, fmt.Sprintf("azurerm's identity test (%s) finds %s inside id, the object's own id, which no argument of a configuration supplies", c.Source, attr)
		case check.Within != "":
			if !ts.Attrs[check.Within].configurable() {
				return nil, fmt.Sprintf("azurerm's identity test (%s) finds %s inside %s, which no argument of a configuration supplies", c.Source, attr, check.Within)
			}
			parent, parse, problem := l.argIDType(r, check.Within)
			if problem != "" {
				return nil, fmt.Sprintf("azurerm's identity test (%s) finds %s inside %s, and %s", c.Source, attr, check.Within, problem)
			}
			if !samePathThrough(segs, parent.Segments, i) {
				return nil, fmt.Sprintf("azurerm's identity test (%s) finds %s inside %s, parsed as %s.%s (%s), and that id does not reach segment {%s} by the path %s's own id does", c.Source, attr, check.Within, parent.Package, parent.TypeName, template(parent.Segments), seg.Name, template(segs))
			}
			if parent.Kind != "resourceids" {
				return nil, fmt.Sprintf("azurerm's identity test (%s) finds %s inside %s, parsed as %s.%s, a %s whose parse this generator does not model", c.Source, attr, check.Within, parent.Package, parent.TypeName, parent.Kind)
			}
			parts, problem := parentParts(parent.Segments)
			if problem != "" {
				return nil, fmt.Sprintf("azurerm's identity test (%s) finds %s inside %s, parsed as %s.%s, and %s", c.Source, attr, check.Within, parent.Package, parent.TypeName, problem)
			}
			src.Within = check.Within
			src.ParentIDType = parent.Package + "." + parent.TypeName
			src.ParentTemplate = template(parent.Segments)
			src.ParentSegment = parent.Segments[i].Name
			src.ParentSegments = parts
			src.ParentParse = parse
		default:
			return nil, fmt.Sprintf("azurerm's identity test (%s) checks %s with %s, which names no source", c.Source, attr, check.Other)
		}
		out[attr] = src
	}
	return out, ""
}

// samePathThrough reports whether parent reaches segment i by exactly the
// path own does: every segment up to and including i is a fixed segment
// with the same text, compared as ARM compares ids, without regard to
// case, or a value segment in both. Then the value at position i of an id
// parsed as parent is the value own's segment i holds.
//
// The value kinds are one class here because each matches exactly one path
// segment when resourceids parses an id: go-azure-sdk's LoadBalancerId
// declares its resource group a UserSpecifiedSegment where its ProbeId
// declares a ResourceGroupSegment, and both parse
// /resourceGroups/<name>/ the same way. A scope, a resource id or a
// data-plane segment, which can span several, never matches.
func samePathThrough(own, parent []segment, i int) bool {
	if i >= len(own) || i >= len(parent) {
		return false
	}
	for k := 0; k <= i; k++ {
		a, b := own[k], parent[k]
		af, bf := fixedSegment(a), fixedSegment(b)
		if af != bf {
			return false
		}
		if af {
			if !strings.EqualFold(a.Value, b.Value) {
				return false
			}
			continue
		}
		if !oneValueSegment(a) || !oneValueSegment(b) {
			return false
		}
	}
	return true
}

func fixedSegment(s segment) bool { return s.Kind == segStatic || s.Kind == segProvider }

func oneValueSegment(s segment) bool {
	switch s.Kind {
	case segSubscription, segResourceGroup, segUser, segConstant:
		return true
	}
	return false
}

// argIDType is the id type azurerm parses argument arg of r's resource as:
// the one id type, stating its segments, that the argument's declaration
// reaches through its validator (`ValidateFunc: commonids.ValidateStorageAccountID`)
// or, for a schema built by a helper, through the helper call's own
// arguments (`commonschema.ResourceIDReferenceRequiredForceNew(&senderusernames.DomainId{})`).
// Only the resource's own files are read, its registration's and its
// Read's: another resource in the package may declare the same argument
// name for another id.
//
// parse is [parseInsensitive] when the validator expression itself, or a
// function of the checkout it calls, names a ParseXxxIDInsensitively, and
// [parseStrict] otherwise: go-azure-sdk's ValidateXxxID calls ParseXxxID,
// and commonschema's ResourceIDReference helpers call resourceids'
// Parse(value, false) (resource_id_reference.go, tryParsingResourceID).
func (l *loader) argIDType(r registration, arg string) (*idSource, string, string) {
	files := []*ast.File{r.File}
	pkgs := []*pkg{r.Pkg}
	if r.ReadFile != nil && r.ReadFile != r.File {
		files = append(files, r.ReadFile)
		pkgs = append(pkgs, r.ReadPkg)
	}
	var found []typeKey
	declared, insensitive := false, false
	for k, f := range files {
		p := pkgs[k]
		if f == nil || p == nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			if key, ok := stringLit(kv.Key); !ok || key != arg {
				return true
			}
			var expr ast.Node
			switch v := kv.Value.(type) {
			case *ast.CompositeLit:
				for _, e := range v.Elts {
					inner, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if id, ok := inner.Key.(*ast.Ident); ok && (id.Name == "ValidateFunc" || id.Name == "ValidateDiagFunc") {
						expr = inner.Value
					}
				}
			case *ast.CallExpr:
				expr = v
			}
			if expr == nil {
				return true
			}
			declared = true
			if l.namesInsensitiveParse(p, f, expr, 0) {
				insensitive = true
			}
			l.collect(p, f, expr, 0, map[*ast.FuncDecl]bool{}, &found)
			return false
		})
	}
	if !declared {
		return nil, "", fmt.Sprintf("the resource declares no validator for %s, so which id azurerm parses it as is not stated", arg)
	}
	parse := parseStrict
	if insensitive {
		parse = parseInsensitive
	}
	var withSegs []*idSource
	var names []string
	for _, k := range found {
		src, problem := l.segmentsOf(k.p, k.name)
		if problem != "" || src.Segments == nil {
			continue
		}
		withSegs = append(withSegs, src)
		names = append(names, k.name)
	}
	switch len(withSegs) {
	case 0:
		return nil, "", fmt.Sprintf("%s's validator reaches no id type that states its segments", arg)
	case 1:
		return withSegs[0], parse, ""
	}
	return nil, "", fmt.Sprintf("%s's validator reaches several id types (%s), so which one azurerm parses it as is a choice", arg, strings.Join(names, ", "))
}

// namesInsensitiveParse reports whether n names a function whose name ends
// in "Insensitively" (go-azure-sdk's ParseXxxIDInsensitively), looking
// through the bodies of functions of the checkout n calls, two deep.
func (l *loader) namesInsensitiveParse(p *pkg, f *ast.File, n ast.Node, depth int) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found {
			return false
		}
		var name string
		switch e := x.(type) {
		case *ast.SelectorExpr:
			name = e.Sel.Name
		case *ast.Ident:
			name = e.Name
		default:
			return true
		}
		if strings.HasSuffix(name, "Insensitively") {
			found = true
			return false
		}
		if depth >= 2 {
			return true
		}
		var expr ast.Expr
		switch e := x.(type) {
		case *ast.SelectorExpr:
			expr = e
		case *ast.Ident:
			expr = e
		}
		if fp, ff, fd := l.funcRef(p, f, expr); fd != nil && fd.Body != nil && fp != nil && !strings.Contains(fp.path, "/vendor/") && strings.HasPrefix(fp.path, l.module) {
			if l.namesInsensitiveParse(fp, ff, fd.Body, depth+1) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// sourcedComponents is the template the identity sources make of an id
// segs that [mapSegments] could not map by name (#2138): each value segment
// read from where azurerm's identity test says its attribute comes from.
// A renamed argument is its argument (dns_zone_name's segment reads
// zone_name); a constant is literal text (record_type's "CNAME"); the
// provider's subscription is the cloud value; and a leading run of segments
// whose attributes are all inside one argument is that argument's whole ARM
// id, exactly when the id it is parsed as is that run and no more. Anything
// else (a parent id read for a segment after the id has begun, a parent id
// longer than the run, the object's own name inside a parent's id) answers
// ok false, and the row keeps [mapSegments]'s reason.
func sourcedComponents(segs []segment, at map[string]int, sources map[string]identitySource) ([]component, bool) {
	attrAt := map[int]string{}
	for a, i := range at {
		attrAt[i] = a
	}
	last := -1
	lead := -1
	var leadArg, leadTemplate string
	for i, s := range segs {
		if s.Kind == segStatic || s.Kind == segProvider {
			continue
		}
		last = i
		src := sources[attrAt[i]]
		if src.Within == "" {
			continue
		}
		if leadArg == "" {
			leadArg, leadTemplate = src.Within, src.ParentTemplate
		}
		if src.Within != leadArg || src.ParentTemplate != leadTemplate {
			return nil, false
		}
		lead = i
	}
	if last < 0 || lead == last {
		return nil, false
	}
	var out []component
	start := 0
	if lead >= 0 {
		for i := 0; i <= lead; i++ {
			s := segs[i]
			if s.Kind != segStatic && s.Kind != segProvider && sources[attrAt[i]].Within == "" {
				return nil, false
			}
		}
		if templateShape(template(segs[:lead+1])) != templateShape(leadTemplate) {
			return nil, false
		}
		out = append(out, component{Attrs: []string{leadArg}, ARMID: true, Segment: segs[lead].Name, Rule: ruleIdentityTest})
		start = lead + 1
	}
	for i := start; i < len(segs); i++ {
		s := segs[i]
		switch s.Kind {
		case segStatic, segProvider:
			out = appendLiteral(out, "/"+s.Value)
			continue
		}
		src := sources[attrAt[i]]
		switch {
		case src.Argument != "":
			out = appendLiteral(out, "/")
			out = append(out, component{Attrs: []string{src.Argument}, Segment: s.Name, Rule: ruleIdentityTest})
		case src.Provider != "":
			out = appendLiteral(out, "/")
			out = append(out, component{Cloud: src.Provider, Segment: s.Name, Rule: ruleSubscription})
		case src.Constant != "":
			out = appendLiteral(out, "/"+src.Constant)
		default:
			return nil, false
		}
	}
	return out, true
}

// templateShape is a template with each value segment's name dropped and
// its letters folded, so two parsers that name one id's segments
// differently ({resourceGroup}, {resourceGroupName}) state one shape.
func templateShape(t string) string {
	var b strings.Builder
	in := false
	for _, r := range strings.ToLower(t) {
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
