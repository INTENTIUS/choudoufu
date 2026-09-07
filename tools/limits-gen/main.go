// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command limits-gen writes live/LIMITATIONS.md's per-refusal content from
// the registries that define the refusals, instead of from someone's memory
// of them.
//
// This is GitHub issue #110's second acceptance criterion. The document's job
// is to tell an operator whether their configuration can move to live
// markers, and it was hand-written against a set nothing could enumerate.
// Thirteen of the sixteen lint rules had an entry here; three cited the issue
// tracker instead. The thirty-two identity refusals had three between them.
// And the largest single blocker in the #102 corpus, along with two more of
// the top seven, was in no table at all. Anything hand-maintained against a
// set that large drifts, and the drift is invisible until someone hits an
// undocumented refusal.
//
// # What it generates, and what it does not
//
// Three spans, marked the way tools/survey-gen already marks its own (see
// internal/live/mdspan). Two sit inside live/LIMITATIONS.md's "Every
// refusal, enumerated" section:
//
//   - refusal-table: every refusal in internal/live/check's AllRefusals,
//     ranked by how many corpus configurations it blocks, with where it is
//     documented and which package raises it.
//   - refusal-entries: a heading and a description per non-lint refusal.
//     Most point their DocsRef at the heading written here, which is what
//     closes the loop: internal/live/check's
//     TestEveryRefusalDocsRefIsResolvable fails if this has not been run.
//     The seven whose registry defers to a hand-written entry get a section
//     that says what they are and then names it, so a What that has drifted
//     from the prose is visible instead of invisible (#698).
//
// The third heads the hand-written half:
//
//   - lint-roster: one row per lint rule - the rule, its summary, its
//     severity, the entry below that documents it, and that entry's fixture
//     directory under live/e2e/limits. The prose stays hand-written; the
//     roster of it does not, so a rule with no entry or an entry whose
//     fixture directory was renamed fails the render (#698).
//
// The render fails, writing nothing, when a refusal it would give an entry
// to has no description, and when a refusal points at a heading in this
// document that nobody wrote. Both used to be tests over the committed file
// alone; a generator that will happily write a broken document and leave the
// finding to CI is one round trip worse than one that refuses.
//
// AllRefusals rather than Catalog is deliberate. The corpus ranks the two
// passes it can run without a cloud; documentation covers all five, and a
// stamping refusal shows a dash in the frequency columns because it has
// never been measured rather than because it blocks nothing.
//
// The narrative sections stay hand-written, per #110's own scope: the
// "Enforced today" entries, which carry a Construct / Why banned /
// Forwarding address / Enforcement treatment per lint rule, are prose nobody
// should generate. The table links to them, and the roster indexes them.
//
// Measured at #698's commit: 378397 bytes, of which 59.1% were already
// inside a generated span - this generator's and survey-gen's - and 40.9%
// hand prose, 109611 bytes of it the "Enforced today" entries.
//
// Frequency comes from live/corpus-refusals.json when it is present. A
// refusal missing from that artifact is rendered as unmeasured rather than
// as zero, because those are different claims and only one of them is true
// when the corpus has not been regenerated.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/limits-gen
//
// No provider, no network, no cloud. It reads compiled-in tables and one
// committed artifact.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/docsref"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/mdspan"
)

const (
	limitationsRel = "live/LIMITATIONS.md"
	corpusRel      = "live/corpus-refusals.json"

	spanTable   = "refusal-table"
	spanEntries = "refusal-entries"
)

// markers is this generator's own span vocabulary. Two generators write into
// live/LIMITATIONS.md and neither may touch the other's regions.
var markers = mdspan.For("limits-gen")

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "limits-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	freq, measured, err := readCorpus(filepath.Join(root, corpusRel))
	if err != nil {
		return err
	}
	if !measured {
		fmt.Fprintf(os.Stderr, "limits-gen: %s is absent; frequencies render as unmeasured\n", corpusRel)
	}

	path := filepath.Join(root, filepath.FromSlash(limitationsRel))
	src, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}

	catalog := check.AllRefusals()
	out, err := markers.Replace(limitationsRel, string(src), spanTable, renderTable(catalog, freq, measured))
	if err != nil {
		return err
	}
	entries, err := renderEntries(catalog, freq, measured)
	if err != nil {
		return err
	}
	out, err = markers.Replace(limitationsRel, out, spanEntries, entries)
	if err != nil {
		return err
	}
	roster, err := renderRoster(catalog, root)
	if err != nil {
		return err
	}
	out, err = markers.Replace(limitationsRel, out, spanRoster, roster)
	if err != nil {
		return err
	}

	sweep, err := readWoSweep(filepath.Join(root, filepath.FromSlash(woSweepRel)))
	if err != nil {
		return err
	}
	out, err = applyResidueSpans(out, sweep)
	if err != nil {
		return err
	}

	// Last, and against the rendered text rather than against what was on
	// disk: a hand-written entry this run just replaced would otherwise be
	// checked in the version that still had it.
	if err := verifyCoverage(catalog, out); err != nil {
		return err
	}

	if out == string(src) {
		fmt.Fprintf(os.Stderr, "limits-gen: %s is already current\n", limitationsRel)
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "limits-gen: rewrote %s (%d refusals)\n", limitationsRel, len(catalog))
	return nil
}

