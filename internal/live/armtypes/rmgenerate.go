// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const azurermSchemaKey = "registry.opentofu.org/hashicorp/azurerm"

// AzurermSourcePin reads the version (version/VERSION) and git commit of an
// azurerm checkout, the pair the facts record.
func AzurermSourcePin(src string) (version, commit string, err error) {
	data, err := os.ReadFile(filepath.Join(src, "version", "VERSION"))
	if err != nil {
		return "", "", err
	}
	version = strings.TrimSpace(string(data))
	out, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse HEAD in %s: %w", src, err)
	}
	return version, strings.TrimSpace(string(out)), nil
}

// azurermTaggedTypes reads `tofu providers schema -json` output and returns
// the azurerm managed resource types whose schema declares a `tags`
// attribute of type map(string) - ARM's tags - and, apart, those whose
// `tags` is something else (a list of labels the service keeps in its own
// properties), with a digest of the azurerm half of the document.
func azurermTaggedTypes(schemaPath string) (tagged map[string]bool, notMap []string, digest string, err error) {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, nil, "", err
	}
	var doc struct {
		ProviderSchemas map[string]json.RawMessage `json:"provider_schemas"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, "", err
	}
	raw, ok := doc.ProviderSchemas[azurermSchemaKey]
	if !ok {
		return nil, nil, "", fmt.Errorf("%s has no %s schema", schemaPath, azurermSchemaKey)
	}
	// The digest is over a canonical re-encoding, as tools/azurerm-id-gen
	// takes it, so both artifacts name the same schema the same way.
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, nil, "", err
	}
	canon, err := json.Marshal(generic)
	if err != nil {
		return nil, nil, "", err
	}
	sum := sha256.Sum256(canon)
	var p struct {
		ResourceSchemas map[string]struct {
			Block struct {
				Attributes map[string]struct {
					Type json.RawMessage `json:"type"`
				} `json:"attributes"`
			} `json:"block"`
		} `json:"resource_schemas"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, nil, "", err
	}
	tagged = map[string]bool{}
	for name, rs := range p.ResourceSchemas {
		a, ok := rs.Block.Attributes["tags"]
		if !ok {
			continue
		}
		var typ []string
		if json.Unmarshal(a.Type, &typ) == nil && reflect.DeepEqual(typ, []string{"map", "string"}) {
			tagged[name] = true
		} else {
			notMap = append(notMap, name)
		}
	}
	sort.Strings(notMap)
	return tagged, notMap, hex.EncodeToString(sum[:]), nil
}

// GenerateAzurermTags builds the azurerm tag facts from a provider checkout
// and its schema: for every managed resource type whose schema has `tags`,
// whether its Create sends them in the create call or a later call, and
// what azurerm's own `tags` validation allows.
func GenerateAzurermTags(src, schemaPath, version, commit string) (*RMTags, error) {
	tagged, notMap, digest, err := azurermTaggedTypes(schemaPath)
	if err != nil {
		return nil, err
	}
	l, err := newRMLoader(src)
	if err != nil {
		return nil, err
	}
	regs, err := l.registrations()
	if err != nil {
		return nil, err
	}
	byType := map[string]rmRegistration{}
	for _, r := range regs {
		byType[r.Type] = r
	}
	out := &RMTags{Version: version, Commit: commit, SchemaSHA256: digest, NotARMTags: notMap, rows: map[string]RMTagRow{}}
	for name := range tagged {
		r, ok := byType[name]
		row := RMTagRow{Type: name}
		if !ok || r.Pkg == nil {
			row.Create = CreateUnknown
			row.Reason = "not found in the provider's registrations"
			row.Rules = TagRules{Validator: "unresolved"}
			out.rows[name] = row
			continue
		}
		l.classifyCreate(r, &row)
		row.Rules = l.tagRules(r)
		out.rows[name] = row
	}
	return out, nil
}

