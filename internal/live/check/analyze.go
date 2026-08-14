// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Context is what a caller can tell the analysis about the world outside the
// configuration. It is [lint.Context] and [identity.Context]'s shared field,
// under one name, because both passes must be told the same thing: a type
// the schemas admit has to read the same answer at both, or a lint refusal
// and a resolution refusal will disagree about the same type.
type Context struct {
	// Schemas are the provider's managed resource type schemas, keyed by
	// type name.
	//
	// Running without them is supported and is materially less accurate,
	// in one direction that matters to this instrument specifically: a
	// type absent from the generated admission table is refused as
	// unadmitted when the provider's own identity schema would have
	// settled it. That single rule would then top any ranking built from
	// the result, which is the one outcome #102 exists to prevent. So
	// [Report.Schemas] records whether they were present and every front
	// end says so.
	Schemas map[string]providers.Schema
}

// Analyze runs both configuration-only passes over one loaded configuration
// and returns what refused it.
//
// This is the whole of the shared instrument. "choudoufu live-check" renders
// one of these for a human; tools/corpus-gen folds many into a ranking. Both
// see the same findings from the same two passes, which is what keeps the
// project's published compatibility claim and a user's own verdict from
// drifting apart.
func Analyze(ctx context.Context, cfg *configs.Config, actx Context) Report {
	report := Report{
		Schemas:   len(actx.Schemas) > 0,
		Checked:   CheckedLayers(),
		Unchecked: UncheckedLayers(),
	}
	if cfg == nil {
		return report
	}

	findings := findingMap{}

	for _, issue := range lint.CheckWith(ctx, cfg, lint.Context{Schemas: actx.Schemas}) {
		f := findings.get(LayerLint, string(issue.Rule))
		f.add(Site{
			Address: issue.Construct,
			Type:    issue.Type,
			Module:  issue.Module.String(),
			Detail:  issue.Detail,
			File:    issue.Subject.Filename,
			Line:    issue.Subject.Start.Line,
			Column:  issue.Subject.Start.Column,
		})
	}

	// Where lint already refused a construct, identity's verdict on the
	// same construct is not a second refusal to count.
	//
	// The two passes ask the same question of some constructs on purpose -
	// lint's admission check and identity's resolution consult the same
	// schemas so that "a lint refusal and a resolution refusal never
	// disagree about the same type". In a live-plan run only one is ever
	// seen, because lint is fatal and identity never runs. Here both run,
	// so that a configuration blocked by lint still reports what identity
	// would have said about everything else - but counting one unadmitted
	// resource as two blocked refusals would inflate exactly the ranking
	// this instrument exists to produce.
	refusedByLint := make(map[string]bool)
	for _, f := range findings {
		for _, site := range f.Sites {
			if loc := site.location(); loc != "" {
				refusedByLint[loc] = true
			}
		}
	}

	result, diags := identity.ResolveWith(ctx, cfg, identity.Context{Schemas: actx.Schemas})
	if result != nil {
		report.Instances = result.Len()
	}
	for _, diag := range diags {
		desc := diag.Description()
		site := Site{
			Detail: desc.Detail,
		}
		if src := diag.Source(); src.Subject != nil {
			site.File = src.Subject.Filename
			site.Line = src.Subject.Start.Line
			site.Column = src.Subject.Start.Column
		}

		switch diag.Severity() {
		case tfdiags.Error:
			if loc := site.location(); loc != "" && refusedByLint[loc] {
				report.Shadowed++
				continue
			}
			f := findings.get(LayerIdentity, desc.Summary)
			f.add(site)
		default:
			// Warnings are collected but never make the verdict negative.
			// identity's schema-disagreement refusal is the population
			// here, and it means provider-version skew rather than a
			// configuration this mode cannot move.
			report.addWarning(desc.Summary, site)
		}
	}

	for _, f := range findings {
		report.Findings = append(report.Findings, *f)
	}
	rank(report.Findings)
	rank(report.Warnings)
	return report
}

// Dir loads one directory and analyzes it: the entry point both front ends
// call, and the reason they cannot drift.
//
// A directory that will not load comes back as a report with no findings and
// a [Report.Load] carrying the diagnostics, which callers must check before
// reading "no findings" as "nothing refused it". [Report.Readable] is that
// check.
func Dir(ctx context.Context, dir string, actx Context) Report {
	load := Load(ctx, dir)
	report := Analyze(ctx, load.Config, actx)
	report.Load = load
	return report
}

// Report is one configuration's verdict.
type Report struct {
	// Findings are the refusals that fired, ranked by how many sites each
	// blocks. Empty means both checked passes accepted the configuration.
	Findings []Finding

	// Warnings are the non-fatal diagnostics, ranked the same way. They do
	// not affect [Report.Blocked].
	Warnings []Finding

	// Instances is how many managed resource instances resolved.
	Instances int

	// Shadowed is how many identity refusals fell on a construct lint had
	// already refused, and were therefore not counted as findings. It is
	// reported rather than dropped silently so that the dedupe rule can be
	// checked against a run instead of taken on trust.
	Shadowed int

	// Schemas records whether provider schemas were available. See
	// [Context.Schemas] for why a report is worth less without them.
	Schemas bool

	// Load carries what loading the directory cost, when the caller used
	// [Load]. Unresolved modules and unset variables both bound what the
	// findings below can be trusted to cover.
	Load LoadResult

	// Checked and Unchecked are the live-path stages this analysis did and
	// did not run. Both are reported, always: a verdict that named only
	// what passed would read as a promise about stages nobody looked at.
	Checked   []Layer
	Unchecked []Layer
}

