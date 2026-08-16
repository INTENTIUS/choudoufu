// Command refusal-probe measures the corpus's refusals without regenerating
// it, and diffs two such measurements.
//
// It exists because every agent working the language wall rebuilt this same
// throwaway program, one per branch, costing five to ten minutes each before
// any of the actual work started. Worse than the time: each rebuild made its
// own choices about what to count, so two agents reporting "sites" were not
// always reporting the same number, and reconciling them cost more time
// again. This is that program, written once.
//
// # Why not just run the generator
//
// `just corpus` acquires provider schemas across ~75 plugin subprocesses and
// takes long enough that six agents in one session backgrounded it, lost
// track of it, and stalled. It is also the wrong tool for a with/without
// comparison: it writes live/corpus-refusals.json, so two agents running it
// concurrently in the same tree clobber each other.
//
// This runs the same check.Dir the generator runs, over the same manifest,
// writes wherever you point it, and finishes in about twenty seconds.
//
// # The bound you must carry
//
// Without provider schemas this UNDERCOUNTS refusals and OVERCOUNTS what a
// fix clears. A type absent from the generated admission table is refused as
// unadmitted here, where the provider's own identity schema would have
// settled it - see check.Context.Schemas. More important for measuring a
// fix: clearing a refusal often reveals another refusal underneath it that
// only a schema-backed run can see.
//
// That is not hypothetical. One fix measured 11 sites cleared with this
// instrument and delivered 10046 -> 10046 on the schema-backed regeneration,
// because fourteen sites had merely moved from one refusal ID to another.
// Another measured 60 and delivered exactly 60. The difference was not luck:
// the second agent checked, per entry, that no other refusal count had
// changed.
//
// So: sweep with this, then verify the entries you care about against real
// schemas before reporting a number as anything but an upper bound. Every
// output carries schemas:false to make that hard to forget.
//
// # Use
//
//	refusal-probe -out before.json                 # sweep the whole manifest
//	  ... make your change ...
//	refusal-probe -out after.json
//	refusal-probe -diff before.json,after.json     # what moved
//	refusal-probe -entry .corpus/vpc -v            # one entry, per-site detail
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
)

// run is one sweep: what was measured, and what it is worth.
type run struct {
	// Manifest is the manifest path, and Root the tree it was resolved
	// against, so a diff can refuse to compare two runs of different
	// things.
	Manifest string `json:"manifest"`
	Root     string `json:"root"`

	// Schemas records that provider schemas were absent. It is always
	// false and is written anyway: a consumer that does not see the field
	// should not be able to assume the answer.
	Schemas bool `json:"schemas"`

	// Entries are the per-entry results, sorted by name. Per-entry is the
	// unit that matters for a with/without comparison - an aggregate can
	// hold steady while two entries move in opposite directions.
	Entries []entry `json:"entries"`

	// Totals are the aggregate counts.
	Totals totals `json:"totals"`
}

type totals struct {
	Entries    int `json:"entries"`
	Readable   int `json:"readable"`
	Blocked    int `json:"blocked"`
	Sites      int `json:"sites"`
	Instances  int `json:"instances"`
	Shadowed   int `json:"shadowed"`
	Unresolved int `json:"unresolved_modules"`
}

type entry struct {
	Name     string `json:"name"`
	Origin   string `json:"origin"`
	Readable bool   `json:"readable"`
	Blocked  bool   `json:"blocked"`

	// Sites is this entry's total, and Refusals the per-ID breakdown. A
	// fix that moves sites between IDs without changing the total is the
	// single most common outcome on this codebase, so both are recorded.
	Sites     int            `json:"sites"`
	Refusals  map[string]int `json:"refusals"`
	Instances int            `json:"instances"`
	Shadowed  int            `json:"shadowed"`

	// Unresolved counts module calls check.Load could not read without
	// installing them. Every count above is a floor for an entry with
	// registry module calls, not a measurement - roughly a sixth of the
	// refusal surface was found missing on one such entry.
	Unresolved int `json:"unresolved_modules"`
}