// classifyCreate fills the create columns of one row.
func (l *rmLoader) classifyCreate(r rmRegistration, row *RMTagRow) {
	p, f, outer, body := l.createBody(r)
	if body == nil {
		row.Create, row.Reason = CreateUnknown, "no Create function found in source"
		return
	}
	a := newRMAnalyzer(l)
	a.analyzeBody(p, f, outer, nil, body)
	if a.exhausted {
		row.Create, row.Reason = CreateUnknown, "the create path is larger than the analysis's inlining budget"
		return
	}

	// The create call is the first create-named call every run makes;
	// when each sits under a condition, the first of them. A conditional
	// create before it is a side path (azurerm_api_management restores a
	// soft-deleted service with an untagged PUT, then PUTs it with tags).
	createAt := -1
	for i, e := range a.events {
		if rmCreateVerb(e.name) && !e.cond {
			createAt = i
			break
		}
	}
	if createAt < 0 {
		for i, e := range a.events {
			if rmCreateVerb(e.name) {
				createAt = i
				break
			}
		}
	}
	switch {
	case createAt >= 0 && a.events[createAt].tainted:
		row.Create = CreateInCreate
		row.Call, row.At = a.events[createAt].text, a.events[createAt].pos
		return
	case createAt >= 0:
		for _, e := range a.events[createAt+1:] {
			if e.tainted {
				row.Create = CreateAfterCreate
				row.Call, row.At = e.text, e.pos
				row.CreateCall = a.events[createAt].text
				return
			}
		}
	default:
		for _, e := range a.events {
			if e.tainted {
				row.Create = CreateUnknown
				row.Call, row.At = e.text, e.pos
				row.Reason = "no API call named as a create; tags go through " + e.text
				return
			}
		}
	}
	row.Create = CreateUnknown
	switch {
	case createAt < 0 && len(a.events) == 0:
		row.Reason = "the create path makes no API call this analysis recognises"
	case createAt < 0:
		row.Reason = "no API call named as a create"
	case a.tagReads == 0:
		row.Reason = "the create path never reads tags"
		row.CreateCall = a.events[createAt].text
	default:
		row.Reason = "tags are read but not traced into an API call"
		row.CreateCall = a.events[createAt].text
	}
}

// createBody finds the function body a resource's Create runs: the
// `Create:` field of an untyped resource, or the Func of the
// sdk.ResourceFunc a typed resource's Create() returns.
func (l *rmLoader) createBody(r rmRegistration) (*rmPkg, *ast.File, *ast.FuncDecl, *ast.BlockStmt) {
	if r.TypeName != "" {
		mp, m := l.methodWithEmbedding(r.Pkg, r.TypeName, "Create", 0)
		if m == nil || m.Body == nil {
			return nil, nil, nil, nil
		}
		var fn ast.Expr
		ast.Inspect(m.Body, func(n ast.Node) bool {
			if fn != nil {
				return false
			}
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Func" {
					fn = kv.Value
					return false
				}
			}
			return true
		})
		bp, bf, bd, body := l.funcBody(mp, mp.fileOf[m], fn)
		if bd == nil {
			bd = m // a literal: the receiver is Create()'s
		}
		return bp, bf, bd, body
	}
	if r.Ctor == nil {
		return nil, nil, nil, nil
	}
	cp, cf, v := l.findField(r.Pkg, r.File, r.Ctor, "Create", 0, map[*ast.FuncDecl]bool{})
	if v == nil {
		return nil, nil, nil, nil
	}
	return l.funcBody(cp, cf, v)
}

// funcBody resolves a function value to a body: a literal, a named
// function, a method value, or a call whose result is a function literal.
func (l *rmLoader) funcBody(p *rmPkg, f *ast.File, e ast.Expr) (*rmPkg, *ast.File, *ast.FuncDecl, *ast.BlockStmt) {
	switch x := e.(type) {
	case *ast.FuncLit:
		return p, f, nil, x.Body
	case *ast.Ident, *ast.SelectorExpr:
		if cp, cf, fd := l.funcRef(p, f, x); fd != nil && fd.Body != nil {
			return cp, cf, fd, fd.Body
		}
	case *ast.CallExpr:
		cp, cf, fd := l.funcRef(p, f, x.Fun)
		if fd == nil || fd.Body == nil {
			return nil, nil, nil, nil
		}
		for _, s := range fd.Body.List {
			if rs, ok := s.(*ast.ReturnStmt); ok && len(rs.Results) == 1 {
				if fl, ok := rs.Results[0].(*ast.FuncLit); ok {
					return cp, cf, fd, fl.Body
				}
			}
		}
	}
	return nil, nil, nil, nil
}

