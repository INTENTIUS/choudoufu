// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package marksafe is the lockstep check behind GitHub issue #240: a
// cty.Value accessor that panics on a marked value cannot be called in a
// live package on a value nothing proves is unmarked.
//
// # The shape it exists for
//
// A sensitive input variable produces a marked cty.Value, and marks
// propagate through every derived expression. Some cty accessors handle a
// mark; others call assertUnmarked and panic. The three guards this
// repository reaches for first - convert.Convert, IsNull, IsKnown - are all
// in the first group, so the usual sequence
//
//	v, err := convert.Convert(x, cty.String)
//	if err != nil || v.IsNull() || !v.IsKnown() {
//		return
//	}
//	name := v.AsString() // panics when x was marked
//
// reads as thorough and is not. Four sites of exactly that shape shipped
// green in one month; a fifth, sixth, seventh and eighth were found by this
// package's own sweep. Refusing a marked value is the correct behaviour -
// an identity component becomes a cloud tag in plaintext - so the fix at
// each site is a diagnostic naming sensitivity, never an unmark.
//
// # Nothing here is a hand list
//
// [UnsafeMethods] is derived by calling every no-argument method on
// [cty.Value] against marked sample values of every kind and recording
// which ones panic with cty's own assertUnmarked message. The set therefore
// tracks the cty version in go.mod rather than someone's memory of it: the
// first hand list written for this work missed False, which is the second
// most common of the eight sites found.
//
// [Scan] then finds every call of one of those methods in the packages it
// is given and attaches the proof, if any, that the receiver cannot be
// marked at that point. A site with no proof is a failure. It is not
// recorded and skipped: a scanner that quietly ignores what it does not
// recognise reports that everything is safe precisely because it can see
// nothing, which is how the sibling scanner in internal/live/refusalscan
// was defeated four ways in one sitting.
//
// # What counts as a proof
//
// [Proof] enumerates them. Each is a syntactic fact about the receiver
// inside one function, not a whole-program dataflow: the analysis is
// deliberately shallow, so that a site it cannot prove is a site a reader
// cannot prove either. Widening it is how this check stops meaning
// anything.
package marksafe

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// markedPanicMessage is the panic cty raises from assertUnmarked. Matching
// on it rather than on any panic keeps [UnsafeMethods] from recording a
// method that panicked for an unrelated reason - "not bool", say, which
// True raises on a string.
const markedPanicMessage = "value is marked, so must be unmarked first"

// markSamples are the value kinds a marked receiver can have. Every kind is
// tried because the accessors are type-specific: AsString panics only on a
// marked string, ElementIterator only on a marked collection, and a set
// built from marked elements hoists their marks to itself while a list,
// map, object and tuple do not.
func markSamples() []cty.Value {
	mark := "sensitive"
	return []cty.Value{
		cty.StringVal("x").Mark(mark),
		cty.NumberIntVal(3).Mark(mark),
		cty.True.Mark(mark),
		cty.ListVal([]cty.Value{cty.StringVal("a")}).Mark(mark),
		cty.MapVal(map[string]cty.Value{"a": cty.StringVal("b")}).Mark(mark),
		cty.SetVal([]cty.Value{cty.StringVal("a")}).Mark(mark),
		cty.ObjectVal(map[string]cty.Value{"a": cty.StringVal("b")}).Mark(mark),
		cty.TupleVal([]cty.Value{cty.StringVal("a")}).Mark(mark),
		cty.EmptyObjectVal.Mark(mark),
	}
}

// UnsafeMethods are the no-argument [cty.Value] methods that panic rather
// than error when the receiver is marked, derived by calling them.
//
// Methods taking arguments are outside what reflection can drive without
// inventing values for them, and are covered instead by the fact that every
// one this repository calls - Index, GetAttr, HasIndex, Equals - either
// handles marks itself or returns a value whose own accessor is in this
// set.
func UnsafeMethods() map[string]bool {
	out := map[string]bool{}
	rt := reflect.TypeOf(cty.Value{})
	for _, sample := range markSamples() {
		rv := reflect.ValueOf(sample)
		for i := 0; i < rt.NumMethod(); i++ {
			m := rt.Method(i)
			if m.Type.NumIn() != 1 {
				continue
			}
			if panicsMarked(rv.Method(i)) {
				out[m.Name] = true
			}
		}
	}
	return out
}