// frequency is one refusal's corpus measurement.
type frequency struct {
	Configs int
	Sites   int
}

// readCorpus reads the committed #102 artifact, keyed the way the catalog is.
//
// A missing artifact is not an error: this generator has to run in a
// checkout where the corpus has never been fetched, and saying "unmeasured"
// is the honest rendering of that.
func readCorpus(path string) (map[string]frequency, bool, error) {
	src, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	var artifact struct {
		Refusals []struct {
			Layer   string `json:"layer"`
			ID      string `json:"id"`
			Configs int    `json:"configs"`
			Sites   int    `json:"sites"`
		} `json:"refusals"`
	}
	if err := json.Unmarshal(src, &artifact); err != nil {
		return nil, false, fmt.Errorf("%s: %w", corpusRel, err)
	}

	out := make(map[string]frequency, len(artifact.Refusals))
	for _, r := range artifact.Refusals {
		out[r.Layer+"/"+r.ID] = frequency{Configs: r.Configs, Sites: r.Sites}
	}
	return out, true, nil
}

func key(r check.Refusal) string { return string(r.Layer) + "/" + r.ID }

// severityLabel renders a refusal's severity for the table.
//
// [check.Refusal] carries no severity field, so each layer that has a
// mechanism for anything but fatal is asked directly, by the ID that layer's
// refusals are keyed on. Two do today:
//
//   - lint, whose refusal ID is its rule's own string ([lint.Rule.Severity],
//     GitHub issue #214);
//   - discovery, whose refusal ID is the diagnostic Summary and whose
//     [discovery.SeverityForRefusal] is the same call the diagnostic itself
//     is built from - five of its refusals are warnings;
//   - dataread, which serves two demand classes with opposite contracts
//     ([dataread.StopsTheRun]): an identity demand it cannot meet refuses
//     the run, and a root-output demand it cannot meet costs one output its
//     prior value and nothing else.
//
// This function said "error" for every discovery refusal until that second
// case existed, which put four warnings in the table as blockers: an
// unresolvable account ID, a displaced marker, a tagged ARN that could not
// be joined, an owned resource of an unsweepable type - plus the sweep-gap
// note. A reader ranking work off this table was reading four to five
// non-blockers as things that stop a run.
//
// The remaining layers answer "error" because nothing in them can yet
// declare otherwise, not because this generator assumes so.
func severityLabel(r check.Refusal) string {
	switch r.Layer {
	case check.LayerLint:
		if lint.Rule(r.ID).Severity() == lint.SeverityWarning {
			return "warning"
		}
	case check.LayerDiscovery:
		if discovery.SeverityForRefusal(r.ID) == discovery.SeverityWarning {
			return "warning"
		}
	case check.LayerDataread:
		if !dataread.StopsTheRun(r.ID) {
			return "warning"
		}
	}
	return "error"
}