// findField looks for `<name>: X` (or `r.<name> = X`) in a resource
// constructor, following calls into the provider's own helpers a couple of
// levels deep for constructors that build the resource in a shared function.
func (l *rmLoader) findField(p *rmPkg, f *ast.File, fd *ast.FuncDecl, name string, depth int, seen map[*ast.FuncDecl]bool) (*rmPkg, *ast.File, ast.Expr) {
	if fd == nil || fd.Body == nil || seen[fd] {
		return nil, nil, nil
	}
	seen[fd] = true
	var found ast.Expr
	var calls []*ast.CallExpr
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok && id.Name == name {
				found = n.Value
				return false
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if se, ok := lhs.(*ast.SelectorExpr); ok && se.Sel.Name == name && i < len(n.Rhs) {
					found = n.Rhs[i]
					return false
				}
			}
		case *ast.CallExpr:
			calls = append(calls, n)
		}
		return true
	})
	if found != nil {
		return p, f, found
	}
	if depth >= 2 {
		return nil, nil, nil
	}
	for _, c := range calls {
		cp, cf, cfd := l.funcRef(p, f, c.Fun)
		if cfd == nil || !l.inModule(cp) {
			continue
		}
		if rp, rf, e := l.findField(cp, cf, cfd, name, depth+1, seen); e != nil {
			return rp, rf, e
		}
	}
	return nil, nil, nil
}

// tagRules reads the `tags` entry of a resource's top-level schema and the
// validation function it carries.
func (l *rmLoader) tagRules(r rmRegistration) TagRules {
	p, f, v := l.tagsSchemaExpr(r)
	if v == nil {
		return TagRules{Validator: "unresolved"}
	}
	rules := TagRules{Validator: types.ExprString(v)}
	vp, vf, vfn, env := l.validateFunc(p, f, v, nil)
	if lit := schemaLiteral(v); lit {
		// An inline &pluginsdk.Schema{...}: name its ValidateFunc.
		rules.Validator = "schema literal, no ValidateFunc"
		if vfn != nil {
			rules.Validator = "schema literal, ValidateFunc " + types.ExprString(vfn)
		}
	}
	if vfn == nil {
		return rules
	}
	l.readValidator(vp, vf, vfn, env, &rules, 0)
	return rules
}

// tagsSchemaExpr finds the value of "tags" in the resource's top-level
// schema map: the `Schema:` field of an untyped resource, or what a typed
// resource's Arguments() returns.
func (l *rmLoader) tagsSchemaExpr(r rmRegistration) (*rmPkg, *ast.File, ast.Expr) {
	var p *rmPkg
	var f *ast.File
	var src ast.Expr
	var scope *ast.BlockStmt
	if r.TypeName != "" {
		mp, m := l.methodWithEmbedding(r.Pkg, r.TypeName, "Arguments", 0)
		if m == nil || m.Body == nil {
			return nil, nil, nil
		}
		p, f, scope = mp, mp.fileOf[m], m.Body
		for _, s := range m.Body.List {
			if rs, ok := s.(*ast.ReturnStmt); ok && len(rs.Results) == 1 {
				src = rs.Results[0]
			}
		}
	} else {
		if r.Ctor == nil {
			return nil, nil, nil
		}
		p, f, src = l.findField(r.Pkg, r.File, r.Ctor, "Schema", 0, map[*ast.FuncDecl]bool{})
		scope = r.Ctor.Body
	}
	lit, lp, lf := l.mapLit(p, f, src, scope, 0)
	if lit == nil {
		return nil, nil, nil
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := rmStringLit(kv.Key); ok && k == "tags" {
			return lp, lf, kv.Value
		}
	}
	// A schema map extended after its literal: s["tags"] = ...
	var found ast.Expr
	if scope != nil {
		ast.Inspect(scope, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
				for i, lhs := range as.Lhs {
					if ix, ok := lhs.(*ast.IndexExpr); ok {
						if k, ok := rmStringLit(ix.Index); ok && k == "tags" && found == nil {
							found = as.Rhs[i]
						}
					}
				}
			}
			return found == nil
		})
	}
	return p, f, found
}

