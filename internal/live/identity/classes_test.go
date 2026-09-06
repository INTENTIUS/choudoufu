// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestAllClassesMatchesTheConstBlock is what stops [AllClasses] from being a
// list somebody forgot to add to. Every consuming package's class-table
// guard is only as good as this list, so the list is read back out of
// identity.go's own source - the const block declaring `X Class = "..."` -
// and compared, in declaration order, with what AllClasses returns.
//
// Adding a class to the const block and nowhere else fails here first, and
// then, once AllClasses names it, in every package whose handler table does
// not.
func TestAllClassesMatchesTheConstBlock(t *testing.T) {
	declared := classesDeclaredIn(t, "identity.go")
	if len(declared) == 0 {
		t.Fatal("found no `Name Class = \"...\"` constants in identity.go; this test is not reading what it thinks it is reading")
	}

	got := AllClasses()
	if len(got) != len(declared) {
		t.Fatalf("AllClasses() has %d entries, identity.go declares %d: %v vs %v", len(got), len(declared), got, declared)
	}
	for i := range declared {
		if got[i] != declared[i] {
			t.Errorf("AllClasses()[%d] = %q, identity.go declares %q in that position", i, got[i], declared[i])
		}
	}
}

// TestClassTableGapsReportsBothDirections proves the helper every consuming
// package's guard leans on can actually fail: a table missing a class, and a
// table holding a key that is not a class, are both reported.
func TestClassTableGapsReportsBothDirections(t *testing.T) {
	full := map[Class]int{}
	for i, c := range AllClasses() {
		full[c] = i
	}
	if missing, unknown := ClassTableGaps(full); len(missing) != 0 || len(unknown) != 0 {
		t.Fatalf("a total table reported gaps: missing=%v unknown=%v", missing, unknown)
	}

	delete(full, ClassRecordLocated)
	full["NOT_A_CLASS"] = -1
	missing, unknown := ClassTableGaps(full)
	if len(missing) != 1 || missing[0] != ClassRecordLocated {
		t.Errorf("missing = %v, want [%s]", missing, ClassRecordLocated)
	}
	if len(unknown) != 1 || unknown[0] != Class("NOT_A_CLASS") {
		t.Errorf("unknown = %v, want [NOT_A_CLASS]", unknown)
	}
}

// classesDeclaredIn parses one of this package's own source files and
// returns, in declaration order, the name of every constant declared with
// the explicit type Class.
func classesDeclaredIn(t *testing.T, file string) []Class {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	var out []Class
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Class" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("constant of type Class with a non-literal value at %s; this test cannot read it", fset.Position(v.Pos()))
				}
				out = append(out, Class(lit.Value[1:len(lit.Value)-1]))
			}
		}
	}
	return out
}