func panicsMarked(m reflect.Value) (yes bool) {
	defer func() {
		if r := recover(); r != nil {
			yes = fmt.Sprint(r) == markedPanicMessage
		}
	}()
	m.Call(nil)
	return false
}

// Proof is why one call site cannot see a marked receiver. The empty string
// means nothing proves it, which is the failure this package exists to
// report.
type Proof string

const (
	// ProofGuarded is the receiver being tested with IsMarked,
	// ContainsMarked, HasMark or marks.Contains EARLIER IN THE SAME
	// FUNCTION than the read. Position rather than dominance: a control-flow
	// analysis would be the only part of this anyone had to take on trust,
	// while "the guard is written above the read" is a fact a reader
	// checks in a second. A guard below its read does not count, which is
	// the version of this that would otherwise have accepted "the function
	// mentions IsMarked somewhere".
	ProofGuarded Proof = "guarded by an IsMarked test on the same value"

	// ProofUnmarked is the receiver coming out of Unmark, UnmarkDeep or
	// unmarkForce. Note that Unmark strips only the top-level mark, so this
	// proof does NOT extend to the elements of an unmarked collection; see
	// ProofIteratorKey.
	ProofUnmarked Proof = "produced by Unmark or UnmarkDeep"

	// ProofLiteralEval is the receiver coming out of an hcl.Expression
	// evaluated against a nil EvalContext. With no variables and no
	// functions in scope, such an expression either fails or produces a
	// constant, and a constant carries no marks.
	ProofLiteralEval Proof = "evaluated against a nil hcl.EvalContext"

	// ProofConstructed is the receiver being built here, by a cty
	// constructor or a package-level cty constant.
	ProofConstructed Proof = "constructed in this function by cty"

	// ProofIteratorKey is the receiver being the KEY half of an element
	// iterator's Element(). cty synthesizes those - a StringVal for a map
	// or object attribute name, a NumberIntVal for a list or tuple index -
	// so a key is never marked even when the value beside it is. The value
	// half gets no proof from this, which is what the eighth site found by
	// this package's sweep turned on.
	ProofIteratorKey Proof = "the key half of an ElementIterator element"

	// ProofConverted is the receiver coming out of convert.Convert applied
	// to something already proven. Convert preserves marks, so it neither
	// adds nor removes the question.
	ProofConverted Proof = "converted from a value proven above"

	// ProofRecovered is the enclosing function having a deferred recover.
	// A panic there becomes the function's own not-ok answer rather than a
	// crashed run, so the class this package guards cannot escape - but the
	// user gets whatever generic outcome that function reports rather than
	// a diagnostic naming sensitivity, so these are counted separately and
	// are worth converting to a real guard.
	ProofRecovered Proof = "the enclosing function recovers from panics"
)

// Site is one call of an unsafe method.
type Site struct {
	File   string
	Line   int
	Func   string
	Method string
	Recv   string
	Proof  Proof
}

// String renders a site the way a test failure should quote it.
func (s Site) String() string {
	return fmt.Sprintf("%s:%d %s: %s.%s()", s.File, s.Line, s.Func, s.Recv, s.Method)
}