// mapLit resolves an expression to the map literal it is: a literal, a
// local assigned one in scope, or a call to a provider function returning one.
func (l *rmLoader) mapLit(p *rmPkg, f *ast.File, e ast.Expr, scope *ast.BlockStmt, depth int) (*ast.CompositeLit, *rmPkg, *ast.File) {
	if depth > 3 || e == nil {
		return nil, nil, nil
	}
	switch x := e.(type) {
	case *ast.CompositeLit:
		if _, ok := x.Type.(*ast.MapType); ok {
			return x, p, f
		}
	case *ast.Ident:
		var rhs ast.Expr
		if scope != nil {
			ast.Inspect(scope, func(n ast.Node) bool {
				if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
					for i, lhs := range as.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && id.Name == x.Name && rhs == nil {
							rhs = as.Rhs[i]
						}
					}
				}
				if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Names) == len(vs.Values) {
					for i, id := range vs.Names {
						if id.Name == x.Name && rhs == nil {
							rhs = vs.Values[i]
						}
					}
				}
				return rhs == nil
			})
		}
		if rhs != nil {
			return l.mapLit(p, f, rhs, scope, depth+1)
		}
	case *ast.CallExpr:
		if fl, ok := x.Fun.(*ast.FuncLit); ok {
			// Schema: func() map[string]*pluginsdk.Schema { ... }()
			var found *ast.CompositeLit
			ast.Inspect(fl.Body, func(n ast.Node) bool {
				if rs, ok := n.(*ast.ReturnStmt); ok && len(rs.Results) == 1 && found == nil {
					found, p, f = l.mapLit(p, f, rs.Results[0], fl.Body, depth+1)
				}
				return found == nil
			})
			if found != nil {
				return found, p, f
			}
			return nil, nil, nil
		}
		cp, cf, fd := l.funcRef(p, f, x.Fun)
		if fd == nil {
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				for _, ms := range p.methods {
					if m := ms[sel.Sel.Name]; m != nil {
						cp, cf, fd = p, p.fileOf[m], m
						break
					}
				}
			}
		}
		if fd == nil || fd.Body == nil {
			return nil, nil, nil
		}
		for _, s := range fd.Body.List {
			if rs, ok := s.(*ast.ReturnStmt); ok && len(rs.Results) == 1 {
				if lit, lp, lf := l.mapLit(cp, cf, rs.Results[0], fd.Body, depth+1); lit != nil {
					return lit, lp, lf
				}
			}
		}
	}
	return nil, nil, nil
}

// validateFunc follows a `tags` schema expression to its ValidateFunc:
// a &Schema{...} literal, or a call to a schema constructor
// (commonschema.Tags(), TagsWithMaximumElements(15)) whose literal it
// returns. env binds the constructor's parameters to the call's arguments,
// so ValidateWithMaximumElements(max) reads max as 15.
func (l *rmLoader) validateFunc(p *rmPkg, f *ast.File, e ast.Expr, env map[string]ast.Expr) (*rmPkg, *ast.File, ast.Expr, map[string]ast.Expr) {
	switch x := e.(type) {
	case *ast.UnaryExpr:
		return l.validateFunc(p, f, x.X, env)
	case *ast.CompositeLit:
		for _, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "ValidateFunc" {
					return p, f, kv.Value, env
				}
			}
		}
	case *ast.CallExpr:
		cp, cf, fd := l.funcRef(p, f, x.Fun)
		if fd == nil || fd.Body == nil {
			return nil, nil, nil, nil
		}
		inner := bindParams(fd, x.Args, env)
		for _, s := range fd.Body.List {
			if rs, ok := s.(*ast.ReturnStmt); ok && len(rs.Results) == 1 {
				return l.validateFunc(cp, cf, rs.Results[0], inner)
			}
		}
	}
	return nil, nil, nil, nil
}

func schemaLiteral(e ast.Expr) bool {
	if u, ok := e.(*ast.UnaryExpr); ok {
		e = u.X
	}
	_, ok := e.(*ast.CompositeLit)
	return ok
}

func bindParams(fd *ast.FuncDecl, args []ast.Expr, outer map[string]ast.Expr) map[string]ast.Expr {
	env := map[string]ast.Expr{}
	i := 0
	for _, fld := range fd.Type.Params.List {
		for _, n := range fld.Names {
			if i < len(args) {
				a := args[i]
				if id, ok := a.(*ast.Ident); ok && outer[id.Name] != nil {
					a = outer[id.Name]
				}
				env[n.Name] = a
			}
			i++
		}
	}
	return env
}

