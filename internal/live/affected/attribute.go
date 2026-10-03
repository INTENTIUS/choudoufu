// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package affected

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// lockFile is the dependency lock file's name.
const lockFile = ".terraform.lock.hcl"

// attribution accumulates a result while the changes are walked.
type attribution struct {
	a, b     *snapshot
	roots    map[string]*Root
	reasons  map[string]map[string]*Reason // root dir -> reason text -> reason
	indet    map[string]Indeterminacy      // kind+path -> entry
	unplaced map[string]Unplaced           // path -> entry
	// compare are the reached module directories a change landed in, whose
	// required_providers are compared across the revisions.
	compare map[string]bool
}

// attribute walks every change to the roots, then the pins, then the
// cross-estate readers. a is the base revision, b the head.
func attribute(a, b *snapshot, changes []change, ignore []string) *Result {
	at := &attribution{
		a: a, b: b,
		roots:    map[string]*Root{},
		reasons:  map[string]map[string]*Reason{},
		indet:    map[string]Indeterminacy{},
		unplaced: map[string]Unplaced{},
		compare:  map[string]bool{},
	}
	floatingB := floatingCalls(b)

	for _, c := range changes {
		for _, side := range []struct {
			snap *snapshot
			path string
		}{{a, c.Old}, {b, c.New}} {
			if side.path == "" {
				continue
			}
			at.place(side.snap, side.path, ignore, floatingB)
		}
	}

	at.providers()
	at.pins()
	at.loadErrors()
	at.dependents()
	return at.result()
}

func ignored(p string, patterns []string) bool {
	for _, pat := range patterns {
		if ok, _ := doublestar.Match(pat, p); ok {
			return true
		}
		// A pattern naming a directory covers what is under it.
		if ok, _ := doublestar.Match(strings.TrimSuffix(pat, "/")+"/**", p); ok {
			return true
		}
	}
	return false
}

// place attributes one path at one revision.
func (at *attribution) place(s *snapshot, p string, ignore []string, floating []string) {
	if ignored(p, ignore) {
		at.unplace(p, UnplacedIgnored, "matches -ignore")
		return
	}
	if path.Base(p) == lockFile {
		at.indeterminate(IndetLock, p, fmt.Sprintf("%s changed: a provider lock bump can change every root's plan", p))
	}
	dir, ok := s.nearestModuleDir(p)
	if !ok {
		if isDocumentation(p) {
			at.unplace(p, UnplacedDocs, "documentation outside every module")
			return
		}
		at.indeterminate(IndetUnplaced, p, fmt.Sprintf("%s is outside every module directory, and a plan can read such a file (a -var-file, a file() call, a wrapper's configuration) without the configuration saying so", p))
		return
	}
	_, isRoot := s.roots[dir]
	if !isRoot && len(floating) > 0 {
		at.indeterminate(IndetFloating, p, fmt.Sprintf("%s is module code, and %s calls a module by a floating source: a published version of this change may reach it, and only the version a run installs says whether it did", p, floating[0]))
	}
	users := s.users(dir)
	if len(users) == 0 {
		if !isRoot && len(floating) > 0 {
			return
		}
		at.unplace(p, UnplacedUnread, fmt.Sprintf("no root calls %s by a local path", dir))
		return
	}
	at.compare[dir] = true
	for _, g := range users {
		if g.Dir == dir {
			at.add(s, g, Reason{Kind: KindChanged, Paths: []string{p}, Text: KindChanged})
		} else {
			at.add(s, g, Reason{Kind: KindUses, Module: dir, Paths: []string{p}, Text: KindUses + " " + dir})
		}
	}
}

// floatingCalls lists, for a reason string, every floating module call in
// the head revision's roots as "<root> module.<call> (<source> <version>)".
func floatingCalls(s *snapshot) []string {
	var out []string
	for _, g := range s.roots {
		for name, c := range g.Calls {
			if c.Floating {
				out = append(out, fmt.Sprintf("%s module.%s (%s)", g.Dir, name, full(c)))
			}
		}
	}
	sort.Strings(out)
	return out
}

