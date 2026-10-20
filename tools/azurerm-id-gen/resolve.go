// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"go/ast"
	"strings"
	"unicode"
)

// Segment kinds. The first block are the resourceids constructors
// (go-azure-helpers/resourcemanager/resourceids/segments.go) under their own
// names; a hand parser's ID() format string is read into the same kinds.
const (
	segStatic        = "static"
	segProvider      = "resource-provider"
	segSubscription  = "subscription"
	segResourceGroup = "resource-group"
	segUser          = "user"
	segConstant      = "constant"
	segScope         = "scope"
	segResourceID    = "resource-id"
	segDataPlane     = "data-plane"
)

// segment is one path segment of an ARM id template.
type segment struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`  // the parser's own name for a value segment
	Value string `json:"value,omitempty"` // the fixed text of a static or provider segment
}

// idSource is the id type a resource's importer parses and where it came
// from.
type idSource struct {
	Kind     string    `json:"kind"` // "resourceids" or "hand-parser"
	Package  string    `json:"package"`
	TypeName string    `json:"id_type"`
	File     string    `json:"file"`
	Segments []segment `json:"-"`
}

type typeKey struct {
	p    *pkg
	name string
}

// idTypeOf follows a registration's validating expression to the id type
// it parses. It walks the expression in source order, and through every
// function it calls inside the checkout, collecting each type that knows
// its own segments; the first one found is the importer's type. The order
// matters for ImporterValidatingResourceIdThen, whose second argument may
// parse a parent id after the first has validated the resource's own.
func (l *loader) idTypeOf(r registration) (*idSource, string) {
	var found []typeKey
	l.collect(r.Pkg, r.File, r.Expr, 0, map[*ast.FuncDecl]bool{}, &found)
	if len(found) == 0 {
		return nil, "no id type reachable from the " + r.Via
	}
	return l.segmentsOf(found[0].p, found[0].name)
}

// readIDTypeOf is the id type a resource's Read parses its stored id with:
// the first call in Read's body, in source order, that is handed the
// resource's own id (d.Id(), metadata.ResourceData.Id()) and whose
// declared result is a type that states its segments. That id is the one
// the resource holds (azurerm's Create sets it, d.SetId(id.ID()), and Read
// is where it is taken apart again), so it is the check on what the
// importer's expression yielded (#1120 unit 13). Nil when Read was not
// found or parses its id some other way.
func (l *loader) readIDTypeOf(r registration) *typeKey {
	if r.Read == nil {
		return nil
	}
	return l.readIn(r.ReadPkg, r.ReadFile, r.Read, 0, map[ast.Node]bool{})
}

// readIn is [loader.readIDTypeOf] over one body. When the body hands the
// id to no parser itself it follows the calls it makes, two deep and only
// inside the checkout's services: a typed resource's Read is often
// `return r.base.readFunc("resource_id")` (policy's assignment resources),
// a method of a field whose type this loader does not track, so a method
// is followed by name when its package declares exactly one by that name.
func (l *loader) readIn(p *pkg, f *ast.File, body ast.Node, depth int, seen map[ast.Node]bool) *typeKey {
	if p == nil || body == nil || seen[body] {
		return nil
	}
	seen[body] = true
	var found *typeKey
	var calls []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !passesOwnID(call) {
			calls = append(calls, call)
			return true
		}
		fp, ff, fd := l.funcRef(p, f, call.Fun)
		if fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			return true
		}
		if tp, name := l.typeRef(fp, ff, fd.Type.Results.List[0].Type); tp != nil && l.isIDType(tp, name) {
			found = &typeKey{tp, name}
			return false
		}
		return true
	})
	if found != nil || depth >= 2 {
		return found
	}
	for _, c := range calls {
		fp, ff, fd := l.funcRef(p, f, c.Fun)
		if fd == nil {
			if se, ok := c.Fun.(*ast.SelectorExpr); ok {
				fp, fd = p, p.uniqueMethod(se.Sel.Name)
				if fd != nil {
					ff = p.fileOf[fd]
				}
			}
		}
		if fd == nil || fd.Body == nil || !strings.HasPrefix(fp.path, l.module+"/internal/services/") {
			continue
		}
		if k := l.readIn(fp, ff, fd.Body, depth+1, seen); k != nil {
			return k
		}
	}
	return nil
}

// passesOwnID reports whether a call is handed a resource's own id: an
// argument that is a no-argument call of a method named Id.
func passesOwnID(call *ast.CallExpr) bool {
	for _, a := range call.Args {
		c, ok := a.(*ast.CallExpr)
		if !ok || len(c.Args) != 0 {
			continue
		}
		if se, ok := c.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "Id" {
			return true
		}
	}
	return false
}

func (l *loader) collect(p *pkg, f *ast.File, n ast.Node, depth int, seen map[*ast.FuncDecl]bool, out *[]typeKey) {
	if n == nil {
		return
	}
	add := func(tp *pkg, name string) {
		if tp == nil || !l.isIDType(tp, name) {
			return
		}
		for _, k := range *out {
			if k.p == tp && k.name == name {
				return
			}
		}
		*out = append(*out, typeKey{tp, name})
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			// A function literal's body, never its signature (#1120 unit
			// 13). An importer that wraps a parser literal declares the
			// type the wrapper wants back, and that is not the one the
			// literal parses: azurerm_lb_nat_rule's importer is
			// loadBalancerSubResourceImporter(func(input string)
			// (*loadbalancers.LoadBalancerId, error) {...}), whose body
			// parses the rule's own InboundNatRuleId and returns the load
			// balancer's id built from it (loadbalancer.go line 114 and
			// lb_nat_rule_resource.go line 31 at 5.10.0). Read in
			// signature order, the result type was the first id type
			// seen, so the row recorded the load balancer's id.
			l.collect(p, f, x.Body, depth, seen, out)
			return false
		case *ast.CallExpr:
			// validation.None(...) accepts an id only when it is none of
			// the ids its arguments validate, so those are the ids this
			// resource's is not (#1120 unit 13): azurerm_resource_policy_assignment's
			// IDValidationFunc is validate.ResourceAssignmentId, whose
			// validation.None lists the management group, resource group
			// and subscription validators (policy/validate/resource_assignment_id.go
			// lines 14-23 at 5.10.0), and the row took the management
			// group's id.
			if se, ok := x.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "None" {
				return false
			}
		case *ast.CompositeLit:
			if x.Type != nil {
				tp, name := l.typeRef(p, f, x.Type)
				add(tp, name)
			}
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true // a call chain: keep walking into X
			}
			if _, isPkg := p.imports[f][id.Name]; !isPkg {
				return false // a field or method on a local value
			}
			if tp, name := l.typeRef(p, f, x); tp != nil {
				add(tp, name)
				return false
			}
			if fp, ff, fd := l.funcRef(p, f, x); fd != nil {
				l.followFunc(fp, ff, fd, depth, seen, out)
			}
			return false
		case *ast.Ident:
			if _, ok := p.types[x.Name]; ok {
				add(p, x.Name)
				return false
			}
			if fd := p.funcs[x.Name]; fd != nil {
				l.followFunc(p, p.fileOf[fd], fd, depth, seen, out)
			}
			return false
		}
		return true
	})
}

// followFunc takes a function's declared result first: a parser returning
// *FooId names its type outright. Otherwise it reads the body, as far as
// four calls deep and only inside the checkout's own code and the two Azure
// SDK modules - the pluginsdk and terraform-plugin-sdk wrappers carry no id
// types and are where an unbounded walk would spend its time.
func (l *loader) followFunc(p *pkg, f *ast.File, fd *ast.FuncDecl, depth int, seen map[*ast.FuncDecl]bool, out *[]typeKey) {
	if seen[fd] {
		return
	}
	seen[fd] = true
	if fd.Type.Results != nil && len(fd.Type.Results.List) > 0 {
		tp, name := l.typeRef(p, f, fd.Type.Results.List[0].Type)
		if tp != nil && (l.isIDType(tp, name) || (l.followable(tp.path) && isStruct(tp, name))) {
			// A parser's declared result is its answer even when that type
			// cannot state its segments (policy's PolicyDefinitionId splits
			// on a regular expression): walking on into its body would find
			// whatever helper id it parses a piece with and misattribute it.
			for _, k := range *out {
				if k.p == tp && k.name == name {
					return
				}
			}
			*out = append(*out, typeKey{tp, name})
			return
		}
	}
	if depth >= 4 || fd.Body == nil || !l.followable(p.path) {
		return
	}
	l.collect(p, f, fd.Body, depth+1, seen, out)
}

func (l *loader) followable(importPath string) bool {
	if strings.HasPrefix(importPath, l.module+"/internal/tf/") {
		return false
	}
	return strings.HasPrefix(importPath, l.module+"/") ||
		strings.HasPrefix(importPath, "github.com/hashicorp/go-azure-sdk/") ||
		strings.HasPrefix(importPath, "github.com/hashicorp/go-azure-helpers/")
}

// isIDType reports whether a named type states its own segments: a
// Segments() method (go-azure-sdk / go-azure-helpers resourceids, commonids)
// or an ID() method formatting an ARM path (the hand parsers under
// internal/services/*/parse).
func (l *loader) isIDType(p *pkg, name string) bool {
	if _, ok := p.types[name]; !ok {
		return false
	}
	return p.method(name, "Segments") != nil || p.method(name, "ID") != nil
}

// segmentsOf reads a type's segments, preferring Segments() over ID().
// segmentsOf reads a type's segments, preferring Segments() over ID(). The
// source is returned even when its segments cannot be read, so an unmapped
// row still names the type that defeated it.
func (l *loader) segmentsOf(p *pkg, name string) (*idSource, string) {
	src := &idSource{Package: p.path, TypeName: name, File: l.relPath(l.fset.Position(p.types[name].Pos()).Filename)}
	if strings.HasPrefix(name, "CompositeResourceID") {
		// commonids.ParseCompositeResourceID: several ARM ids joined by
		// "|", one per object the resource links.
		src.Kind = "composite"
		return src, "composite id (several ARM ids joined by |)"
	}
	if m := p.method(name, "Segments"); m != nil {
		src.Kind = "resourceids"
		segs, problem := segmentsFromList(m)
		if problem != "" {
			return src, problem
		}
		src.Segments = segs
		return src, ""
	}
	if m := p.method(name, "ID"); m != nil {
		src.Kind = "hand-parser"
		segs, problem := segmentsFromFormat(m)
		if problem != "" {
			return src, problem
		}
		src.Segments = segs
		return src, ""
	}
	src.Kind = "custom"
	return src, "id type states no segments (a parser with its own grammar)"
}

func isStruct(p *pkg, name string) bool {
	ts := p.types[name]
	if ts == nil {
		return false
	}
	_, ok := ts.Type.(*ast.StructType)
	return ok
}

// segmentsFromList reads `return []resourceids.Segment{ resourceids.X(...), ... }`.
func segmentsFromList(m *ast.FuncDecl) ([]segment, string) {
	var lit *ast.CompositeLit
	ast.Inspect(m.Body, func(n ast.Node) bool {
		if rs, ok := n.(*ast.ReturnStmt); ok && len(rs.Results) == 1 && lit == nil {
			lit, _ = rs.Results[0].(*ast.CompositeLit)
		}
		return lit == nil
	})
	if lit == nil {
		return nil, "Segments() does not return a literal list"
	}
	var out []segment
	for _, e := range lit.Elts {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return nil, "Segments() element is not a constructor call"
		}
		se, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil, "Segments() element is not a resourceids constructor"
		}
		arg := func(i int) string {
			if i < len(call.Args) {
				if s, ok := stringLit(call.Args[i]); ok {
					return s
				}
			}
			return ""
		}
		switch se.Sel.Name {
		case "StaticSegment":
			out = append(out, segment{Kind: segStatic, Value: arg(1)})
		case "ResourceProviderSegment":
			out = append(out, segment{Kind: segProvider, Value: arg(1)})
		case "SubscriptionIdSegment":
			out = append(out, segment{Kind: segSubscription, Name: arg(0)})
		case "ResourceGroupSegment":
			out = append(out, segment{Kind: segResourceGroup, Name: arg(0)})
		case "UserSpecifiedSegment":
			out = append(out, segment{Kind: segUser, Name: arg(0)})
		case "ConstantSegment":
			out = append(out, segment{Kind: segConstant, Name: arg(0)})
		case "ScopeSegment":
			out = append(out, segment{Kind: segScope, Name: arg(0)})
		case "ResourceIDSegment":
			out = append(out, segment{Kind: segResourceID, Name: arg(0)})
		case "DataPlaneBaseURISegment":
			out = append(out, segment{Kind: segDataPlane, Name: arg(0)})
		default:
			return nil, "unknown resourceids segment constructor " + se.Sel.Name
		}
	}
	return out, ""
}

// segmentsFromFormat reads a hand parser's ID(): a fmt.Sprintf whose format
// is a literal or a local bound to one, with one id field per %s.
func segmentsFromFormat(m *ast.FuncDecl) ([]segment, string) {
	locals := map[string]string{}
	var call *ast.CallExpr
	ast.Inspect(m.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && i < len(x.Rhs) {
					if s, ok := stringLit(x.Rhs[i]); ok {
						locals[id.Name] = s
					}
				}
			}
		case *ast.CallExpr:
			if se, ok := x.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "Sprintf" && call == nil {
				call = x
			}
		}
		return true
	})
	if call == nil || len(call.Args) == 0 {
		return nil, "ID() does not format a path with fmt.Sprintf"
	}
	format, ok := stringLit(call.Args[0])
	if !ok {
		id, isIdent := call.Args[0].(*ast.Ident)
		if !isIdent {
			return nil, "ID() format is not a literal"
		}
		if format, ok = locals[id.Name]; !ok {
			return nil, "ID() format is not a literal"
		}
	}
	args := call.Args[1:]
	var out []segment
	tokens := strings.Split(strings.TrimPrefix(format, "/"), "/")
	if strings.HasPrefix(format, "%s") {
		// A scope: the first value is itself a full ARM id.
		tokens[0] = "%scope"
	} else if !strings.HasPrefix(format, "/") {
		return nil, "ID() format is not an ARM path"
	}
	next := 0
	for _, tok := range tokens {
		switch {
		case tok == "%s" || tok == "%scope":
			if next >= len(args) {
				return nil, "ID() format has more verbs than arguments"
			}
			field := fieldName(args[next])
			next++
			if field == "" {
				return nil, "ID() argument is not an id field"
			}
			kind := fieldKind(field)
			if tok == "%scope" {
				kind = segScope
			}
			if strings.Contains(strings.ToLower(field), "url") {
				// keyVaultBaseUrl and friends: a data-plane endpoint, not
				// an ARM id, wherever it sits in the format.
				kind = segDataPlane
			}
			out = append(out, segment{Kind: kind, Name: lowerFirst(field)})
		case strings.Contains(tok, "%"):
			return nil, "ID() path segment combines several values"
		case strings.HasPrefix(tok, "Microsoft.") && len(out) > 0 && out[len(out)-1].Kind == segStatic && out[len(out)-1].Value == "providers":
			out = append(out, segment{Kind: segProvider, Value: tok})
		default:
			out = append(out, segment{Kind: segStatic, Value: tok})
		}
	}
	return out, ""
}

// fieldName is the id field an ID() argument reads, looking through
// wrappers such as strings.TrimPrefix(id.Scope, "/").
func fieldName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.CallExpr:
		for _, a := range x.Args {
			if n := fieldName(a); n != "" {
				return n
			}
		}
	}
	return ""
}

func fieldKind(field string) string {
	switch field {
	case "SubscriptionId", "SubscriptionID", "Subscription":
		return segSubscription
	case "ResourceGroup", "ResourceGroupName":
		return segResourceGroup
	}
	return segUser
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
