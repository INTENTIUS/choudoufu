// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"regexp"
	"strconv"
	"strings"
)

// This file reads what a resource's Create sends, for the maintainer's
// ruling of 2026-10-20: with a record store in play, a declared instance of
// a type with no labels map, with no record, found live at the identity its
// configuration computes, is not owned. What follows is the family's to
// say per type ([substrate.Unrecorded]): the plan proposes the create where
// nothing can collide, and stops with the name-held refusal where Google
// would answer 409 alreadyExists. The facts recorded here are the evidence
// unrecorded.go decides from.

var (
	createRegRe  = regexp.MustCompile(`\bCreate(?:Context)?:\s+(\w+),`)
	customVerbRe = regexp.MustCompile(`:(\w+)$`)
	// setVerbRe is a set verb: setIamPolicy, SetOrgPolicy. "setUp" is
	// "set up", a create (discovery engine's :setUpDataConnectorV2).
	setVerbCallRe = regexp.MustCompile(`^[sS]et(?:[A-TV-Z]|U[a-oq-z])`)
	newVerbCallRe = regexp.MustCompile(`^(?:Insert|Create|Add)(?:[A-Z]\w*)?$`)
	alreadyErrRe  = regexp.MustCompile(`\bErrorf\("([^"]*\balready\b[^"]*)"`)
)

// notAnAPICall are receivers whose calls set resource data, not cloud
// state: d.Set, d.SetId, tpgresource.SetResourceIdentityAttributes.
var notAnAPICall = map[string]bool{"d": true, "diff": true, "tpgresource": true}

// adoptBranchCalls are the calls a branch may make and still be taking an
// object it found rather than writing one.
var adoptBranchCalls = map[string]bool{
	"d.SetId": true, "d.Set": true, "fmt.Errorf": true, "fmt.Sprintf": true,
	"log.Printf": true, "tpgresource.SetResourceIdentityAttributes": true,
}

func readCreateWrites(s *Source, text string) {
	if e := alreadyErrRe.FindStringSubmatch(text); e != nil {
		s.CreateRefusesExisting = e[1]
	}
	m := createRegRe.FindStringSubmatch(text)
	if m == nil {
		return
	}
	fset := gotoken.NewFileSet()
	f, err := parser.ParseFile(fset, "", text, 0)
	if err != nil {
		return
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == m[1] && fd.Body != nil {
			fn = fd
		}
	}
	if fn == nil {
		return
	}
	s.CreateFunc = fn.Name.Name

	body := text[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset]
	if u := createURLRe.FindStringSubmatch(body); u != nil {
		path, _, _ := strings.Cut(u[1], "?")
		if c := customVerbRe.FindStringSubmatch(path); c != nil {
			s.CreateCustom = c[1]
		}
	}

	sets, news := map[string]bool{}, map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok && id.Name == "Method" {
				if bl, ok := n.Value.(*ast.BasicLit); ok && bl.Kind == gotoken.STRING {
					if v, err := strconv.Unquote(bl.Value); err == nil {
						s.CreateMethods = append(s.CreateMethods, v)
					}
				}
			}
		case *ast.CallExpr:
			recv, name := callName(n)
			if notAnAPICall[recv] || name == "" {
				return true
			}
			if setVerbCallRe.MatchString(name) {
				sets[name] = true
			}
			if newVerbCallRe.MatchString(name) {
				news[name] = true
			}
		}
		return true
	})
	s.CreateSetCalls, s.CreateNewCalls = sortedSet(sets), sortedSet(news)
	s.CreateAdopts = adoptsBeforeWriting(fn.Body)
}

// callName is a call's receiver or package and its function name: "d" and
// "SetId" for d.SetId(...), "" and "foo" for foo(...).
func callName(c *ast.CallExpr) (recv, name string) {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return "", f.Name
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name, f.Sel.Name
		}
		return "", f.Sel.Name
	}
	return "", ""
}

// adoptsBeforeWriting reports whether one of body's top-level if
// statements, ahead of the first statement that sends anything, returns
// nil from a branch that sets a non-empty id and calls nothing else of
// consequence ([adoptBranchCalls]). google_project_service's Create is the
// shape: it reads the project's enabled services and, when the service is
// already among them, sets the id and returns.
func adoptsBeforeWriting(body *ast.BlockStmt) bool {
	for _, st := range body.List {
		if ifs, ok := st.(*ast.IfStmt); ok && adoptBranch(ifs.Body) {
			return true
		}
		if sends(st) {
			return false
		}
	}
	return false
}

func adoptBranch(b *ast.BlockStmt) bool {
	if len(b.List) == 0 {
		return false
	}
	ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	if id, ok := ret.Results[0].(*ast.Ident); !ok || id.Name != "nil" {
		return false
	}
	setsID, clean := false, true
	ast.Inspect(b, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		recv, name := callName(c)
		if !adoptBranchCalls[recv+"."+name] {
			clean = false
			return false
		}
		if recv == "d" && name == "SetId" && len(c.Args) == 1 {
			if bl, ok := c.Args[0].(*ast.BasicLit); !ok || bl.Value != `""` {
				setsID = true
			}
		}
		return true
	})
	return setsID && clean
}

// sends reports whether st makes a call that could write: anything but
// the adopt branch's own vocabulary and the provider's config and project
// lookups. It is deliberately wide, so an adopt branch is only ever
// credited when it comes first.
func sends(st ast.Stmt) bool {
	found := false
	ast.Inspect(st, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		recv, name := callName(c)
		switch {
		case recv == "transport_tpg" && name == "SendRequest":
			found = true
		case setVerbCallRe.MatchString(name) && !notAnAPICall[recv]:
			found = true
		case newVerbCallRe.MatchString(name) && !notAnAPICall[recv]:
			found = true
		case strings.Contains(strings.ToLower(name), "enable"):
			found = true
		}
		return !found
	})
	return found
}
