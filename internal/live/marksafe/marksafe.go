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
// [UnsafeMethods] is derived by calling every method on [cty.Value] twice -
// once against a marked sample value, once against the same sample unmarked -
// and recording the ones whose outcome the mark changed. The set therefore
// tracks the cty version in go.mod rather than someone's memory of it. Each
// narrower question that was asked here first missed something real: a hand
// list missed False, matching cty's assertUnmarked message missed Hash and
// Range, and calling only no-argument methods without driving their results
// missed ForEachElement and Elements. See [UnsafeMethods] for which was which.
//
// [Scan] then finds every call of one of those methods in the packages it
// is given and attaches the proof, if any, that the receiver cannot be
// marked at that point. A site with no proof is a failure. It is not
// recorded and skipped: a scanner that quietly ignores what it does not
// recognise reports that everything is safe precisely because it can see
// nothing, which is how the sibling scanner in internal/live/refusalscan
// was defeated four ways in one sitting.
//
// The derivation yields method NAMES and the scanner matches names, so the
// two disagree whenever another type has a method of the same name. That was
// harmless until Range was derived, since hcl.Expression.Range is everywhere
// in these packages. [ReceiverIndex] closes it with the Go type checker, and
// only in the safe direction: a receiver resolved to something other than
// cty.Value is proven, a receiver that cannot be resolved is not.
//
// # What counts as a proof
//
// [Proof] enumerates them. Each is a syntactic fact about the receiver
// inside one function, not a whole-program dataflow: the analysis is
// deliberately shallow, so that a site it cannot prove is a site a reader
// cannot prove either. Widening it is how this check stops meaning
// anything.
//
// A fact holds over a SPAN rather than from a position onwards, and the span
// is the region the Go language says the test governs - an if body, an else
// body, the rest of the enclosing block after a guard that returns, or the
// right-hand operand of a && or || whose left operand did the testing. A
// fact is void from the moment its name is assigned again.
//
// # What it still does not see
//
// These are gaps, not accepted proofs. Each makes the check refuse something
// it cannot follow, so the cost is a call site someone has to write a
// visible guard at:
//
//   - Nothing crosses a function boundary. A value returned already checked
//     by its callee carries no proof here, and the fix is a test at the call
//     site rather than a comment about the callee.
//   - Dominance is approximated by the regions above. A guard in a shape
//     outside them - a switch case, a goto, a boolean stored in a variable
//     and tested later - proves nothing.
//   - A method value, f := v.AsString, is not a call and is invisible.
//   - Receivers are compared as rendered text, so two values that spell the
//     same in one function are one value here. Assignment to the name voids
//     the fact, but mutation reached another way - m["k"] after m is written
//     through - is not tracked.
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

// markedPanicMessage is the panic cty raises from assertUnmarked. Nothing in
// the derivation matches on it - see [UnsafeMethods] for why - but the
// dynamic half of this package's tests uses it to tell the crash class it
// guards from an unrelated one.
const markedPanicMessage = "value is marked, so must be unmarked first"

// capsuleThing gives markSamples a capsule-typed value to mark. cty has one
// accessor, EncapsulatedValue, that is reachable on no other kind, and
// without a sample of the kind its behaviour on a marked receiver is
// indistinguishable from its behaviour on a wrongly-typed one.
type capsuleThing struct{ s string }

var capsuleType = cty.Capsule("marksafe sample", reflect.TypeOf(capsuleThing{}))

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
		cty.CapsuleVal(capsuleType, &capsuleThing{s: "x"}).Mark(mark),
	}
}