func main() {
	var (
		manifest = flag.String("manifest", "live/corpus-manifest.json", "manifest to resolve")
		root     = flag.String("root", ".", "repository root the manifest resolves against")
		out      = flag.String("out", "", "write the sweep here as JSON (default: summary to stdout)")
		diff     = flag.String("diff", "", "compare two sweeps: before.json,after.json")
		one      = flag.String("entry", "", "measure only entries whose name contains this")
		verbose  = flag.Bool("v", false, "with -entry, print every refused site")
	)
	flag.Parse()

	if *diff != "" {
		if err := runDiff(*diff); err != nil {
			fmt.Fprintln(os.Stderr, "refusal-probe:", err)
			os.Exit(1)
		}
		return
	}

	r, err := sweep(*manifest, *root, *one, *verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "refusal-probe:", err)
		os.Exit(1)
	}

	if *out != "" {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "refusal-probe:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "refusal-probe:", err)
			os.Exit(1)
		}
	}
	summarize(r, *out)
}

func sweep(manifestPath, root, only string, verbose bool) (*run, error) {
	m, err := check.ReadManifest(filepath.Join(root, manifestPath))
	if err != nil {
		return nil, err
	}
	refs, err := m.Resolve(root)
	if err != nil {
		return nil, err
	}

	r := &run{Manifest: manifestPath, Root: root, Schemas: false}
	ctx := context.Background()

	for _, ref := range refs {
		if only != "" && !strings.Contains(ref.Name, only) {
			continue
		}

		varFiles := make([]string, 0, len(ref.VarFiles))
		for _, v := range ref.VarFiles {
			varFiles = append(varFiles, filepath.Join(root, v))
		}

		// No Schemas: that is the whole bound this tool carries.
		rep := check.Dir(ctx, ref.Dir, check.Context{}, varFiles...)

		e := entry{
			Name:       ref.Name,
			Origin:     ref.Origin,
			Readable:   rep.Readable(),
			Blocked:    rep.Blocked(),
			Sites:      rep.Sites(),
			Instances:  rep.Instances,
			Shadowed:   rep.Shadowed,
			Unresolved: len(rep.Load.UnresolvedModules),
			Refusals:   map[string]int{},
		}
		for _, f := range rep.Findings {
			e.Refusals[f.ID] += len(f.Sites)
			if verbose && only != "" {
				for _, s := range f.Sites {
					fmt.Printf("%s\t%s:%d\t%s\n", ref.Name, s.File, s.Line, f.ID)
				}
			}
		}
		r.Entries = append(r.Entries, e)

		r.Totals.Entries++
		if e.Readable {
			r.Totals.Readable++
		}
		if e.Blocked {
			r.Totals.Blocked++
		}
		r.Totals.Sites += e.Sites
		r.Totals.Instances += e.Instances
		r.Totals.Shadowed += e.Shadowed
		r.Totals.Unresolved += e.Unresolved
	}

	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].Name < r.Entries[j].Name })
	return r, nil
}

func summarize(r *run, wrote string) {
	byID := map[string]int{}
	cfgs := map[string]int{}
	for _, e := range r.Entries {
		for id, n := range e.Refusals {
			byID[id] += n
			cfgs[id]++
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return byID[ids[i]] > byID[ids[j]] })

	fmt.Printf("entries %d  readable %d  blocked %d  sites %d  instances %d  shadowed %d\n",
		r.Totals.Entries, r.Totals.Readable, r.Totals.Blocked,
		r.Totals.Sites, r.Totals.Instances, r.Totals.Shadowed)
	fmt.Printf("unresolved module calls %d - every count above is a floor for the entries carrying them\n",
		r.Totals.Unresolved)
	fmt.Println("schemas false - undercounts refusals, overcounts what a fix clears; verify per-entry before reporting")
	fmt.Println()
	for _, id := range ids {
		fmt.Printf("%5d sites %4d cfg  %s\n", byID[id], cfgs[id], id)
	}
	if wrote != "" {
		fmt.Printf("\nwrote %s\n", wrote)
	}
}

