// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

type options struct {
	repoDir string
	ref     string
	since   string
	minLen  int
	verbose bool
}

type finding struct {
	Merge   string   `json:"merge"`
	Subject string   `json:"subject"`
	Parent  string   `json:"parent"`
	File    string   `json:"file"`
	Lines   []string `json:"lines"`
	// Repaired lists the subset of Lines present again at the scanned ref:
	// lost at this merge, restored by later work.
	Repaired []string `json:"repaired,omitempty"`
}

type stats struct {
	MergesScanned    int `json:"merges_scanned"`
	MergesSkipped    int `json:"merges_skipped_no_base"`
	CandidateLines   int `json:"candidate_lines"`
	AfterMoveFilters int `json:"after_move_filters"`
	SupersededDrop   int `json:"superseded_dropped"`
	InformedDropped  int `json:"informed_dropped"`
	DedupedLines     int `json:"deduped_lines"`
	LostLines        int `json:"lost_lines"`
}

type result struct {
	Findings []finding `json:"findings"`
	Stats    stats     `json:"stats"`
}

type mergeInfo struct {
	sha     string
	subject string
	parents []string
}

func runScan(opts options) (*result, error) {
	r, err := openRepo(opts.repoDir, opts.minLen)
	if err != nil {
		return nil, err
	}
	defer r.close()

	merges, err := listMerges(r, opts)
	if err != nil {
		return nil, err
	}

	res := &result{Findings: []finding{}}
	// Oldest merge first, so a loss is reported once, at the merge that
	// first dropped it; later merges re-dropping still-lost content are
	// echoes of the same event.
	sc := &scanState{reported: map[string]bool{}}
	for i, m := range merges {
		if opts.verbose {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s %s\n", i+1, len(merges), short(m.sha), m.subject)
		}
		if err := scanMerge(r, m, sc, res); err != nil {
			return nil, fmt.Errorf("merge %s: %w", m.sha, err)
		}
	}
	if err := annotateRepaired(r, opts.ref, res); err != nil {
		return nil, err
	}
	sort.Slice(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Merge != b.Merge {
			return a.Merge < b.Merge
		}
		return a.File < b.File
	})
	return res, nil
}

// scanState carries cross-merge memory: every normalized line already
// reported as lost, so cascades (the same still-lost content re-dropped by
// each later merge along a branch) surface once, at the first drop.
type scanState struct {
	reported map[string]bool
}

func listMerges(r *repo, opts options) ([]mergeInfo, error) {
	args := []string{"log", "--merges", "--topo-order", "--format=%H%x00%P%x00%s%x01"}
	if opts.since != "" {
		args = append(args, opts.since+".."+opts.ref)
	} else {
		args = append(args, opts.ref)
	}
	out, err := r.git(args...)
	if err != nil {
		return nil, err
	}
	var merges []mergeInfo
	for _, rec := range strings.Split(out, "\x01") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x00", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected log record %q", rec)
		}
		merges = append(merges, mergeInfo{
			sha:     parts[0],
			subject: parts[2],
			parents: strings.Fields(parts[1]),
		})
	}
	// --topo-order lists descendants first; reverse for ancestors-first.
	for i, j := 0, len(merges)-1; i < j; i, j = i+1, j-1 {
		merges[i], merges[j] = merges[j], merges[i]
	}
	return merges, nil
}

func mergeBases(r *repo, m mergeInfo) ([]string, error) {
	var out string
	var err error
	if len(m.parents) == 2 {
		out, err = r.git("merge-base", "--all", m.parents[0], m.parents[1])
	} else {
		args := append([]string{"merge-base", "--octopus"}, m.parents...)
		out, err = r.git(args...)
	}
	if err != nil {
		// No common ancestor exits non-zero; treat as "no base".
		return nil, nil
	}
	return strings.Fields(out), nil
}

func scanMerge(r *repo, m mergeInfo, sc *scanState, res *result) error {
	bases, err := mergeBases(r, m)
	if err != nil {
		return err
	}
	if len(bases) == 0 {
		res.Stats.MergesSkipped++
		return nil
	}
	res.Stats.MergesScanned++

	for pi, p := range m.parents {
		others := make([]string, 0, len(m.parents)-1)
		for oi, o := range m.parents {
			if oi != pi {
				others = append(others, o)
			}
		}
		if err := scanParent(r, m, p, others, bases, sc, res); err != nil {
			return err
		}
	}
	return nil
}