// UnsafeMethods are the [cty.Value] methods that panic rather than error
// when the receiver is marked, derived by calling every one of them twice -
// once on a marked sample, once on the same sample unmarked - and recording
// the ones whose outcome the mark changed.
//
// The question asked is deliberately "did the mark change what happened",
// not "did cty print its assertUnmarked message". Three earlier answers were
// each narrower than the phenomenon:
//
//   - A hand-written list missed False, which was three of the eight sites
//     issue #240 turned up.
//   - Matching cty's assertUnmarked text missed Hash and Range, which raise
//     their own wording from set_internals.go and value_range.go. Neither
//     goes through assertUnmarked at all, so no amount of care with that
//     one constant would have found them.
//   - Calling only no-argument methods missed ForEachElement, which takes a
//     callback and calls assertUnmarked itself, and calling without driving
//     the result missed Elements, whose panic happens inside the iter.Seq2
//     it returns and so only when a caller ranges over it. cty's own
//     documentation on ElementIterator says "New code should prefer to use
//     Value.Elements", so that is the accessor the next person reaches for.
//
// The comparison is on the panic MESSAGE, not merely on whether a panic
// happened, because several accessors reject a wrong-typed receiver before
// they would have returned: EncapsulatedValue panics either way on a string,
// and only the wording says which reason. A method whose message legitimately
// varied with the mark for some other reason would be a false positive here,
// which costs one spurious entry in a recorded list a human reads; the
// opposite error costs a crashed run.
func UnsafeMethods() map[string]bool {
	out := map[string]bool{}
	rt := reflect.TypeOf(cty.Value{})
	for _, marked := range markSamples() {
		plain, _ := marked.UnmarkDeep()
		mv, uv := reflect.ValueOf(marked), reflect.ValueOf(plain)
		for i := 0; i < rt.NumMethod(); i++ {
			m := rt.Method(i)
			if callOutcome(mv.Method(i), syntheticArgs(m.Type)) != callOutcome(uv.Method(i), syntheticArgs(m.Type)) {
				out[m.Name] = true
			}
		}
	}
	return out
}

// noPanic is callOutcome's answer for a call that returned. It is not a
// possible panic value, so it cannot collide with one.
const noPanic = "\x00no panic"

// callOutcome calls one method and reports what happened: the panic value,
// or noPanic. Every returned value is driven in case the work has been
// deferred into it.
func callOutcome(m reflect.Value, in []reflect.Value) (outcome string) {
	defer func() {
		if r := recover(); r != nil {
			outcome = fmt.Sprint(r)
		}
	}()
	for _, r := range m.Call(in) {
		driveIterator(r)
	}
	return noPanic
}

// driveIterator ranges over a returned range-over-func value, taking the
// first element and stopping. A method that returns iter.Seq or iter.Seq2
// has done nothing at all when it returns - the body runs inside the
// closure - so calling it and looking at the result observes nothing.
//
// Recognised structurally rather than by naming iter.Seq2: any func taking
// one argument, that argument itself being a func returning a single bool,
// is a range-over-func by the language's own definition, so a future cty
// method returning one is driven the day it lands.
func driveIterator(v reflect.Value) {
	t := v.Type()
	if t.Kind() != reflect.Func || t.NumIn() != 1 || t.NumOut() != 0 || t.IsVariadic() {
		return
	}
	yield := t.In(0)
	if yield.Kind() != reflect.Func || yield.NumOut() != 1 || yield.Out(0).Kind() != reflect.Bool {
		return
	}
	stop := reflect.MakeFunc(yield, func([]reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(false)}
	})
	v.Call([]reflect.Value{stop})
}

// syntheticArgs invents one argument per parameter so that methods taking
// arguments are driven too. A zero value does for everything except a
// callback, which has to be callable: ForEachElement invokes its argument
// per element, and a nil func there would crash for a reason that has
// nothing to do with marks.
//
// Inventing a zero argument makes several methods panic about the argument
// rather than about the receiver - Index on a zero key, say. That costs
// nothing: the same wrong argument is passed to the marked and the unmarked
// call, so an argument-shaped complaint appears on both sides and cancels.
func syntheticArgs(mt reflect.Type) []reflect.Value {
	n := mt.NumIn()
	out := make([]reflect.Value, 0, n-1)
	for i := 1; i < n; i++ {
		t := mt.In(i)
		if mt.IsVariadic() && i == n-1 {
			t = t.Elem()
		}
		if t.Kind() == reflect.Func {
			ft := t
			out = append(out, reflect.MakeFunc(ft, func([]reflect.Value) []reflect.Value {
				res := make([]reflect.Value, ft.NumOut())
				for j := range res {
					res[j] = reflect.Zero(ft.Out(j))
				}
				return res
			}))
			continue
		}
		out = append(out, reflect.Zero(t))
	}
	return out
}

// Proof is why one call site cannot see a marked receiver. The empty string
// means nothing proves it, which is the failure this package exists to
// report.
type Proof string