func runDiff(spec string) error {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		return fmt.Errorf("-diff wants before.json,after.json")
	}
	before, err := load(parts[0])
	if err != nil {
		return err
	}
	after, err := load(parts[1])
	if err != nil {
		return err
	}
	if before.Manifest != after.Manifest {
		return fmt.Errorf("these measure different manifests (%q vs %q); the comparison would be meaningless",
			before.Manifest, after.Manifest)
	}

	fmt.Printf("sites      %d -> %d  (%+d)\n", before.Totals.Sites, after.Totals.Sites,
		after.Totals.Sites-before.Totals.Sites)
	fmt.Printf("instances  %d -> %d  (%+d)\n", before.Totals.Instances, after.Totals.Instances,
		after.Totals.Instances-before.Totals.Instances)
	fmt.Printf("blocked    %d -> %d  (%+d)\n", before.Totals.Blocked, after.Totals.Blocked,
		after.Totals.Blocked-before.Totals.Blocked)
	fmt.Println()

	// Per-ID, because sites moving between IDs at a constant total is this
	// codebase's most common fix outcome and an aggregate hides it.
	bID, aID := byID(before), byID(after)
	ids := union(bID, aID)
	fmt.Println("by refusal ID:")
	var moved bool
	for _, id := range ids {
		if bID[id] == aID[id] {
			continue
		}
		moved = true
		fmt.Printf("  %+6d  %5d -> %-5d  %s\n", aID[id]-bID[id], bID[id], aID[id], id)
	}
	if !moved {
		fmt.Println("  (nothing)")
	}

	// Per-entry regressions get their own section. A fix that clears sites
	// overall while making one entry worse is the shape that has cost this
	// project real instances, and it must not be reported as a win.
	bE, aE := byEntry(before), byEntry(after)
	var worse, better []string
	for name, a := range aE {
		b, ok := bE[name]
		if !ok {
			continue
		}
		switch {
		case a.Sites > b.Sites || a.Instances < b.Instances:
			worse = append(worse, fmt.Sprintf("  %s  sites %d -> %d, instances %d -> %d",
				name, b.Sites, a.Sites, b.Instances, a.Instances))
		case a.Sites < b.Sites || a.Instances > b.Instances:
			better = append(better, fmt.Sprintf("  %s  sites %d -> %d, instances %d -> %d",
				name, b.Sites, a.Sites, b.Instances, a.Instances))
		}
	}
	sort.Strings(worse)
	sort.Strings(better)

	fmt.Printf("\nentries improved: %d\n", len(better))
	for _, s := range better {
		fmt.Println(s)
	}
	fmt.Printf("\nentries WORSE: %d\n", len(worse))
	for _, s := range worse {
		fmt.Println(s)
	}
	if len(worse) > 0 {
		fmt.Println("\nan entry losing instances is a resolution the change took away; do not report the")
		fmt.Println("aggregate without these beside it")
	}
	fmt.Println("\nschemas false in both runs - upper bound, verify per-entry with real schemas")
	return nil
}

func load(path string) (*run, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

func byID(r *run) map[string]int {
	m := map[string]int{}
	for _, e := range r.Entries {
		for id, n := range e.Refusals {
			m[id] += n
		}
	}
	return m
}

func byEntry(r *run) map[string]entry {
	m := make(map[string]entry, len(r.Entries))
	for _, e := range r.Entries {
		m[e.Name] = e
	}
	return m
}

func union(a, b map[string]int) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di := b[out[i]] - a[out[i]]
		dj := b[out[j]] - a[out[j]]
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}