// scanParent applies the rule for one contributing parent p of merge m:
// lines in p, absent from every base, absent from m.
func scanParent(r *repo, m mergeInfo, p string, others, bases []string, sc *scanState, res *result) error {
	entries, err := r.diffNameStatus(bases[0], p)
	if err != nil {
		return err
	}
	genDirs, err := r.generatedDirs(p)
	if err != nil {
		return err
	}

	// Contributed-and-then-lost candidates per path in p.
	perFile := map[string]*fileCand{}

	// Rename map p -> m, and m-side changed paths for the moved-content pass.
	pmEntries, err := r.diffNameStatus(p, m.sha)
	if err != nil {
		return err
	}
	pmTo := map[string]string{}
	var mChanged []string
	for _, e := range pmEntries {
		if e.from != "" {
			pmTo[e.from] = e.to
		}
		if e.to != "" {
			mChanged = append(mChanged, e.to)
		}
	}

	for _, e := range entries {
		if e.status == 'D' || e.to == "" {
			continue
		}
		if skipPath(e.to, genDirs) {
			continue
		}
		// A file identical between p and m lost nothing.
		if _, changed := pmTo[e.to]; !changed {
			continue
		}
		pSet, err := r.linesOf(p + ":" + e.to)
		if err != nil {
			return err
		}
		if pSet == nil { // generated or binary
			continue
		}
		cand := map[string]string{}
		for n, orig := range pSet {
			cand[n] = orig
		}
		basePath := e.from
		if basePath == "" {
			basePath = e.to
		}
		for _, b := range bases {
			bSet, err := r.linesOf(b + ":" + basePath)
			if err != nil {
				return err
			}
			for n := range bSet {
				delete(cand, n)
			}
			if len(cand) == 0 {
				break
			}
		}
		if len(cand) == 0 {
			continue
		}
		// Same-file (rename-followed) subtraction against m.
		if mPath := pmTo[e.to]; mPath != "" {
			mSet, err := r.linesOf(m.sha + ":" + mPath)
			if err != nil {
				return err
			}
			for n := range mSet {
				delete(cand, n)
			}
		}
		if len(cand) > 0 {
			perFile[e.to] = &fileCand{basePath: basePath, mPath: pmTo[e.to], cand: cand}
		}
	}

	count := func() int {
		n := 0
		for _, fc := range perFile {
			n += len(fc.cand)
		}
		return n
	}
	res.Stats.CandidateLines += count()
	if len(perFile) == 0 {
		return nil
	}

	// Moved content, pass 1: a candidate present in any file the merge
	// changed relative to p survived the merge somewhere.
	for _, mp := range mChanged {
		mSet, err := r.linesOf(m.sha + ":" + mp)
		if err != nil {
			return err
		}
		if len(mSet) == 0 {
			continue
		}
		for f, fc := range perFile {
			for n := range fc.cand {
				if _, ok := mSet[n]; ok {
					delete(fc.cand, n)
				}
			}
			if len(fc.cand) == 0 {
				delete(perFile, f)
			}
		}
		if len(perFile) == 0 {
			return nil
		}
	}

	prune := func() {
		for f, fc := range perFile {
			if len(fc.cand) == 0 {
				delete(perFile, f)
			}
		}
	}
	prune()

	// Moved content, pass 2: reflow tolerance. A candidate whose token run
	// appears contiguously in the merged file (or any file the merge
	// changed) survived with only its wrap points moved.
	for f, fc := range perFile {
		if mPath := pmTo[f]; mPath != "" {
			if err := dropReflowed(r, m.sha+":"+mPath, fc); err != nil {
				return err
			}
		}
	}
	prune()
	for _, mp := range mChanged {
		if len(perFile) == 0 {
			break
		}
		for _, fc := range perFile {
			if err := dropReflowed(r, m.sha+":"+mp, fc); err != nil {
				return err
			}
		}
		prune()
	}

	// Moved content, pass 3: whitespace-tolerant grep over m's whole tree,
	// catching moves into files m did not change relative to p.
	if err := dropSurvivors(r, m.sha, perFile); err != nil {
		return err
	}
	prune()
	res.Stats.AfterMoveFilters += count()
	if len(perFile) == 0 {
		return nil
	}

	// Superseded variants: a candidate whose merged file holds a same-shaped
	// sibling line (same leading token, most tokens shared) was edited in
	// place by the merge - both sides' versions were on the table and a
	// successor won. That is a resolution, not a silent drop.
	for _, fc := range perFile {
		if fc.mPath == "" {
			continue
		}
		mSet, err := r.linesOf(m.sha + ":" + fc.mPath)
		if err != nil {
			return err
		}
		for n := range fc.cand {
			if hasVariant(n, mSet) {
				delete(fc.cand, n)
				res.Stats.SupersededDrop++
			}
		}
	}
	prune()
	if len(perFile) == 0 {
		return nil
	}

	// Informed deletions: if the other side's own history carries the line
	// but its tip does not, that side saw the content and dropped it.
	for f, fc := range perFile {
		for _, o := range others {
			hist, tip, err := otherSideLines(r, o, bases, uniqueStrings(f, fc.basePath, pmTo[f]))
			if err != nil {
				return err
			}
			for n := range fc.cand {
				_, seen := hist[n]
				_, kept := tip[n]
				if seen && !kept {
					delete(fc.cand, n)
					res.Stats.InformedDropped++
				}
			}
		}
	}
	prune()

	for f, fc := range perFile {
		lines := make([]string, 0, len(fc.cand))
		for n, orig := range fc.cand {
			if sc.reported[n] {
				res.Stats.DedupedLines++
				continue
			}
			sc.reported[n] = true
			lines = append(lines, strings.TrimRight(orig, "\r"))
		}
		if len(lines) == 0 {
			continue
		}
		sort.Strings(lines)
		res.Stats.LostLines += len(lines)
		res.Findings = append(res.Findings, finding{
			Merge:   m.sha,
			Subject: m.subject,
			Parent:  p,
			File:    f,
			Lines:   lines,
		})
	}
	return nil
}