const (
	// ProofGuarded is the receiver being tested with IsMarked,
	// ContainsMarked, HasMark or marks.Contains somewhere the Go language
	// says the test governs the read: the body the test opens, the else it
	// falls to, the rest of the enclosing block after a test whose body
	// returns, or the operand a && or || only evaluates because the test
	// came out the right way.
	//
	// The first version of this was position-ordering alone - any test
	// written above the read, anywhere in the function - and it recorded
	// this proof for four shapes where no test governs the read at all: an
	// if with an empty body, an if that falls through, a test in a sibling
	// branch, and a name reassigned between the test and the read. A
	// recorded proof that does not hold is worse than an admitted gap,
	// because the only thing this package sells is that a green result
	// means something.
	//
	// It is still not a dominance analysis, and the regions above are the
	// whole of what it understands. A guard in any other shape proves
	// nothing, which costs a visible test at a call site and no more.
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

	// ProofNotCtyValue is the receiver having been resolved by the Go type
	// checker to a type that is not cty.Value, so the method called is a
	// different method that happens to share a name.
	//
	// This exists because the derivation and the scanner disagree about what
	// a method IS. The derivation reflects over cty.Value and yields method
	// NAMES; the scanner matches those names against selector expressions,
	// which carry no type. That was accurate while every unsafe name was
	// peculiar to cty - measured, and it was, for the ten the first version
	// found. Deriving Range broke it: cty.Value.Range is mark-unsafe, and
	// hcl.Expression.Range is the single most-called method in these
	// packages, so name matching alone turns one real question into 97
	// spurious ones and the check stops meaning anything.
	//
	// Resolution is one-directional on purpose. A receiver the type checker
	// resolves to something else is proven; a receiver it cannot resolve at
	// all is NOT, and stays a failure. A missing or broken index therefore
	// makes this check louder rather than blind, which is the direction the
	// sibling scanner in internal/live/refusalscan got wrong.
	ProofNotCtyValue Proof = "the receiver is not a cty.Value"

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
	// RecvType is the receiver's type as the Go type checker resolved it,
	// or empty when no [ReceiverIndex] covered this position. Empty means
	// unknown, not safe.
	RecvType string
	Proof    Proof
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
// recv may be nil, in which case no receiver is resolved and a site whose
// method name collides with an unrelated type's is reported unproven. That is
// the safe direction, and it is what the planted-file tests rely on.
func Scan(dirs []string, unsafeMethods map[string]bool, recv ReceiverIndex) ([]Site, error) {
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
			out = append(out, scanFile(fset, path, file, unsafeMethods, recv)...)
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

func scanFile(fset *token.FileSet, path string, file *ast.File, unsafeMethods map[string]bool, recv ReceiverIndex) []Site {
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
			if !ok || !unsafeMethods[sel.Sel.Name] {
				return true
			}
			pos := fset.Position(sel.Sel.Pos())
			site := Site{
				File:     path,
				Line:     pos.Line,
				Func:     fn.Name.Name,
				Method:   sel.Sel.Name,
				Recv:     exprString(sel.X),
				RecvType: recv[receiverKey(path, pos)],
			}
			if site.RecvType != "" && !isCtyValue(site.RecvType) {
				site.Proof = ProofNotCtyValue
			} else {
				site.Proof = f.proofFor(sel.X, sel.Sel.Pos())
			}
			out = append(out, site)
			return true
		})
	}
	return out
}

// funcFacts is what one function body says about its own values.
//
// A fact is not a position but a SPAN: the region of the function over which
// the fact holds. The first version of this recorded only the earliest
// position at which each fact was established and accepted any read below it,
// which is a rule with no notion of control flow at all - so an `if` that
// tested a value and did nothing licensed every read under it, and so did a
// test in a branch the read never runs in. Both of those recorded a proof
// that does not hold, which is worse than an admitted gap: the whole value of
// this package is that a green result means something.
//
// The spans are still shallow, and deliberately. They come from two shapes,
// which between them are every mark guard written in this repository:
//
//   - `if X.IsMarked() { ...; return }` proves X unmarked from the END of the
//     if statement to the end of the BLOCK CONTAINING IT. Not to the end of
//     the function: a read in a later sibling branch, or after the block
//     closes, is not something this if statement decided.
//   - `if !X.IsMarked() { ... }` proves X unmarked INSIDE the if's body, and
//     nowhere else.
//
// The terminating-body requirement is what makes the first sound. An `if`
// whose body falls through has not established anything about the code after
// it, and an `if` with an empty body has not established anything at all.
type funcFacts struct {
	guarded   map[string][]span
	unmarked  map[string][]span
	literal   map[string][]span
	built     map[string][]span
	iterKey   map[string][]span
	converted map[string][]conversion
	// assigned holds every position at which a name is written. A fact about
	// a name is void from the moment the name is made to hold something else:
	// after `a = b`, whatever was proven about the old `a` is a fact about a
	// value this function no longer has under that name.
	assigned map[string][]token.Pos
	recovers bool
}