// add names root g, found in snapshot s, with why. Reasons with the same
// text merge their paths.
func (at *attribution) add(s *snapshot, g *rootGraph, why Reason) {
	r, ok := at.roots[g.Dir]
	if !ok {
		estate := g.Estate
		removed := false
		if hg, inHead := at.b.roots[g.Dir]; inHead {
			if hg.Estate != "" {
				estate = hg.Estate
			}
		} else {
			removed = true
		}
		r = &Root{Dir: g.Dir, Estate: estate, Removed: removed}
		at.roots[g.Dir] = r
		at.reasons[g.Dir] = map[string]*Reason{}
	}
	if have, ok := at.reasons[g.Dir][why.Text]; ok {
		have.Paths = mergeSorted(have.Paths, why.Paths)
		return
	}
	w := why
	w.Paths = mergeSorted(nil, why.Paths)
	at.reasons[g.Dir][why.Text] = &w
}

func mergeSorted(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(append([]string{}, a...), b...) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (at *attribution) indeterminate(kind, p, text string) {
	key := kind + "\x00" + p
	if _, ok := at.indet[key]; !ok {
		at.indet[key] = Indeterminacy{Kind: kind, Path: p, Text: text}
	}
}

func (at *attribution) unplace(p, why, text string) {
	if _, ok := at.unplaced[p]; !ok {
		at.unplaced[p] = Unplaced{Path: p, Why: why, Text: text}
	}
}

// providers compares required_providers in every reached directory a
// change landed in.
func (at *attribution) providers() {
	dirs := make([]string, 0, len(at.compare))
	for d := range at.compare {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		ra, rb := at.a.providers[d], at.b.providers[d]
		names := map[string]bool{}
		for n := range ra {
			names[n] = true
		}
		for n := range rb {
			names[n] = true
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			va, inA := ra[n]
			vb, inB := rb[n]
			if ra == nil || rb == nil || (inA == inB && va == vb) {
				// A directory read at one revision only is new or gone, and
				// its roots are named by the files themselves.
				continue
			}
			at.indeterminate(IndetProvider, d, fmt.Sprintf("provider %s %s -> %s in %s: a provider version bump can change every root's plan", n, quoteOrNone(va, inA), quoteOrNone(vb, inB), d))
		}
	}
}

func quoteOrNone(v string, ok bool) string {
	if !ok {
		return "(none)"
	}
	if v == "" {
		return `""`
	}
	return fmt.Sprintf("%q", v)
}

// pins names every root whose graph holds a module call outside the
// working tree that differs between the revisions.
func (at *attribution) pins() {
	dirs := make([]string, 0, len(at.b.roots))
	for d := range at.b.roots {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		gb := at.b.roots[d]
		ga, ok := at.a.roots[d]
		if !ok || ga.LoadErr != "" || gb.LoadErr != "" {
			continue
		}
		names := map[string]bool{}
		for n := range ga.Calls {
			names[n] = true
		}
		for n := range gb.Calls {
			names[n] = true
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			ca, cb := ga.Calls[n], gb.Calls[n]
			if ca == cb {
				continue
			}
			// A call that is local on both sides is read from the working
			// tree, and its files name the root.
			remoteA := ca.Source != "" && !ca.Local
			remoteB := cb.Source != "" && !cb.Local
			if !remoteA && !remoteB {
				continue
			}
			from, to := pinLabels(ca, cb)
			at.add(at.b, gb, Reason{Kind: KindPin, Module: n, From: from, To: to, Text: fmt.Sprintf("%s %s %s -> %s", KindPin, n, from, to)})
		}
	}
}

// loadErrors makes the answer indeterminate for every root whose graph
// could not be read at either revision: a change could reach it unseen.
func (at *attribution) loadErrors() {
	for _, s := range []*snapshot{at.a, at.b} {
		for d, g := range s.roots {
			if g.LoadErr != "" {
				at.indeterminate(IndetLoad, d, fmt.Sprintf("%s does not load, so what it reaches is unknown: %s", d, g.LoadErr))
			}
		}
	}
	// Only the head revision's reads decide dependents, so only there does
	// an unreadable one leave the answer open.
	for d, g := range at.b.roots {
		if g.ReadsErr != "" {
			at.indeterminate(IndetReads, d, fmt.Sprintf("%s has a cross-estate read this cannot name, so whether it reads a named root is unknown: %s", d, g.ReadsErr))
		}
	}
}

// dependents names, transitively, every root that reads a named root's
// estate, as the head revision's configuration reads it.
func (at *attribution) dependents() {
	readers := map[string][]*rootGraph{}
	for _, g := range at.b.roots {
		for _, e := range g.Reads {
			readers[e] = append(readers[e], g)
		}
	}
	queue := make([]string, 0, len(at.roots))
	for d := range at.roots {
		queue = append(queue, d)
	}
	sort.Strings(queue)
	done := map[string]bool{}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if done[d] {
			continue
		}
		done[d] = true
		// The estate a root owns at either end: a reader of the name it
		// had before a rename is affected by the rename.
		var estates []string
		for _, s := range []*snapshot{at.a, at.b} {
			if g, ok := s.roots[d]; ok && g.Estate != "" && (len(estates) == 0 || estates[0] != g.Estate) {
				estates = append(estates, g.Estate)
			}
		}
		for _, estate := range estates {
			rs := readers[estate]
			sort.Slice(rs, func(i, j int) bool { return rs[i].Dir < rs[j].Dir })
			for _, g := range rs {
				if g.Dir == d {
					continue
				}
				at.add(at.b, g, Reason{Kind: KindReads, Estate: estate, Text: KindReads + " " + estate})
				if !done[g.Dir] {
					queue = append(queue, g.Dir)
				}
			}
		}
	}
}