// Readable reports whether the configuration could be loaded at all.
//
// It matters because an unreadable directory also has no findings, and the
// two must never render the same way: "nothing refused this" and "nothing
// could be read" are opposite answers to the question a user asked.
func (r Report) Readable() bool { return r.Load.Config != nil }

// Blocked reports whether this configuration can move under live markers at
// all, as far as the two checked passes can tell.
//
// The rule is not this package's opinion: it is what LivePlanCommand already
// does with the same two results. Any lint issue is fatal there, and any
// identity error diagnostic is fatal there, because a partial identity map
// makes the plan propose creating objects that already exist.
func (r Report) Blocked() bool { return len(r.Findings) > 0 }

// Sites is the total number of refused sites across every finding.
func (r Report) Sites() int {
	var n int
	for _, f := range r.Findings {
		n += len(f.Sites)
	}
	return n
}

// Finding is one refusal and every place it fired in this configuration.
type Finding struct {
	Refusal

	// Sites are where it fired, ordered by file and line.
	Sites []Site

	// Registered is false when the refusal's identity was not in
	// [Catalog]. It means this package and identity's registry have
	// drifted, and the finding is reported under its raw summary rather
	// than dropped.
	Registered bool
}

// Site is one place a refusal fired.
type Site struct {
	// Address is the offending construct in address form, where the rule
	// has one.
	Address string

	// Type is the managed resource type, set only for the type-shaped
	// rules. See [lint.Issue.Type] and [Finding.Types].
	Type string

	// Module is the module path the site is in; empty for the root.
	Module string

	// Detail is the per-site explanation, which is also the remedy text
	// #101's audit put into these messages.
	Detail string

	// File, Line and Column locate it.
	File   string
	Line   int
	Column int
}

// location renders the site as "file:line", the form an editor and a reader
// both follow.
func (s Site) location() string {
	if s.File == "" {
		return ""
	}
	if s.Line == 0 {
		return s.File
	}
	return fmt.Sprintf("%s:%d", s.File, s.Line)
}

// Location is [Site.location] for callers outside this package.
func (s Site) Location() string { return s.location() }

// Types summarizes a type-shaped finding as a count per resource type,
// sorted by count and then name.
//
// It returns nothing for every other rule. This exists because the two
// type-shaped rules are the ones that produce hundreds of near-identical
// sites in a real configuration, and #114 asks for them summarized rather
// than enumerated - the resolution layer's unadmitted-type refusal once
// interpolated the whole admitted list into a single 25KB error, and a
// report that listed every site would be that mistake with more steps.
func (f Finding) Types() []TypeCount {
	counts := make(map[string]int)
	for _, site := range f.Sites {
		if site.Type == "" {
			continue
		}
		counts[site.Type]++
	}
	if len(counts) == 0 {
		return nil
	}

	out := make([]TypeCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, TypeCount{Type: name, Sites: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sites != out[j].Sites {
			return out[i].Sites > out[j].Sites
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// TypeCount is one resource type's share of a type-shaped finding.
type TypeCount struct {
	Type  string `json:"type"`
	Sites int    `json:"sites"`
}

// Remedy is what to do about this refusal, which is the first site's detail.
//
// The refusal registry's What is preferred where it has one, since it is
// written once per rule rather than once per site. Neither is authored
// here: #101 audited every one of these messages into saying what is
// actually true, and restating them in a report would be a second place for
// them to go stale.
func (f Finding) Remedy() string {
	if f.What != "" {
		return f.What
	}
	if len(f.Sites) > 0 {
		return f.Sites[0].Detail
	}
	return ""
}

type findingKey struct {
	layer Layer
	id    string
}

type findingMap map[findingKey]*Finding

func (m findingMap) get(layer Layer, id string) *Finding {
	key := findingKey{layer: layer, id: id}
	if f, ok := m[key]; ok {
		return f
	}
	refusal, registered := lookup(layer, id)
	f := &Finding{Refusal: refusal, Registered: registered}
	m[key] = f
	return f
}

func (f *Finding) add(site Site) {
	f.Sites = append(f.Sites, site)
}

func (r *Report) addWarning(summary string, site Site) {
	for i := range r.Warnings {
		if r.Warnings[i].ID == summary {
			r.Warnings[i].Sites = append(r.Warnings[i].Sites, site)
			return
		}
	}
	refusal, registered := lookup(LayerIdentity, summary)
	r.Warnings = append(r.Warnings, Finding{
		Refusal:    refusal,
		Sites:      []Site{site},
		Registered: registered,
	})
}

// rank orders findings the way both front ends present them: most sites
// first, then by identity so that two runs over the same configuration
// produce the same order.
//
// Ranking by site count rather than by rule order is #114's second
// requirement, and it is the same choice #102 makes for the corpus with a
// different denominator. Sites within a finding are ordered by position so
// the first few printed are the first few in the file.
func rank(findings []Finding) {
	for i := range findings {
		sites := findings[i].Sites
		sort.SliceStable(sites, func(a, b int) bool {
			if sites[a].File != sites[b].File {
				return sites[a].File < sites[b].File
			}
			return sites[a].Line < sites[b].Line
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if len(findings[i].Sites) != len(findings[j].Sites) {
			return len(findings[i].Sites) > len(findings[j].Sites)
		}
		if findings[i].Layer != findings[j].Layer {
			return findings[i].Layer < findings[j].Layer
		}
		return findings[i].ID < findings[j].ID
	})
}