// span is the half-open region [from, to) over which one fact holds.
type span struct {
	from token.Pos
	to   token.Pos
}

type conversion struct {
	src  string
	span span
}

func newFuncFacts() *funcFacts {
	return &funcFacts{
		guarded:   map[string][]span{},
		unmarked:  map[string][]span{},
		literal:   map[string][]span{},
		built:     map[string][]span{},
		iterKey:   map[string][]span{},
		converted: map[string][]conversion{},
		assigned:  map[string][]token.Pos{},
	}
}

func analyzeFunc(fn *ast.FuncDecl) *funcFacts {
	f := newFuncFacts()
	f.walkBlock(fn.Body)
	return f
}

func (f *funcFacts) walkBlock(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	for _, st := range b.List {
		f.walkStmt(st, b.Rbrace)
	}
}

// walkStmt records the facts one statement establishes. blockEnd is where the
// enclosing block closes, which is how far a fact established here can reach.
func (f *funcFacts) walkStmt(st ast.Stmt, blockEnd token.Pos) {
	if st == nil {
		return
	}
	// A function literal anywhere inside this statement is its own block. Its
	// positions nest inside the statement's, so the span arithmetic scopes it
	// correctly without a separate mechanism.
	//
	// The same pass records what Go's short-circuit evaluation proves WITHIN
	// a single expression, which is where the most common guard in these
	// packages lives:
	//
	//	if err != nil || ks.IsNull() || ks.IsMarked() || ks.AsString() != key {
	//
	// AsString runs only when every operand to its left was false, so the
	// IsMarked test beside it is a proof for it - over the extent of the
	// right operand and nowhere else. A rule that only looked at whole
	// statements reported this line, which is itself one of issue #240's
	// fixes, as unproven.
	ast.Inspect(st, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			f.walkBlock(x.Body)
		case *ast.BinaryExpr:
			// A || B evaluates B only when A was false; A && B evaluates B
			// only when A was true.
			var known bool
			switch x.Op {
			case token.LOR:
				known = false
			case token.LAND:
				known = true
			default:
				return true
			}
			rhs := span{from: x.Y.Pos(), to: x.Y.End()}
			for _, g := range guardsWhen(x.X, known) {
				if !g.marked {
					f.note(f.guarded, g.name, rhs)
				}
			}
		}
		return true
	})

	switch node := st.(type) {
	case *ast.DeferStmt:
		if containsRecover(node) {
			f.recovers = true
		}
	case *ast.AssignStmt:
		f.noteAssign(node, blockEnd)
	case *ast.BlockStmt:
		f.walkBlock(node)
	case *ast.IfStmt:
		f.walkIf(node, blockEnd)
	case *ast.ForStmt:
		f.walkStmt(node.Init, node.Body.Rbrace)
		f.walkStmt(node.Post, node.Body.Rbrace)
		f.walkBlock(node.Body)
	case *ast.RangeStmt:
		if node.Tok == token.DEFINE || node.Tok == token.ASSIGN {
			f.noteName(node.Key, node.Body.Lbrace)
			f.noteName(node.Value, node.Body.Lbrace)
		}
		f.walkBlock(node.Body)
	case *ast.SwitchStmt:
		f.walkStmt(node.Init, node.Body.Rbrace)
		f.walkBlock(node.Body)
	case *ast.TypeSwitchStmt:
		f.walkStmt(node.Init, node.Body.Rbrace)
		f.walkStmt(node.Assign, node.Body.Rbrace)
		f.walkBlock(node.Body)
	case *ast.CaseClause:
		for _, s := range node.Body {
			f.walkStmt(s, node.End())
		}
	case *ast.CommClause:
		for _, s := range node.Body {
			f.walkStmt(s, node.End())
		}
	case *ast.SelectStmt:
		f.walkBlock(node.Body)
	case *ast.LabeledStmt:
		f.walkStmt(node.Stmt, blockEnd)
	}
}

