// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"fmt"
	"sort"
	"strings"
)

// Root is one root of a set, as wave planning sees it.
type Root struct {
	// Root is the root's directory as the set names it.
	Root string `json:"root"`
	// Estate is the estate the root owns.
	Estate string `json:"estate"`
	// Reads are the estates the root's configuration reads.
	Reads []Read `json:"reads,omitempty"`
}

// Read is one estate a root's configuration reads, and where.
type Read struct {
	// Estate is the producer estate.
	Estate string `json:"estate"`
	// From is the data source that reads it, module-qualified when it is
	// declared inside one.
	From string `json:"from"`
}

// Edge is one ordering constraint between two roots of the set: Reader
// lands after Producer.
type Edge struct {
	Reader   string `json:"reader"`
	Producer string `json:"producer"`
	Estate   string `json:"estate"`
	From     string `json:"from"`
}

// Wave is one step of a rollout.
type Wave struct {
	// Number counts from 1.
	Number int `json:"wave"`
	// Canary is true for the wave the explicit canaries form.
	Canary bool `json:"canary"`
	// Roots are the wave's roots, sorted.
	Roots []string `json:"roots"`
	// Edges are ordering constraints between two roots of this same wave.
	// Only the canary wave can have any.
	Edges []Edge `json:"edges,omitempty"`
	// Digest is the set digest over this wave's roots, when the roots'
	// plans were given.
	Digest string `json:"digest,omitempty"`
}

// External is a read of an estate no root of the set owns. It orders
// nothing.
type External struct {
	Root   string `json:"root"`
	Estate string `json:"estate"`
	From   string `json:"from"`
}

// Waves is a set split into waves.
type Waves struct {
	Waves    []Wave     `json:"waves"`
	Edges    []Edge     `json:"edges"`
	External []External `json:"external_reads"`
	// WaveOf maps each root to its wave number.
	WaveOf map[string]int `json:"-"`
}