// Scan parses every non-test .go file in each directory and returns every
// call of an unsafe method, in file and line order, each carrying its proof
// or the empty Proof when nothing proves it.
//
// Every file is read, not only those importing go-cty. Filtering on the
// import was the first version and it was a hole: a file can call
// helper().AsString() on a cty.Value without naming the package. Measured,
// the filter changed nothing - no other type in these packages has a method
// of one of these names - so it bought a blind spot and no accuracy. A
// same-named method on an unrelated type would be reported here, and the
// answer is to look at it rather than to stop looking.
func Scan(dirs []string, unsafeMethods map[string]bool) ([]Site, error) {
	var out []Site
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("marksafe: reading %s: %w", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("marksafe: parsing %s: %w", path, err)
			}
			out = append(out, scanFile(fset, path, file, unsafeMethods)...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func scanFile(fset *token.FileSet, path string, file *ast.File, unsafeMethods map[string]bool) []Site {
	var out []Site
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		f := analyzeFunc(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) != 0 || !unsafeMethods[sel.Sel.Name] {
				return true
			}
			out = append(out, Site{
				File:   path,
				Line:   fset.Position(sel.Sel.Pos()).Line,
				Func:   fn.Name.Name,
				Method: sel.Sel.Name,
				Recv:   exprString(sel.X),
				Proof:  f.proofFor(sel.X, sel.Sel.Pos()),
			})
			return true
		})
	}
	return out
}

// funcFacts is what one function body says about its own values.
type funcFacts struct {
	// Each map holds the EARLIEST position at which the fact was
	// established, so a proof written below the read it is supposed to
	// license does not count. That is the difference between "this function
	// mentions IsMarked somewhere" and "this value was tested before it was
	// read", and only the second is a proof.
	guarded   map[string]token.Pos
	unmarked  map[string]token.Pos
	literal   map[string]token.Pos
	built     map[string]token.Pos
	iterKey   map[string]token.Pos
	converted map[string]convertedFrom
	recovers  bool
}

type convertedFrom struct {
	src string
	pos token.Pos
}

func analyzeFunc(fn *ast.FuncDecl) *funcFacts {
	f := &funcFacts{
		guarded:   map[string]token.Pos{},
		unmarked:  map[string]token.Pos{},
		literal:   map[string]token.Pos{},
		built:     map[string]token.Pos{},
		iterKey:   map[string]token.Pos{},
		converted: map[string]convertedFrom{},
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.DeferStmt:
			if containsRecover(node) {
				f.recovers = true
			}
		case *ast.CallExpr:
			f.noteGuard(node)
		case *ast.AssignStmt:
			f.noteAssign(node)
		}
		return true
	})
	return f
}

func containsRecover(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		call, ok := m.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "recover" {
			found = true
		}
		return true
	})
	return found
}

// markGuards are the calls that ask whether a value carries a mark. A call
// to one of these on X is what licenses reading X below it.
var markGuards = map[string]bool{
	"IsMarked":       true,
	"ContainsMarked": true,
	"HasMark":        true,
	"HasSameMarks":   true,
}

func (f *funcFacts) noteGuard(call *ast.CallExpr) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if markGuards[fun.Sel.Name] {
			f.note(f.guarded, exprString(fun.X), call.Pos())
		}
		// marks.Contains(v, marks.Sensitive) and marks.Has(v, ...): the
		// package-qualified form of the same question.
		if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "marks" && len(call.Args) > 0 {
			switch fun.Sel.Name {
			case "Contains", "Has":
				f.note(f.guarded, exprString(call.Args[0]), call.Pos())
			}
		}
	}
}

// unmarkers produce a value with its top-level marks removed.
var unmarkers = map[string]bool{
	"Unmark":              true,
	"UnmarkDeep":          true,
	"unmarkForce":         true,
	"UnmarkDeepWithPaths": true,
}