// walkIf records what an if statement's condition proves, and where.
//
// Three regions, each with its own reason:
//
//	if C { body } else { alt }
//	<after>
//
// In body, C is true. In alt, C is false. After the statement, C is false
// only if body left - which is what bodyTerminates asks.
func (f *funcFacts) walkIf(node *ast.IfStmt, blockEnd token.Pos) {
	f.walkStmt(node.Init, node.End())

	for _, g := range guardsWhen(node.Cond, true) {
		if !g.marked {
			f.note(f.guarded, g.name, span{from: node.Body.Lbrace, to: node.Body.Rbrace})
		}
	}
	for _, g := range guardsWhen(node.Cond, false) {
		if g.marked {
			continue
		}
		if els, ok := node.Else.(*ast.BlockStmt); ok {
			f.note(f.guarded, g.name, span{from: els.Lbrace, to: els.Rbrace})
		}
		if bodyTerminates(node.Body) {
			f.note(f.guarded, g.name, span{from: node.End(), to: blockEnd})
		}
	}

	f.walkBlock(node.Body)

	// An `else if` inherits this block's reach only when the then-branch
	// leaves. Otherwise control can arrive below the whole chain without the
	// else's condition ever having been evaluated, and a guard in it proves
	// nothing there - so the chain's own end is as far as the else can see.
	elseEnd := blockEnd
	if !bodyTerminates(node.Body) {
		elseEnd = node.End()
	}
	f.walkStmt(node.Else, elseEnd)
}

// bodyTerminates reports whether control certainly leaves the block, which is
// what makes the code AFTER the if statement dominated by the condition being
// false. An empty body never terminates, which is the whole point: an if that
// tests a value and does nothing proves nothing about anything.
func bodyTerminates(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	switch last := b.List[len(b.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		// break, continue and goto all leave the block.
		return true
	case *ast.ExprStmt:
		if call, ok := last.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
		}
	case *ast.BlockStmt:
		return bodyTerminates(last)
	case *ast.IfStmt:
		// if/else where both arms leave.
		if els, ok := last.Else.(*ast.BlockStmt); ok {
			return bodyTerminates(last.Body) && bodyTerminates(els)
		}
	}
	return false
}

// guard is one mark test together with the truth value the surrounding
// condition forces on it.
type guard struct {
	name string
	// marked is what X.IsMarked() must have returned. Only false is a proof.
	marked bool
}

// guardsWhen returns the mark tests whose result is forced when cond
// evaluates to want, following the boolean algebra rather than guessing at
// it:
//
//	cond is TRUE   =>  every conjunct of an && chain is true
//	cond is FALSE  =>  every disjunct of an || chain is false
//
// and nothing at all in the other two combinations, since either operand can
// be the one that decided a false && or a true ||. Negation swaps want.
//
// Getting this backwards is not a theoretical worry. The refusal written most
// often in these packages is
//
//	if diags.HasErrors() || val.IsNull() || val.IsMarked() { continue }
//
// where the guard sits in an || chain and is sound precisely because the
// chain is false below the if. A rule that dropped guards reached through ||
// reported 26 working call sites as unproven.
func guardsWhen(cond ast.Expr, want bool) []guard {
	var out []guard
	var walk func(e ast.Expr, want bool)
	walk = func(e ast.Expr, want bool) {
		switch x := e.(type) {
		case *ast.ParenExpr:
			walk(x.X, want)
		case *ast.UnaryExpr:
			if x.Op == token.NOT {
				walk(x.X, !want)
			}
		case *ast.BinaryExpr:
			switch {
			case x.Op == token.LAND && want:
				walk(x.X, true)
				walk(x.Y, true)
			case x.Op == token.LOR && !want:
				walk(x.X, false)
				walk(x.Y, false)
			}
		case *ast.CallExpr:
			if name, ok := guardName(x); ok {
				out = append(out, guard{name: name, marked: want})
			}
		}
	}
	walk(cond, want)
	return out
}