// ranked returns the catalog ordered the way the corpus artifact ranks it:
// by configurations blocked, then sites, then layer and ID.
//
// Ranking by configurations rather than sites is #102's ruling and it is
// repeated here rather than re-decided: a raw site count is dominated by
// whichever configuration happened to be largest, so one enormous estate
// would outrank a refusal that stops every configuration in the corpus dead.
func ranked(catalog []check.Refusal, freq map[string]frequency) []check.Refusal {
	out := append([]check.Refusal(nil), catalog...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := freq[key(out[i])], freq[key(out[j])]
		if a.Configs != b.Configs {
			return a.Configs > b.Configs
		}
		if a.Sites != b.Sites {
			return a.Sites > b.Sites
		}
		if out[i].Layer != out[j].Layer {
			return out[i].Layer < out[j].Layer
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// renderTable renders the ranked index of every refusal.
func renderTable(catalog []check.Refusal, freq map[string]frequency, measured bool) string {
	var b strings.Builder
	b.WriteString("| Configs | Sites | Layer | Refusal | Severity | Raised by | Documented at |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range ranked(catalog, freq) {
		// A dash is "not measured", a zero is "measured and blocked
		// nothing". Collapsing the two would be the exact failure this
		// artifact exists to avoid: a refusal absent from a stale corpus
		// run would read as harmless.
		configs, sites := "-", "-"
		if f, ok := freq[key(r)]; ok && measured {
			configs, sites = fmt.Sprint(f.Configs), fmt.Sprint(f.Sites)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | `%s` | %s |\n",
			configs, sites, r.Layer, mdCell(r.ID), severityLabel(r), r.RaisedBy, mdCell(documentedAt(r)))
	}

	fmt.Fprintf(&b, "\n**%d refusals**, from every registry the live path has: "+
		"`internal/live/lint`'s rule table, and `internal/live/identity`'s, "+
		"`internal/live/passthrough`'s, `internal/live/stamp`'s and "+
		"`internal/live/discovery`'s. A refusal blocking nothing is not an "+
		"error in this table - it is the interesting end of it, and a set "+
		"assembled by watching output could never contain one. **Severity** "+
		"is `error` (fatal, stops the run) unless marked `warning`. Three "+
		"layers can declare `warning` today: a lint rule (GitHub issue "+
		"#214's `state-backend`), a discovery refusal, whose severity is "+
		"read from the same call the diagnostic is built from, and a "+
		"dataread refusal belonging to the root-output demand class, which "+
		"costs one output its prior value rather than the run. A `warning` "+
		"does not stop the run - it says this run saw less than the whole "+
		"picture, or found something outside its own coverage - so it is not "+
		"a blocker and should not be ranked as one.\n", len(catalog))
	if measured {
		fmt.Fprintf(&b, "\nCounts are from `%s`, over the corpus that artifact names. "+
			"Read them as a ranking and not as a rate: the corpus leans on module "+
			"`examples/`, which use variables, conditionals and `dynamic` blocks "+
			"harder than an ordinary estate does. A dash means the refusal is in "+
			"the registries but was not measured. Every `stamp` and `discovery` "+
			"row shows one: those two passes need a cloud, so no corpus run "+
			"reaches them.\n", corpusRel)
	} else {
		fmt.Fprintf(&b, "\nCounts are absent because `%s` was not present when this was "+
			"generated. Run `just corpus-fetch && just corpus`, then regenerate.\n", corpusRel)
	}
	return b.String()
}

// documentedAt renders the DocsRef column. A reference into this same
// document drops the filename, because repeating it on nearly every row
// crowds out the part that varies.
func documentedAt(r check.Refusal) string {
	ref, err := docsref.Parse(r.DocsRef)
	if err != nil || ref.Doc != limitationsRel || len(ref.Headings) == 0 {
		return r.DocsRef
	}
	quoted := make([]string, len(ref.Headings))
	for i, h := range ref.Headings {
		quoted[i] = `"` + h + `"`
	}
	return strings.Join(quoted, " / ")
}

// hasGeneratedEntry reports whether the entries span carries a section for
// this refusal at all.
//
// Every refusal except a lint rule's, which is GitHub issue #698's widening.
// The span used to carry only the refusals whose DocsRef pointed at its own
// heading, which left seven non-lint refusals - the ones whose registry sets
// Doc to defer to a hand-written entry - with a What string in code that
// appeared nowhere in the document. Those seven now get a section that says
// what they are and then names the fuller treatment, so the enumerated
// section really does enumerate, and a drifting What is visible.
//
// Lint rules stay out, and not only because their prose is hand-written: a
// lint rule's DocsRef can coincide with the heading this generator would
// write - `unadmitted-type`'s hand-written entry is headed with the rule's
// own name - and the coincidence produced a second, empty entry for it. Lint
// carries no What at all (its per-issue Detail is the equivalent), so a
// generated entry for a rule is a heading with nothing under it. They are
// covered by the roster instead; see [spanRoster].
func hasGeneratedEntry(r check.Refusal) bool { return r.RaisedBy != check.RaisedByLint }

// ownsEntry reports whether this generator's own heading for a refusal is
// where that refusal's DocsRef points, which is the case for every refusal
// nobody wrote a fuller treatment for.
func ownsEntry(r check.Refusal) bool {
	return hasGeneratedEntry(r) && r.DocsRef == generatedRef(r)
}

// renderEntries renders one heading per non-lint refusal: what trips it,
// which pass raises it, how often the corpus saw it, and - for the ones a
// human wrote a fuller treatment for - where that treatment is.
//
// It returns an error rather than rendering a heading with nothing under it.
// That is GitHub issue #698's first acceptance criterion, and it belongs in
// the render and not only in a test: a refusal added to a registry with no
// What is a refusal a user can hit and cannot look up, and `just limits`
// should refuse to write the document that pretends otherwise. The test that
// used to be the only check on this (TestEveryGeneratedEntryHasContent) now
// asserts the render fails rather than asserting the strings are non-empty.
func renderEntries(catalog []check.Refusal, freq map[string]frequency, measured bool) (string, error) {
	var b strings.Builder
	for _, r := range ranked(catalog, freq) {
		if !hasGeneratedEntry(r) {
			continue
		}
		if strings.TrimSpace(r.What) == "" {
			return "", fmt.Errorf("%s/%s gets an entry in %s's %s span and has no What, which renders as a heading with nothing under it; give the refusal a description in its registry",
				r.Layer, r.ID, limitationsRel, spanEntries)
		}
		if r.RaisedBy == "" {
			return "", fmt.Errorf("%s/%s gets an entry in %s's %s span and names no raising package",
				r.Layer, r.ID, limitationsRel, spanEntries)
		}
		fmt.Fprintf(&b, "#### %s\n\n", r.ID)
		fmt.Fprintf(&b, "**What.** %s\n\n", r.What)
		// A pass-through refusal is not named with a pass. The catalog files
		// it under identity because that is where most of them surface, but
		// two are raised while the configuration is still being decoded, and
		// an audit caught the generated entry telling the reader those two
		// come from the identity pass. Naming the package that raises it is
		// true of all of them.
		if r.Passthrough() {
			fmt.Fprintf(&b, "**Where.** Raised by `%s` and passed through: this is a diagnostic the live path shows without having written it. See the section preamble.\n\n", r.RaisedBy)
		} else {
			fmt.Fprintf(&b, "**Where.** The %s pass, raised by `%s`.\n\n", r.Layer, r.RaisedBy)
		}
		f, inArtifact := freq[key(r)]
		switch {
		case !measured || !inArtifact:
			b.WriteString("**How often.** Not measured: absent from the corpus artifact this was generated against.\n\n")
		case f.Configs == 0:
			b.WriteString("**How often.** Blocked no configuration in the measured corpus.\n\n")
		default:
			fmt.Fprintf(&b, "**How often.** Blocked %s in the measured corpus, at %s.\n\n",
				plural(f.Configs, "configuration"), plural(f.Sites, "site"))
		}
		if !ownsEntry(r) {
			// A refusal whose registry set Doc: somebody wrote it a fuller
			// treatment somewhere, and this section's job is to get the
			// reader there rather than to compete with it.
			ref, err := docsref.Parse(r.DocsRef)
			if err != nil {
				return "", fmt.Errorf("%s/%s: %w", r.Layer, r.ID, err)
			}
			fmt.Fprintf(&b, "**Full entry.** `%s`%s - hand-written, and the authority on this refusal.\n\n", ref.Doc, headingSuffix(ref))
		}
	}
	return b.String(), nil
}

// headingSuffix renders the heading half of a parsed reference for prose,
// or nothing when the reference names a whole document.
func headingSuffix(ref docsref.Ref) string {
	if len(ref.Headings) == 0 {
		return ""
	}
	quoted := make([]string, len(ref.Headings))
	for i, h := range ref.Headings {
		quoted[i] = `"` + h + `"`
	}
	return ", " + strings.Join(quoted, " / ")
}

// verifyCoverage is GitHub issue #698's first acceptance criterion applied to
// the half of the catalog this generator does not write entries for: a
// refusal whose DocsRef sends the reader to a heading in this document that
// nobody wrote.
//
// internal/live/check's TestEveryRefusalDocsRefIsResolvable checks the same
// thing against the committed file. Checking it here as well is what makes
// the criterion hold at the moment the document is written rather than at
// the moment somebody runs the suite: `just limits` on a tree with a new,
// undocumented lint rule now fails and writes nothing, instead of writing a
// document that references an entry which does not exist.
func verifyCoverage(catalog []check.Refusal, rendered string) error {
	present := docsref.Headings(rendered)
	for _, r := range catalog {
		ref, err := docsref.Parse(r.DocsRef)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", r.Layer, r.ID, err)
		}
		if ref.Doc != limitationsRel {
			continue
		}
		for _, heading := range ref.Headings {
			if !present[heading] {
				return fmt.Errorf("%s/%s is documented at %s, %q, and this document has no such heading; write the entry, or point the refusal at the document that explains it",
					r.Layer, r.ID, limitationsRel, heading)
			}
		}
	}
	return nil
}

// generatedRef is the DocsRef a refusal has when this generator owns its
// entry. It mirrors identity.Refusal.DocsRef and passthrough.Refusal.DocsRef,
// and the mirroring is checked: internal/live/check's
// TestEveryRefusalDocsRefIsResolvable fails if a refusal points at a heading
// this generator did not write.
func generatedRef(r check.Refusal) string {
	return fmt.Sprintf("%s, %q", limitationsRel, r.ID)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// mdCell escapes the one character that would break out of a markdown table
// cell. Two refusal summaries carry double quotes and none carry a pipe
// today, but the table is generated from strings this tool does not control.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// repoRoot resolves the checkout root from this file's own location, so the
// generator runs the same from anywhere.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/limits-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}