// readValidator reads a tags ValidateFunc's body for the rules it
// enforces: `len(<map>) > N` is the tag count, `len(<range key>) > N` the
// key length, `len(<range value, or what is computed from it>) > N` the
// value length,
// `strings.ToLower(k) != k` lowercase keys, and `<map>["lit"]` a key it
// refuses. A call whose result it returns as its own (warnings, errors =
// tags.Validate(i, k)) is read too.
func (l *rmLoader) readValidator(p *rmPkg, f *ast.File, e ast.Expr, env map[string]ast.Expr, r *TagRules, depth int) {
	if depth > 3 {
		return
	}
	var body *ast.BlockStmt
	switch x := e.(type) {
	case *ast.FuncLit:
		body = x.Body
	case *ast.Ident, *ast.SelectorExpr:
		cp, cf, fd := l.funcRef(p, f, x)
		if fd == nil || fd.Body == nil {
			return
		}
		p, f, body = cp, cf, fd.Body
	case *ast.CallExpr:
		cp, cf, fd := l.funcRef(p, f, x.Fun)
		if fd == nil || fd.Body == nil {
			return
		}
		inner := bindParams(fd, x.Args, env)
		for _, s := range fd.Body.List {
			if rs, ok := s.(*ast.ReturnStmt); ok && len(rs.Results) == 1 {
				l.readValidator(cp, cf, rs.Results[0], inner, r, depth+1)
				return
			}
		}
		return
	default:
		return
	}

	// The map, its range key, and its range value plus whatever is
	// computed from that value (value, err := tagValueToString(v)).
	mapVars, keyVars, valueVars := map[string]bool{}, map[string]bool{}, map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Rhs) == 1 && len(s.Lhs) > 0 {
				usesValue := false
				ast.Inspect(s.Rhs[0], func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok && valueVars[id.Name] {
						usesValue = true
					}
					return !usesValue
				})
				if id, ok := s.Lhs[0].(*ast.Ident); ok && usesValue {
					valueVars[id.Name] = true
				}
			}
			if len(s.Rhs) == 1 {
				if ta, ok := s.Rhs[0].(*ast.TypeAssertExpr); ok {
					if _, ok := ta.Type.(*ast.MapType); ok {
						if id, ok := s.Lhs[0].(*ast.Ident); ok {
							mapVars[id.Name] = true
						}
					}
				}
				if c, ok := s.Rhs[0].(*ast.CallExpr); ok && len(s.Lhs) == 2 {
					// warnings, errors = tags.Validate(i, k)
					if _, _, fd := l.funcRef(p, f, c.Fun); fd != nil && fd.Type.Results != nil && fd.Type.Results.NumFields() == 2 {
						l.readValidator(p, f, c.Fun, env, r, depth+1)
					}
				}
			}
		case *ast.RangeStmt:
			if id, ok := s.X.(*ast.Ident); ok && mapVars[id.Name] {
				if k, ok := s.Key.(*ast.Ident); ok {
					keyVars[k.Name] = true
				}
				if v, ok := s.Value.(*ast.Ident); ok {
					valueVars[v.Name] = true
				}
			}
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.GTR || x.Op == token.GEQ {
				c, ok := x.X.(*ast.CallExpr)
				if !ok || len(c.Args) != 1 {
					return true
				}
				if fn, ok := c.Fun.(*ast.Ident); !ok || fn.Name != "len" {
					return true
				}
				nv, ok := intValue(x.Y, env)
				if !ok {
					return true
				}
				if x.Op == token.GEQ {
					nv--
				}
				id, _ := c.Args[0].(*ast.Ident)
				switch {
				case id != nil && mapVars[id.Name]:
					r.MaxTags = nv
				case id != nil && keyVars[id.Name]:
					r.MaxKeyLen = nv
				case id != nil && valueVars[id.Name]:
					r.MaxValueLen = nv
				}
			}
			if x.Op == token.NEQ {
				if c, ok := x.X.(*ast.CallExpr); ok && types.ExprString(c.Fun) == "strings.ToLower" {
					r.LowercaseKeys = true
				}
			}
		case *ast.IndexExpr:
			if id, ok := x.X.(*ast.Ident); ok && mapVars[id.Name] {
				if k, ok := rmStringLit(x.Index); ok {
					r.ForbiddenKeys = appendUnique(r.ForbiddenKeys, k)
				}
			}
		}
		return true
	})
}

func intValue(e ast.Expr, env map[string]ast.Expr) (int, bool) {
	if id, ok := e.(*ast.Ident); ok && env[id.Name] != nil {
		e = env[id.Name]
	}
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.INT {
		return 0, false
	}
	n, err := strconv.Atoi(bl.Value)
	return n, err == nil
}

func appendUnique(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	xs = append(xs, s)
	sort.Strings(xs)
	return xs
}