func (f *funcFacts) noteAssign(as *ast.AssignStmt) {
	if len(as.Rhs) != 1 {
		return
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}
	first := ""
	if len(as.Lhs) > 0 {
		first = exprString(as.Lhs[0])
	}
	second := ""
	if len(as.Lhs) > 1 {
		second = exprString(as.Lhs[1])
	}
	_ = second

	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		switch {
		case unmarkers[fun.Sel.Name]:
			f.note(f.unmarked, first, as.End())
		case fun.Sel.Name == "Value" && len(call.Args) == 1 && isNilIdent(call.Args[0]):
			f.note(f.literal, first, as.End())
		case fun.Sel.Name == "Element" && len(as.Lhs) == 2:
			// k, v := it.Element(): the key is synthesized by cty and
			// carries no mark; the value is whatever the collection held.
			f.note(f.iterKey, first, as.End())
		case isCtyConstructor(fun):
			f.note(f.built, first, as.End())
		case fun.Sel.Name == "Convert":
			if pkg, ok := fun.X.(*ast.Ident); ok && (pkg.Name == "convert" || pkg.Name == "ctyconvert") && len(call.Args) > 0 && first != "" {
				if prev, seen := f.converted[first]; !seen || as.End() < prev.pos {
					f.converted[first] = convertedFrom{src: exprString(call.Args[0]), pos: as.End()}
				}
			}
		}
	}
}

// note records the earliest position at which one fact holds.
func (f *funcFacts) note(m map[string]token.Pos, name string, pos token.Pos) {
	if name == "" {
		return
	}
	if prev, seen := m[name]; !seen || pos < prev {
		m[name] = pos
	}
}

// holds reports whether a fact was established strictly before use.
func holds(m map[string]token.Pos, name string, use token.Pos) bool {
	pos, ok := m[name]
	return ok && pos < use
}

func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// isCtyConstructor recognises cty.StringVal and its siblings: a selector on
// the cty package whose name ends in Val or is one of the type-level
// constructors this repository builds values with.
func isCtyConstructor(sel *ast.SelectorExpr) bool {
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "cty" {
		return false
	}
	return strings.HasSuffix(sel.Sel.Name, "Val") || strings.HasSuffix(sel.Sel.Name, "Vals")
}

func (f *funcFacts) proofFor(recv ast.Expr, use token.Pos) Proof {
	// A receiver built inline: cty.StringVal("x").AsString(), or
	// something.Unmark() chained straight into the accessor.
	if call, ok := recv.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			switch {
			case unmarkers[sel.Sel.Name]:
				return ProofUnmarked
			case isCtyConstructor(sel):
				return ProofConstructed
			}
		}
	}
	if _, ok := recv.(*ast.SelectorExpr); ok {
		// cty.True.False(), and the like.
		if pkg, ok := recv.(*ast.SelectorExpr).X.(*ast.Ident); ok && pkg.Name == "cty" {
			return ProofConstructed
		}
	}

	name := exprString(recv)
	switch {
	case holds(f.guarded, name, use):
		return ProofGuarded
	case holds(f.unmarked, name, use):
		return ProofUnmarked
	case holds(f.literal, name, use):
		return ProofLiteralEval
	case holds(f.built, name, use):
		return ProofConstructed
	case holds(f.iterKey, name, use):
		return ProofIteratorKey
	}
	// convert.Convert carries the question through: the result is proven
	// exactly when its source was. Followed to a fixed depth so a cycle in
	// hand-written code cannot spin here.
	from, ok := f.converted[name]
	for depth := 0; ok && from.pos < use && depth < 8; depth++ {
		if holds(f.guarded, from.src, from.pos) ||
			holds(f.unmarked, from.src, from.pos) ||
			holds(f.literal, from.src, from.pos) ||
			holds(f.built, from.src, from.pos) ||
			holds(f.iterKey, from.src, from.pos) {
			return ProofConverted
		}
		from, ok = f.converted[from.src]
	}

	if f.recovers {
		return ProofRecovered
	}
	return ""
}

// exprString renders an expression the way a human would write it, for
// identity comparison between a guard and a use. It is deliberately partial:
// anything it cannot render becomes a unique unmatched string, so a receiver
// too complicated to name is a receiver nothing proves.
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.ParenExpr:
		return exprString(x.X)
	case *ast.IndexExpr:
		return exprString(x.X) + "[" + exprString(x.Index) + "]"
	case *ast.BasicLit:
		return x.Value
	case *ast.CallExpr:
		return exprString(x.Fun) + "(...)"
	}
	return fmt.Sprintf("<expr@%d>", e.Pos())
}