// annotateRepaired marks, per finding, which lost lines are present again
// at the scanned ref: real losses at the time, since restored by later
// work. Presence is judged with the same whitespace-tolerant match and a
// reflow-tolerant token-run check against the file the loss came from.
func annotateRepaired(r *repo, ref string, res *result) error {
	all := map[string]bool{}
	for _, f := range res.Findings {
		for _, l := range f.Lines {
			if n, ok := normLine(l, 1); ok {
				all[n] = true
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	found, err := grepSurvivors(r, ref, all)
	if err != nil {
		return err
	}
	for i := range res.Findings {
		f := &res.Findings[i]
		var toks []string
		for _, l := range f.Lines {
			n, ok := normLine(l, 1)
			if !ok {
				continue
			}
			if !found[n] {
				if toks == nil {
					toks, err = r.tokensOf(ref + ":" + f.File)
					if err != nil {
						return err
					}
				}
				if !containsTokenRun(toks, strings.Split(n, " ")) {
					continue
				}
			}
			f.Repaired = append(f.Repaired, l)
		}
	}
	return nil
}

// skipPath filters machine-managed paths: JSON artifacts, checksum files,
// and anything inside a directory that carries a GENERATED.md.
func skipPath(p string, genDirs []string) bool {
	switch {
	case strings.HasSuffix(p, ".json"), strings.HasSuffix(p, ".sum"):
		return true
	case path.Base(p) == "GENERATED.md":
		return true
	}
	for _, d := range genDirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

// dropSurvivors removes candidates that a whitespace-tolerant fixed-token
// grep finds anywhere in tree, i.e. content that moved to a file the
// cheaper pass did not look at.
func dropSurvivors(r *repo, tree string, perFile map[string]*fileCand) error {
	all := map[string]bool{}
	for _, fc := range perFile {
		for n := range fc.cand {
			all[n] = true
		}
	}
	found, err := grepSurvivors(r, tree, all)
	if err != nil {
		return err
	}
	for _, fc := range perFile {
		for n := range fc.cand {
			if found[n] {
				delete(fc.cand, n)
			}
		}
	}
	return nil
}

// grepSurvivors reports which of the normalized candidate lines a
// whitespace-tolerant grep finds anywhere in the given tree.
func grepSurvivors(r *repo, tree string, cands map[string]bool) (map[string]bool, error) {
	if len(cands) == 0 {
		return map[string]bool{}, nil
	}
	patterns := make([]string, 0, len(cands))
	for n := range cands {
		patterns = append(patterns, wsPattern(n))
	}
	tmp, err := os.CreateTemp("", "merge-guard-grep-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strings.Join(patterns, "\n") + "\n"); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()

	out, _ := r.gitOK("grep", "-E", "-I", "-h", "--no-color", "-f", tmp.Name(), tree)
	hits := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if n, ok := normLine(line, 1); ok {
			hits[n] = true
		}
	}
	found := map[string]bool{}
	for n := range cands {
		for hit := range hits {
			if strings.Contains(hit, n) {
				found[n] = true
				break
			}
		}
	}
	return found, nil
}

// fileCand is one candidate file: its path at the merge base, its path in
// the merge ("" when the merge deleted it), and its still-unaccounted-for
// contributed lines (normalized -> original).
type fileCand struct {
	basePath string
	mPath    string
	cand     map[string]string
}

// dropReflowed removes candidates whose token sequence appears contiguously
// in the blob at spec.
func dropReflowed(r *repo, spec string, fc *fileCand) error {
	if len(fc.cand) == 0 {
		return nil
	}
	hay, err := r.tokensOf(spec)
	if err != nil {
		return err
	}
	if len(hay) == 0 {
		return nil
	}
	for n := range fc.cand {
		if containsTokenRun(hay, strings.Split(n, " ")) {
			delete(fc.cand, n)
		}
	}
	return nil
}

func containsTokenRun(hay, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(hay) {
		return false
	}
	first := needle[0]
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i] != first {
			continue
		}
		ok := true
		for j := 1; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// hasVariant reports whether the merged file's line set holds an edited
// sibling of candidate n: same leading token and at least half of n's
// tokens present in some single surviving line.
func hasVariant(n string, mSet map[string]string) bool {
	toks := strings.Split(n, " ")
	for mn := range mSet {
		mtoks := strings.Split(mn, " ")
		if mtoks[0] != toks[0] {
			continue
		}
		mHas := map[string]bool{}
		for _, t := range mtoks {
			mHas[t] = true
		}
		shared := 0
		for _, t := range toks {
			if mHas[t] {
				shared++
			}
		}
		if shared*2 >= len(toks) {
			return true
		}
	}
	return false
}

// wsPattern turns a normalized line into an ERE matching it with any
// whitespace runs.
func wsPattern(n string) string {
	toks := strings.Split(n, " ")
	for i, t := range toks {
		toks[i] = regexp.QuoteMeta(t)
	}
	return strings.Join(toks, "[ \t]+")
}

// otherSideLines gathers, for the given paths, the union of line sets the
// other parent's unique history ever carried (hist) and what its tip
// carries (tip).
func otherSideLines(r *repo, other string, bases []string, paths []string) (hist, tip map[string]string, err error) {
	hist = map[string]string{}
	tip = map[string]string{}
	args := []string{"log", "--format=%H", "--raw", "--no-renames", other, "--not"}
	args = append(args, bases...)
	args = append(args, "--")
	args = append(args, paths...)
	out, err := r.git(args...)
	if err != nil {
		return nil, nil, err
	}
	pathSet := map[string]bool{}
	for _, p := range paths {
		pathSet[p] = true
	}
	blobSeen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, ":") {
			continue
		}
		meta, p, ok := strings.Cut(line, "\t")
		if !ok || !pathSet[p] {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 5 {
			continue
		}
		newSha := f[3]
		if strings.Trim(newSha, "0") == "" || blobSeen[newSha] {
			continue
		}
		blobSeen[newSha] = true
		set, err := r.linesOf(newSha)
		if err != nil {
			return nil, nil, err
		}
		for n, orig := range set {
			hist[n] = orig
		}
	}
	for _, p := range paths {
		set, err := r.linesOf(other + ":" + p)
		if err != nil {
			return nil, nil, err
		}
		for n, orig := range set {
			tip[n] = orig
		}
	}
	return hist, tip, nil
}

func uniqueStrings(ss ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