var kindOrder = map[string]int{KindPin: 0, KindChanged: 1, KindUses: 2, KindReads: 3}

func (at *attribution) result() *Result {
	res := &Result{Schema: Schema, Outcome: Determinate, Roots: []Root{}, Indeterminate: []Indeterminacy{}, Unplaced: []Unplaced{}}
	total := map[string]bool{}
	for d := range at.a.roots {
		total[d] = true
	}
	for d := range at.b.roots {
		total[d] = true
	}
	res.RootsTotal = len(total)

	for d, r := range at.roots {
		for _, w := range at.reasons[d] {
			r.Reasons = append(r.Reasons, *w)
		}
		sort.Slice(r.Reasons, func(i, j int) bool {
			x, y := r.Reasons[i], r.Reasons[j]
			if kindOrder[x.Kind] != kindOrder[y.Kind] {
				return kindOrder[x.Kind] < kindOrder[y.Kind]
			}
			return x.Text < y.Text
		})
		res.Roots = append(res.Roots, *r)
	}
	sort.Slice(res.Roots, func(i, j int) bool { return res.Roots[i].Dir < res.Roots[j].Dir })

	for _, i := range at.indet {
		res.Indeterminate = append(res.Indeterminate, i)
	}
	sort.Slice(res.Indeterminate, func(i, j int) bool {
		x, y := res.Indeterminate[i], res.Indeterminate[j]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.Path < y.Path
	})
	if len(res.Indeterminate) > 0 {
		res.Outcome = Indeterminate
	}

	for p, u := range at.unplaced {
		// A path placed at its other revision is not unplaced.
		if at.placedElsewhere(p) {
			continue
		}
		res.Unplaced = append(res.Unplaced, u)
	}
	sort.Slice(res.Unplaced, func(i, j int) bool { return res.Unplaced[i].Path < res.Unplaced[j].Path })
	return res
}

// placedElsewhere reports whether p, unplaced at one revision, named a root
// at the other (a module directory that a root reached at one end only).
func (at *attribution) placedElsewhere(p string) bool {
	for _, rs := range at.reasons {
		for _, w := range rs {
			for _, q := range w.Paths {
				if q == p {
					return true
				}
			}
		}
	}
	return false
}