// guardName reports the value a mark test is asking about.
func guardName(call *ast.CallExpr) (string, bool) {
	fun, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if markGuards[fun.Sel.Name] {
		return exprString(fun.X), true
	}
	// marks.Contains(v, marks.Sensitive) and marks.Has(v, ...): the
	// package-qualified form of the same question.
	if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "marks" && len(call.Args) > 0 {
		switch fun.Sel.Name {
		case "Contains", "Has":
			return exprString(call.Args[0]), true
		}
	}
	return "", false
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

// unmarkers produce a value with its top-level marks removed.
var unmarkers = map[string]bool{
	"Unmark":              true,
	"UnmarkDeep":          true,
	"unmarkForce":         true,
	"UnmarkDeepWithPaths": true,
}

func (f *funcFacts) noteAssign(as *ast.AssignStmt, blockEnd token.Pos) {
	// Every name written here loses whatever was proven about it, whatever
	// the right-hand side is. Recorded before the new fact so that the
	// assignment establishing a fact does not immediately void it: the void
	// is at the same position the fact starts, and holdsAt is strict.
	for _, lhs := range as.Lhs {
		f.noteName(lhs, as.End())
	}

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
	live := span{from: as.End(), to: blockEnd}

	fun, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	switch {
	case unmarkers[fun.Sel.Name]:
		f.note(f.unmarked, first, live)
	case fun.Sel.Name == "Value" && len(call.Args) == 1 && isNilIdent(call.Args[0]):
		f.note(f.literal, first, live)
	case fun.Sel.Name == "Element" && len(as.Lhs) == 2:
		// k, v := it.Element(): the key is synthesized by cty and
		// carries no mark; the value is whatever the collection held.
		f.note(f.iterKey, first, live)
	case isCtyConstructor(fun):
		f.note(f.built, first, live)
	case fun.Sel.Name == "Convert":
		if pkg, ok := fun.X.(*ast.Ident); ok && (pkg.Name == "convert" || pkg.Name == "ctyconvert") && len(call.Args) > 0 && first != "" {
			f.converted[first] = append(f.converted[first], conversion{src: exprString(call.Args[0]), span: live})
		}
	}
}

// noteName records that a name was written at pos.
func (f *funcFacts) noteName(e ast.Expr, pos token.Pos) {
	if e == nil {
		return
	}
	if name := exprString(e); name != "" && name != "_" {
		f.assigned[name] = append(f.assigned[name], pos)
	}
}

// note records one span over which a fact holds.
func (f *funcFacts) note(m map[string][]span, name string, s span) {
	if name == "" || s.to <= s.from {
		return
	}
	m[name] = append(m[name], s)
}

// holds reports whether a fact about name covers the read at use, and whether
// the name still holds the value the fact was about.
func (f *funcFacts) holds(m map[string][]span, name string, use token.Pos) bool {
	for _, s := range m[name] {
		if f.holdsAt(name, s, use) {
			return true
		}
	}
	return false
}

func (f *funcFacts) holdsAt(name string, s span, use token.Pos) bool {
	if use <= s.from || use >= s.to {
		return false
	}
	for _, w := range f.assigned[name] {
		if w > s.from && w < use {
			return false
		}
	}
	return true
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
	if sel, ok := recv.(*ast.SelectorExpr); ok {
		// cty.True.False(), and the like.
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "cty" {
			return ProofConstructed
		}
	}

	name := exprString(recv)
	switch {
	case f.holds(f.guarded, name, use):
		return ProofGuarded
	case f.holds(f.unmarked, name, use):
		return ProofUnmarked
	case f.holds(f.literal, name, use):
		return ProofLiteralEval
	case f.holds(f.built, name, use):
		return ProofConstructed
	case f.holds(f.iterKey, name, use):
		return ProofIteratorKey
	}
	// convert.Convert carries the question through: the result is proven
	// exactly when its source was. Followed to a fixed depth so a cycle in
	// hand-written code cannot spin here.
	if f.provenConverted(name, use, 0) {
		return ProofConverted
	}

	if f.recovers {
		return ProofRecovered
	}
	return ""
}

func (f *funcFacts) provenConverted(name string, use token.Pos, depth int) bool {
	if depth >= 8 {
		return false
	}
	for _, c := range f.converted[name] {
		if !f.holdsAt(name, c.span, use) {
			continue
		}
		at := c.span.from
		if f.holds(f.guarded, c.src, at) ||
			f.holds(f.unmarked, c.src, at) ||
			f.holds(f.literal, c.src, at) ||
			f.holds(f.built, c.src, at) ||
			f.holds(f.iterKey, c.src, at) ||
			f.provenConverted(c.src, at, depth+1) {
			return true
		}
	}
	return false
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
		// The arguments are part of the identity. Rendering these as
		// "fn(...)" made a guard on get(m, "safe") match a read of
		// get(m, "danger") - two different values, one recorded proof.
		args := make([]string, 0, len(x.Args))
		for _, a := range x.Args {
			args = append(args, exprString(a))
		}
		if x.Ellipsis.IsValid() {
			args = append(args, "...")
		}
		return exprString(x.Fun) + "(" + strings.Join(args, ", ") + ")"
	}
	return fmt.Sprintf("<expr@%d>", e.Pos())
}