// Split orders roots into waves. canaries name roots by directory or by
// estate; when there are any they form wave 1. See the package
// documentation for the rules and what is refused.
func Split(roots []Root, canaries []string) (*Waves, error) {
	byRoot := make(map[string]Root, len(roots))
	byEstate := make(map[string]string, len(roots))
	for _, r := range roots {
		if r.Root == "" {
			return nil, fmt.Errorf("a root with no directory")
		}
		if _, dup := byRoot[r.Root]; dup {
			return nil, fmt.Errorf("the set names root %s twice", r.Root)
		}
		if r.Estate == "" {
			return nil, fmt.Errorf("root %s has no estate", r.Root)
		}
		if other, dup := byEstate[r.Estate]; dup {
			return nil, fmt.Errorf("roots %s and %s both own estate %q, so a reader of it cannot be ordered after one owner; one estate per root", other, r.Root, r.Estate)
		}
		byRoot[r.Root] = r
		byEstate[r.Estate] = r.Root
	}

	names := make([]string, 0, len(roots))
	for _, r := range roots {
		names = append(names, r.Root)
	}
	sort.Strings(names)

	// The edges, deduplicated per (reader, producer) on the first read in
	// sorted order, and the reads that leave the set.
	out := &Waves{Edges: []Edge{}, External: []External{}, WaveOf: map[string]int{}}
	producers := make(map[string][]string) // reader -> producers
	readers := make(map[string][]string)   // producer -> readers
	edgeFor := make(map[[2]string]Edge)
	for _, name := range names {
		reads := append([]Read(nil), byRoot[name].Reads...)
		sort.Slice(reads, func(i, j int) bool {
			if reads[i].Estate != reads[j].Estate {
				return reads[i].Estate < reads[j].Estate
			}
			return reads[i].From < reads[j].From
		})
		for _, rd := range reads {
			producer, inSet := byEstate[rd.Estate]
			switch {
			case !inSet:
				out.External = append(out.External, External{Root: name, Estate: rd.Estate, From: rd.From})
			case producer == name:
				// A root reading its own estate orders nothing.
			default:
				key := [2]string{name, producer}
				if _, seen := edgeFor[key]; seen {
					continue
				}
				e := Edge{Reader: name, Producer: producer, Estate: rd.Estate, From: rd.From}
				edgeFor[key] = e
				out.Edges = append(out.Edges, e)
				producers[name] = append(producers[name], producer)
				readers[producer] = append(readers[producer], name)
			}
		}
	}

	// The canaries.
	canary := make(map[string]bool)
	for _, c := range canaries {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		switch {
		case byRoot[c].Root != "":
			canary[c] = true
		case byEstate[c] != "":
			canary[byEstate[c]] = true
		default:
			return nil, fmt.Errorf("canary %q names no root of the set, by directory or by estate", c)
		}
	}
	canaryNames := sortedKeys(canary)
	for _, c := range canaryNames {
		for _, p := range producers[c] {
			if !canary[p] {
				e := edgeFor[[2]string{c, p}]
				return nil, fmt.Errorf("canary %s reads estate %q (%s), which root %s owns and which is not a canary: wave 1 would land the reader before what it reads. Add %s to the canaries, or choose a canary that reads nothing else in the set", c, e.Estate, e.From, p, p)
			}
		}
	}

	if cycle := findCycle(names, producers); cycle != nil {
		var steps []string
		for i := 0; i+1 < len(cycle); i++ {
			e := edgeFor[[2]string{cycle[i], cycle[i+1]}]
			steps = append(steps, fmt.Sprintf("%s reads estate %q (%s)", e.Reader, e.Estate, e.From))
		}
		return nil, fmt.Errorf("the roots read each other in a cycle, so no order lands every reader after what it reads: %s", strings.Join(steps, "; "))
	}

	// Wave numbers: canaries are 1; every other root is one after the
	// latest wave of anything it reads, and after the canary wave when
	// there is one.
	base := 0
	if len(canaryNames) > 0 {
		base = 1
		for _, c := range canaryNames {
			out.WaveOf[c] = 1
		}
	}
	var assign func(string) int
	assign = func(n string) int {
		if w, ok := out.WaveOf[n]; ok {
			return w
		}
		w := base + 1
		for _, p := range producers[n] {
			if pw := assign(p) + 1; pw > w {
				w = pw
			}
		}
		out.WaveOf[n] = w
		return w
	}
	maxWave := base
	for _, n := range names {
		if w := assign(n); w > maxWave {
			maxWave = w
		}
	}

	for w := 1; w <= maxWave; w++ {
		wave := Wave{Number: w, Canary: w == 1 && base == 1, Roots: []string{}}
		for _, n := range names {
			if out.WaveOf[n] == w {
				wave.Roots = append(wave.Roots, n)
			}
		}
		for _, e := range out.Edges {
			if out.WaveOf[e.Reader] == w && out.WaveOf[e.Producer] == w {
				wave.Edges = append(wave.Edges, e)
			}
		}
		if len(wave.Roots) == 0 {
			// Only possible below the first non-canary wave when every
			// root is a canary; there is nothing to number.
			continue
		}
		out.Waves = append(out.Waves, wave)
	}
	for i := range out.Waves {
		out.Waves[i].Number = i + 1
		for _, n := range out.Waves[i].Roots {
			out.WaveOf[n] = i + 1
		}
	}
	return out, nil
}

// findCycle returns one cycle in the reads graph as a path that starts and
// ends at the same root, or nil. It walks roots and producers in sorted
// order, so the cycle it names is the same on every run.
func findCycle(names []string, producers map[string][]string) []string {
	const (
		unseen = iota
		onPath
		done
	)
	state := make(map[string]int, len(names))
	var path []string
	var found []string
	var visit func(string) bool
	visit = func(n string) bool {
		state[n] = onPath
		path = append(path, n)
		ps := append([]string(nil), producers[n]...)
		sort.Strings(ps)
		for _, p := range ps {
			switch state[p] {
			case onPath:
				for i, m := range path {
					if m == p {
						found = append(append([]string(nil), path[i:]...), p)
						return true
					}
				}
			case unseen:
				if visit(p) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		state[n] = done
		return false
	}
	for _, n := range names {
		if state[n] == unseen && visit(n) {
			return found
		}
	}
	return nil
}

// AttachDigests sets every wave's digest from per-root digests. Every root
// of every wave must have one.
func (w *Waves) AttachDigests(byRoot map[string]string) error {
	for i := range w.Waves {
		entries := make([]RootDigestEntry, 0, len(w.Waves[i].Roots))
		for _, r := range w.Waves[i].Roots {
			d, ok := byRoot[r]
			if !ok {
				return fmt.Errorf("root %s is in wave %d but has no plan in the set plan document", r, w.Waves[i].Number)
			}
			entries = append(entries, RootDigestEntry{Root: r, Digest: d})
		}
		d, err := SetDigest(entries)
		if err != nil {
			return err
		}
		w.Waves[i].Digest = d
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
