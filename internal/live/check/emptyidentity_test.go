// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// A resolution that identifies NOTHING is worse than no resolution, and
// nothing in this repository was watching for one.
//
// The two classes below are the ones that claim to have an answer.
// ClassConcrete says "I can name the live object right now"; ClassParentDerived
// says "I can name it once these parents are known". ClassNeedsDiscovery and
// ClassRecordBacked deliberately carry nothing, because their identity is not
// in the configuration at all - see renderedIdentity in identitygolden_test.go.
//
// So an instance in one of the first two classes carrying no payload has
// resolved into a claim it cannot back. Downstream it is a marker written as
// the empty string, an import attempted with no identity, or a collision check
// that reports every instance of a type as the same object. Every verdict-level
// instrument in this repository reads it as a SUCCESS: Report.Instances counts
// it, Report.Blocked is unaffected, and the corpus ranking gains a resolved
// instance.
//
// # Why this is not covered by the golden already
//
// TestIdentityGolden pins the rendered value of every instance, so an existing
// row losing its value shows up as a MODIFIED line, which is an alarm. But an
// instance that previously REFUSED and now resolves empty arrives as an ADDED
// line - and identitygolden_test.go's own doc comment says a diff cannot judge
// an added line, because added lines are what the campaign is trying to
// produce. This test judges it. Every added line is checked for a payload
// whether or not anyone reads the diff, and `-update` cannot silence it.
//
// # What "empty" means, precisely
//
// Not "ImportID is empty". For a type whose table entry is
// TypeIdentity.IdentityObjectOnly - several identity attributes and no
// separator any schema documents to join them with - classify sets ImportID to
// the empty string DELIBERATELY, so the projection imports by identity object
// rather than inventing a grammar (resolve.go, the idObjectOnly branch), and
// IdentityValues carries the whole answer. concreteIdentityKey documents the
// same split and the measured consequence of getting it wrong: three
// aws_autoscaling_schedule instances with distinct scheduled_action_names were
// once reported as three resources with the identity "".
//
// So the invariant is that SOMETHING is populated - the joined string, the
// per-attribute split, or the parent formula - not that any particular field
// is.
func TestNoResolutionIdentifiesNothing(t *testing.T) {
	root := flocitest.RepoRoot(t)
	dirs := identityGoldenDirs(t, root)
	if len(dirs) < 300 {
		t.Fatalf("found only %d configuration directories under %v; the walk is not reaching the tree it is supposed to cover, so a green result here proves nothing",
			len(dirs), identityGoldenRoots)
	}

	var claimed int
	for _, dir := range dirs {
		report, panicked, _ := identityGoldenAnalyze(t.Context(), dir)
		if panicked != "" || !report.Readable() {
			continue
		}
		for _, res := range report.Identities {
			if !identityClassClaimsAnAnswer(res.Class) {
				continue
			}
			claimed++
			if identityPayloadEmpty(res) {
				t.Errorf("%s: %s resolved %s and identifies nothing - ImportID, IdentityValues and Formula are all empty.\n"+
					"A resolution in this class asserts it can name a live object. This one cannot, and every count in this "+
					"repository reads it as a resolved instance.",
					rel(root, dir), res.Addr.String(), res.Class)
			}
		}
	}

	// The anti-tamper leg, in the spirit of identityGoldenSweepFloor. A guard
	// that inspects nothing passes trivially, and the ways this one could come
	// to inspect nothing - a class rename, a sweep that stops reaching the
	// fixtures, a report shape change - are all silent.
	if claimed < 500 {
		t.Fatalf("only %d instances resolved into a class that claims an answer; this guard is inspecting far too little to mean anything", claimed)
	}
	t.Logf("checked %d resolutions across %d configuration directories", claimed, len(dirs))
}

// identityClassClaimsAnAnswer reports whether a class asserts that it can name
// a live object, now or once its parents are known.
//
// Written as an explicit list rather than as "not needs-discovery and not
// record-backed" on purpose: a class added later should have to be classified
// deliberately, and the test below fails when one is not.
func identityClassClaimsAnAnswer(c identity.Class) bool {
	switch c {
	case identity.ClassConcrete, identity.ClassParentDerived:
		return true
	default:
		return false
	}
}

// identityPayloadEmpty is the "identifies nothing" predicate.
func identityPayloadEmpty(res identity.Resolution) bool {
	if res.ImportID != "" {
		return false
	}
	for _, v := range res.IdentityValues {
		if v != "" {
			return false
		}
	}
	if res.Formula != nil && res.Formula.String() != "" {
		return false
	}
	return true
}

// TestEveryIdentityClassIsClassified fails when a class is added and nobody
// decides whether it claims an answer.
//
// Without it, a new class defaults to "carries nothing legitimately" through
// identityClassClaimsAnAnswer's default branch, and the guard above silently
// stops covering it. That is the shape an audit found in a registry scanner
// that recorded the shapes it recognised and skipped the rest: it reported
// everything registered because it was blind.
//
// The roster is read out of the identity package's own source rather than
// written down here, so adding a class is enough to trip it. A hand list would
// have to be updated by the same person who forgot to update the switch.
func TestEveryIdentityClassIsClassified(t *testing.T) {
	// The classes this file has been told about. A class missing here is the
	// failure; a class here that no longer exists stops compiling.
	classified := map[identity.Class]bool{
		identity.ClassConcrete:       true,
		identity.ClassParentDerived:  true,
		identity.ClassNeedsDiscovery: false,
		identity.ClassRecordBacked:   false,
	}

	declared := identityClassesDeclaredInSource(t, flocitest.RepoRoot(t))
	if len(declared) < len(classified) {
		t.Fatalf("the source scan found %d identity classes (%v) against the %d this file names; the scan is broken rather than the package having shrunk",
			len(declared), declared, len(classified))
	}
	for _, c := range declared {
		want, known := classified[c]
		if !known {
			t.Errorf("identity class %q is declared in internal/live/identity but not classified in emptyidentity_test.go; decide whether it claims to name a live object and add it to the map, or TestNoResolutionIdentifiesNothing silently stops covering it", c)
			continue
		}
		if got := identityClassClaimsAnAnswer(c); got != want {
			t.Errorf("identityClassClaimsAnAnswer(%q) = %v, want %v", c, got, want)
		}
	}
}

// identityClassesDeclaredInSource reads every `Class = "..."` constant out of
// internal/live/identity. Go has no enum reflection, so the package's source is
// the only place the roster actually exists.
func identityClassesDeclaredInSource(t *testing.T, root string) []identity.Class {
	t.Helper()

	dir := filepath.Join(root, "internal", "live", "identity")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %s", dir, err)
	}

	var out []identity.Class
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
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
							continue
						}
						s, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatalf("unquoting %s: %s", lit.Value, err)
						}
						out = append(out, identity.Class(s))
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
