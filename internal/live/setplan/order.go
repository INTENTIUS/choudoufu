// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setplan

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// GitHub issue #2051: when a set holds an upstream root and a downstream
// root that reads its outputs through terraform_estate_outputs, the
// downstream is planned after the upstream, on the upstream's planned
// outputs (plan -estate-outputs-from), so the plan a reviewer approves is
// the one that applies once the upstream lands. Only terraform_estate_outputs
// reads order the set here: a tag-filtered data source reads the live
// object, which no plan changes.
//
// The order needs every root's estate and reads, which are known only after
// each root's init (a module's reads are read from where init installed
// it). So every root runs its estate and init stages first, publishes what
// it owns and reads, and waits for the whole set to publish; then each
// waits for its upstreams to finish before planning. Waiting never holds a
// slot of the parallel bound, so the bound cannot deadlock the set.
//
// An upstream that did not plan leaves its readers unplanned with an error
// naming it: they are never planned on the outputs it last applied instead,
// which is the gap this closes. Reads that form a cycle within the set have
// no order, and every root on the cycle fails at the plan stage naming it.

type coordinator struct {
	base    string
	paths   []rootPaths
	readsOf func(ctx context.Context, dir string) ([]string, error)
	sem     chan struct{}

	mu        sync.Mutex
	published []bool
	estates   []string
	reads     [][]string
	results   []Root
	prepared  sync.WaitGroup
	done      []chan struct{}

	once      sync.Once
	upstreams [][]int
	orderErr  []error
	upstream  []bool
}

func newCoordinator(base string, paths []rootPaths, parallel int, readsOf func(ctx context.Context, dir string) ([]string, error)) *coordinator {
	c := &coordinator{
		base:      base,
		paths:     paths,
		readsOf:   readsOf,
		sem:       make(chan struct{}, parallel),
		published: make([]bool, len(paths)),
		estates:   make([]string, len(paths)),
		reads:     make([][]string, len(paths)),
		results:   make([]Root, len(paths)),
		done:      make([]chan struct{}, len(paths)),
	}
	for i := range paths {
		c.done[i] = make(chan struct{})
	}
	c.prepared.Add(len(paths))
	return c
}

func (c *coordinator) acquire() { c.sem <- struct{}{} }
func (c *coordinator) release() { <-c.sem }

func (c *coordinator) readsOfDir(ctx context.Context, dir string) ([]string, error) {
	if c.readsOf == nil {
		return nil, nil
	}
	return c.readsOf(ctx, dir)
}

// publish records what root i owns and reads. Only the first call counts.
func (c *coordinator) publish(i int, estate string, reads []string) {
	c.mu.Lock()
	if c.published[i] {
		c.mu.Unlock()
		return
	}
	c.published[i] = true
	c.estates[i] = estate
	c.reads[i] = reads
	c.mu.Unlock()
	c.prepared.Done()
}

// finish publishes a root that failed before it could, and releases every
// root waiting on it.
func (c *coordinator) finish(i int, r *Root) {
	c.publish(i, "", nil)
	c.mu.Lock()
	c.results[i] = *r
	c.mu.Unlock()
	close(c.done[i])
}

// order works out every root's upstreams once the whole set has published.
func (c *coordinator) order() {
	c.prepared.Wait()
	c.once.Do(func() {
		n := len(c.paths)
		c.upstreams = make([][]int, n)
		c.orderErr = make([]error, n)
		c.upstream = make([]bool, n)
		owners := map[string][]int{}
		for i, e := range c.estates {
			if e != "" {
				owners[e] = append(owners[e], i)
			}
		}
		for i, reads := range c.reads {
			for _, e := range reads {
				own := owners[e]
				switch {
				case len(own) == 0 || e == c.estates[i]:
					// Outside the set: read from the record store as always.
				case len(own) > 1:
					var roots []string
					for _, j := range own {
						roots = append(roots, c.paths[j].rel)
					}
					c.orderErr[i] = fmt.Errorf("this root reads estate %q's outputs, and %d roots of the set own that estate (%s), so which one's plan to read cannot be told", e, len(own), strings.Join(roots, ", "))
				default:
					c.upstreams[i] = append(c.upstreams[i], own[0])
					c.upstream[own[0]] = true
				}
			}
		}
		// Kahn's algorithm: whatever cannot be ordered is on a cycle or
		// behind one.
		indeg := make([]int, n)
		readers := make([][]int, n)
		for i, ups := range c.upstreams {
			indeg[i] = len(ups)
			for _, j := range ups {
				readers[j] = append(readers[j], i)
			}
		}
		var queue []int
		for i := range indeg {
			if indeg[i] == 0 {
				queue = append(queue, i)
			}
		}
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			for _, i := range readers[j] {
				indeg[i]--
				if indeg[i] == 0 {
					queue = append(queue, i)
				}
			}
		}
		var stuck []string
		for i := range indeg {
			if indeg[i] > 0 {
				stuck = append(stuck, c.paths[i].rel)
			}
		}
		sort.Strings(stuck)
		for i := range indeg {
			if indeg[i] > 0 {
				c.orderErr[i] = fmt.Errorf("this root's terraform_estate_outputs reads form a cycle within the set (%s): a reader is planned after what it reads, and a cycle has no order", strings.Join(stuck, ", "))
			}
		}
	})
}

// isUpstream reports whether another root of the set reads root i.
func (c *coordinator) isUpstream(i int) bool {
	c.order()
	return c.upstream[i]
}

// waitUpstreams waits for root i's upstreams and returns them, or an error
// when one did not plan or the set cannot be ordered.
func (c *coordinator) waitUpstreams(i int) ([]PlannedFromRoot, error) {
	c.order()
	if err := c.orderErr[i]; err != nil {
		return nil, err
	}
	var from []PlannedFromRoot
	for _, j := range c.upstreams[i] {
		<-c.done[j]
		c.mu.Lock()
		up := c.results[j]
		c.mu.Unlock()
		if up.Status != StatusPlanned {
			return nil, fmt.Errorf("this root reads estate %q's outputs, and root %s, which owns it, did not plan (stage %s), so this root is not planned on that estate's planned outputs, and never on what it last applied in their place", c.estates[j], c.paths[j].rel, up.Stage)
		}
		from = append(from, PlannedFromRoot{
			Root:     c.paths[j].rel,
			Estate:   c.estates[j],
			PlanJSON: relTo(c.base, planJSONPath(c.paths[j].planFile)),
		})
	}
	sort.Slice(from, func(a, b int) bool { return from[a].Root < from[b].Root })
	return from, nil
}

// planJSONPath is where an upstream's "show -json" document is written,
// beside its plan file: OUT/ROOT.tfplan.json.
func planJSONPath(planFile string) string { return planFile + ".json" }
