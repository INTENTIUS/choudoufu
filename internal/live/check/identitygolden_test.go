// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cohorts"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is the value instrument. Everything else in this repository that
// measures anything - Report.Blocked, Report.Instances, Report.Sites,
// live/corpus-refusals.json, live/cohort-acceptance.json,
// tools/refusal-probe - measures a predicate over a verdict. All of them
// answer "did we refuse?". None of them looks at what a resolution actually
// rendered.
//
// The product's output is a string written into a cloud tag and handed to an
// import. A resolution that renders the WRONG string refuses nothing, so it
// raises no finding, moves no count, and reads as a success in every existing
// artifact. GitHub issue #251 is the worked case: it turned a refusal into a
// marker naming a queue that does not exist, so Report.Instances went UP and
// the change was measured, merged and reported as a win. Every recorded field
// was byte-identical before and after - readable=true blocked=false
// instances=3 sites=0 findings=0 - and the only differing byte in the whole
// system was an import ID moving from ".../q-007" to ".../q-7".
//
// # What this catches, honestly
//
// Of the defects of this shape that have been classified, eight present as a
// MODIFIED line in the golden and are caught automatically: git shows the old
// identity and the new one side by side, and a changed identity for an
// unchanged fixture is categorically an alarm.
//
// Three present as an ADDED line, because the run previously refused and now
// resolves. A diff cannot judge those - a new line is what the whole campaign
// is trying to produce - but it puts the rendered value in front of a reviewer
// who today sees only a count going up. The three read "item-host", "q-007"
// and "//unprojected-ami-instance_type-subnet_id", which are visibly absurd to
// a human and invisible to every instrument.
//
// So: catches eight automatically, surfaces three to a reader who currently
// sees nothing. It does not catch the class.
//
// # What it cannot see, measured rather than assumed
//
// Reverting #251's own fix in a worktree and rerunning this test fails it, as
// 3 added and 2 removed lines - which is the proof it works, and also the
// proof of two bounds.
//
// The first: none of the five lines is in typedvar-number, the fixture the
// defect was FOUND in. That fixture's resource is an aws_sqs_queue, whose
// identity is a queue URL carrying the caller's own AWS account ID. No
// offline analysis knows that account, so both the right identity and the
// wrong one classify NEEDS_DISCOVERY here and neither renders. Every type
// whose identity needs a live account, a server-assigned ID, or a parent's
// real ID is invisible to this file in the same way: roughly two rows in five
// are NEEDS_DISCOVERY or RECORD_BACKED with nothing in the value column. It
// covers the CONCRETE rows and the PARENT_DERIVED ones whose formula is
// symbolic. The exact split is the "# shape:" block at the top of the golden
// and the pin in live/identity_golden_pin_test.go; it is not repeated here,
// because the version of this sentence that named 550, 1320, 658 and 95
// outlived all four.
//
// The second is the direction-of-merit problem, reproduced exactly. With the
// fix reverted, the sweep resolved 1321 instances against 1320 - the count
// went UP - while three fabricated identities appeared (module.child's IAM
// users keyed "n-0" and "n-1", which OpenTofu keys "n-alpha" and by the
// group's live name, because a set's keys ARE its elements) and two correct
// ones vanished. Instances, Sites, Blocked and Findings all read that as an
// improvement. Only the values say otherwise.
//
// # The churn is the point, not a defect
//
// This file changes on most merges, because most merges resolve more
// instances. That is fine and is what makes it useful: git separates added
// lines from modified lines, and the two mean opposite things. Reviewing the
// diff is the work; a large diff of purely added lines is the campaign
// working.
//
// # Two sections, and why the second one is generated
//
// The file has two delimited halves. The first is the committed fixtures:
// every configuration directory under internal/live and live, labelled by its
// path in the checkout. The second is the 31 verification cohorts, labelled
// "<cohorts>/<name>", which are not in the checkout at all - issue #699
// stopped committing them because they were generator output that every
// working copy then filled with an ignored .terraform/. They are rendered
// into this run's own temporary directory by the same
// [flocitest.GenerateCohorts] the acceptance tier uses, and their rendered
// identities are pinned here by value exactly as the committed ones are.
//
// That second half was unpinned for one commit (#929) and pinned again for
// GitHub issue #930, on the maintainer's reading that nothing about the risk
// moved when the fixtures became generated: the product still renders those
// identities on every cohort run, and a wrong one refuses nothing. What
// replaced the pin in between - a check that each cohort DECLARES the types
// its roster records - answers a different question than what each instance
// RESOLVES to.
//
// The cost of that decision is paid here and is worth stating where it is
// paid: generating the cohorts runs `terraform init` and launches the
// provider plugin, which is about thirteen seconds warm and needs `go`, a
// stock `terraform` and the pinned AWS provider on a machine that previously
// needed none of them to run this test.

var updateIdentityGolden = flag.Bool("update", false, "rewrite testdata/identity-golden.txt from this run")

// identityGoldenRoots are the trees swept. Both are in-repo and stable.
//
// .corpus is deliberately absent, unlike sweepRoots next door: it is a
// gitignored symlink that is not always present, and its instance count moves
// with whatever was last fetched. A golden over it would be a file that
// differs between two checkouts of the same commit, which is not a golden.
//
// internal/ at large is absent for a different reason: it is 1570
// configuration directories of upstream OpenTofu parser fixtures, and the
// signal-to-noise of pinning identities rendered from those is bad enough
// that the diff would stop being read.
var identityGoldenRoots = []string{"internal/live", "live"}

const identityGoldenPath = "testdata/identity-golden.txt"

// identityGoldenCohortLabel is what a generated cohort's rows carry in the
// "dir" column, in place of a checkout-relative path they do not have: the
// trees are rendered into t.TempDir(), whose name is different on every run,
// and a golden that differs between two runs of the same commit is not a
// golden.
const identityGoldenCohortLabel = "<cohorts>"

// The section markers. They are comment lines, so every reader that parses
// this file skips them - reportIdentityGoldenDiff's index, and
// live/identity_golden_pin_test.go's recount and digest, which hash rows
// only. They cost a line each and they buy the thing #930 asked for: a reader
// looking at the diff can tell which half a moved row is in, and the two
// halves mean different things. A moved row above the cohort marker is a
// committed fixture nobody edited resolving differently. A moved row below it
// is the generator's output having changed.
const (
	identityGoldenFixtureMarker = "# --- section: committed fixtures, one row per instance the in-repo .tf files resolve ---"
	identityGoldenCohortMarker  = "# --- section: verification cohorts, rendered by `go run ./tools/estate-gen -all` from internal/live/cohorts ---"
)

// identityGoldenSkipCohortsEnv turns the cohort generation off for an
// environment that genuinely cannot run it.
//
// It is an explicit opt-out rather than a silent fallback, and a missing `go`
// or `terraform` is a failure rather than a skip, because the failure this
// file exists to prevent is a green run over unverified identities. Somebody
// who sets this variable knows the cohort half went unchecked on that run; a
// LookPath that quietly degraded would be the same green with nobody knowing.
//
// Two things still hold with it set: the committed-fixture half is verified
// as usual, and the golden's cohort section must still be present and
// non-empty, so an opted-out run cannot pass over a truncated file. What does
// not hold is the by-value check on the cohort rows, which is the whole point
// of the flag being loud.
//
// -update refuses outright when it is set. Regenerating without the cohorts
// would write a golden with 700-odd rows missing, and that file would then
// look like a deletion somebody meant.
const identityGoldenSkipCohortsEnv = "CHOUDOUFU_GOLDEN_SKIP_COHORTS"

// identityGoldenEntry is one directory to analyze: the absolute path to read,
// and the label its rows carry.
//
// The two are separate fields because half the sweep has no path in the
// checkout. A committed fixture labels itself by its path under the
// repository root; a generated cohort labels itself <cohorts>/<name>, and the
// directory behind that label is gone when the test ends.
type identityGoldenEntry struct {
	label string
	dir   string
}

// TestIdentityGolden pins the rendered identity of every managed resource
// instance the in-repo fixtures and the generated verification cohorts
// resolve.
//
// Regenerate with:
//
//	env -u PWD go test -C "$(git rev-parse --show-toplevel)" ./internal/live/check -run TestIdentityGolden -update
//
// then READ the diff. A modified line is an alarm; an added line is progress
// whose rendered value you should still look at.
//
// The cohort half renders 31 estates with tools/estate-gen first, which needs
// `go`, a stock `terraform` and the pinned AWS provider; see
// [identityGoldenSkipCohortsEnv] for the opt-out and what it costs.
func TestIdentityGolden(t *testing.T) {
	root := flocitest.RepoRoot(t)
	fixtures := identityGoldenFixtureEntries(t, root)
	if len(fixtures) < 300 {
		t.Fatalf("found only %d configuration directories under %v; the walk is not reaching the tree it is supposed to cover, so a green result here proves nothing",
			len(fixtures), identityGoldenRoots)
	}

	scrubRoot := func(s string) string { return scrubIdentityGolden(root, s) }
	fixtureRows, fixtureClasses, fixtureInstances := identityGoldenRows(t, fixtures, scrubRoot)
	t.Logf("swept %d committed configuration directories under %v; %d instances resolved", len(fixtures), identityGoldenRoots, fixtureInstances)
	if fixtureInstances == 0 {
		t.Fatal("no instance resolved anywhere in the committed tree; the sweep is broken rather than the fixtures being empty")
	}

	cohortEntries, cohortRoot, generated := identityGoldenCohortEntries(t)
	if !generated {
		if *updateIdentityGolden {
			t.Fatalf("-update with %s set would write a golden with the cohort section missing, which reads as a deletion somebody meant.\n"+
				"Unset it and regenerate on a machine with go and terraform.", identityGoldenSkipCohortsEnv)
		}
		identityGoldenCompareFixturesOnly(t, fixtureRows)
		return
	}

	// The cohort trees live under a temp directory whose name changes every
	// run, and path.module/path.root render a directory straight into an
	// identity. Both scrubs, innermost first.
	scrubCohort := func(s string) string {
		return scrubRoot(strings.ReplaceAll(s, cohortRoot, identityGoldenCohortLabel))
	}
	cohortRows, cohortClasses, cohortInstances := identityGoldenRows(t, cohortEntries, scrubCohort)
	t.Logf("rendered and swept %d generated cohort directories; %d instances resolved", len(cohortEntries), cohortInstances)
	if cohortInstances == 0 {
		t.Fatal("the cohorts rendered but resolved no instance at all; that is a broken render or a broken sweep, not an empty roster")
	}

	got := identityGoldenFile(fixtureRows, cohortRows, len(fixtures), len(cohortEntries),
		fixtureInstances, cohortInstances, identityGoldenClassSum(fixtureClasses, cohortClasses))

	if *updateIdentityGolden {
		// The same relative path the comparison below reads, rather than one
		// rebuilt from the repository root: os.Getwd is the package
		// directory under "go test", and the root can carry the other
		// spelling of a symlinked checkout.
		if err := os.WriteFile(identityGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %s", identityGoldenPath, err)
		}
		t.Logf("wrote %s (%d bytes)", identityGoldenPath, len(got))
		return
	}

	wantBytes, err := os.ReadFile(identityGoldenPath)
	if err != nil {
		t.Fatalf("reading %s: %s\nRun the test with -update to create it.", identityGoldenPath, err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	reportIdentityGoldenDiff(t, want, got)
}

// TestIdentityGoldenCohortsAreDeterministic renders the roster twice, in two
// temporary directories, and holds the two runs to the same rows in the same
// order.
//
// It is separate from TestIdentityGolden, and it is the separable half of
// what pinning generated fixtures costs: it pays for a second `estate-gen
// -all` (about thirteen seconds warm) to prove a property of the GENERATOR
// rather than of any identity, so it can be dropped without unpinning a
// single row.
//
// The property is not free of doubt. The golden is one file compared byte for
// byte, so anything nondeterministic in the render - a map iteration reaching
// the HCL, a directory walk taking filesystem order, a timestamp - would show
// up as a test that fails on some runs and passes on others, which this
// repository has already learned to call a finding rather than a flake. This
// makes it a named failure with the two rows printed side by side instead.
func TestIdentityGoldenCohortsAreDeterministic(t *testing.T) {
	entriesA, rootA, generated := identityGoldenCohortEntries(t)
	if !generated {
		t.Skipf("%s is set; the generator's determinism is not checked on this run", identityGoldenSkipCohortsEnv)
	}
	entriesB, rootB, _ := identityGoldenCohortEntries(t)

	repo := flocitest.RepoRoot(t)
	rowsOf := func(entries []identityGoldenEntry, out string) string {
		rows, _, _ := identityGoldenRows(t, entries, func(s string) string {
			return scrubIdentityGolden(repo, strings.ReplaceAll(s, out, identityGoldenCohortLabel))
		})
		return rows
	}
	a, b := rowsOf(entriesA, rootA), rowsOf(entriesB, rootB)
	if a == b {
		return
	}

	// Every difference is one of these two, since equal-length slices whose
	// every element matches rejoin to equal strings: there is no third case
	// to fall through to.
	linesA, linesB := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(linesA) != len(linesB) {
		t.Fatalf("two renders of the same roster resolved a different number of instances: %d and %d.\n"+
			"The generator is not deterministic, so the golden's cohort section cannot be pinned by value until it is.",
			len(linesA)-1, len(linesB)-1)
	}
	for i := range linesA {
		if linesA[i] != linesB[i] {
			t.Fatalf("two renders of the same roster disagree at row %d:\n  first  %s\n  second %s\n"+
				"The generator is not deterministic, so the golden's cohort section cannot be pinned by value until it is.",
				i+1, linesA[i], linesB[i])
		}
	}
}

// identityGoldenCompareFixturesOnly is the opted-out path: the committed half
// is checked exactly as usual, the generated half is not checked at all, and
// the golden must still carry it.
//
// That last clause is the one worth reading twice. Without it, setting the
// opt-out and deleting the cohort section would be green here and caught only
// by the counts in live/identity_golden_pin_test.go - which is a real leg,
// but not one this file should be leaning on to notice that most of its own
// rows are gone.
func identityGoldenCompareFixturesOnly(t *testing.T, fixtureRows string) {
	t.Helper()

	wantBytes, err := os.ReadFile(identityGoldenPath)
	if err != nil {
		t.Fatalf("reading %s: %s", identityGoldenPath, err)
	}
	want := string(wantBytes)

	before, after, found := strings.Cut(want, identityGoldenCohortMarker)
	if !found {
		t.Fatalf("%s carries no cohort section (%q).\n"+
			"With %s set this run cannot regenerate it, and a golden missing it pins 700-odd fewer identities than it claims.",
			identityGoldenPath, identityGoldenCohortMarker, identityGoldenSkipCohortsEnv)
	}
	if n := len(identityGoldenRowLines(after)); n == 0 {
		t.Fatalf("%s carries the cohort marker and no cohort rows beneath it; the section was truncated.", identityGoldenPath)
	} else {
		t.Logf("%s is set: %d cohort rows in %s were NOT verified by value on this run", identityGoldenSkipCohortsEnv, n, identityGoldenPath)
	}

	if strings.Join(identityGoldenRowLines(before), "\n") == strings.TrimRight(fixtureRows, "\n") {
		return
	}
	reportIdentityGoldenDiff(t, before, fixtureRows)
}

// identityGoldenRowLines is every data row in a chunk of the file: comments
// and blanks dropped.
func identityGoldenRowLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// identityGoldenClassSum adds the two sections' class tallies, since the
// header's class lines cover the whole file.
func identityGoldenClassSum(sections ...map[string]int) map[string]int {
	out := map[string]int{}
	for _, m := range sections {
		for class, n := range m {
			out[class] += n
		}
	}
	return out
}

// identityGoldenRows produces one line per resolved instance, in the order
// the entries were given, which is sorted by label rather than by whatever
// order a directory walk or a generator returned.
func identityGoldenRows(t *testing.T, entries []identityGoldenEntry, scrub func(string) string) (rows string, classCounts map[string]int, instances int) {
	t.Helper()

	classCounts = map[string]int{}
	var body strings.Builder
	for _, e := range entries {
		report, panicked, stack := identityGoldenAnalyze(t.Context(), e.dir)
		if panicked != "" {
			// Not this test's job to assert on, but silently emitting zero
			// lines for a crashing directory would make the golden shrink
			// and read as a deletion rather than a crash.
			t.Errorf("%s panicked during analysis: %s\n%s", e.label, panicked, stack)
			continue
		}
		if !report.Readable() {
			continue
		}
		for _, res := range report.Identities {
			body.WriteString(e.label)
			body.WriteByte('\t')
			body.WriteString(res.Addr.String())
			body.WriteByte('\t')
			body.WriteString(string(res.Class))
			body.WriteByte('\t')
			body.WriteString(scrub(renderedIdentity(res)))
			body.WriteByte('\t')
			body.WriteString(scrub(renderedIdentityAttrs(res)))
			body.WriteByte('\n')
			classCounts[string(res.Class)]++
			instances++
		}
	}
	return body.String(), classCounts, instances
}

// identityGoldenFile assembles the whole file: the header, the shape block
// the pin in live/ reads back, and the two delimited sections.
//
// The digest covers the rows of both sections and nothing else - not the
// header, which would make it depend on itself, and not the section markers,
// because live/identity_golden_pin_test.go recomputes it by hashing every
// non-comment line and the two have to agree.
func identityGoldenFile(fixtureRows, cohortRows string, fixtureDirs, cohortDirs, fixtureInstances, cohortInstances int, classCounts map[string]int) string {
	var buf strings.Builder
	buf.WriteString("# internal/live/check/testdata/identity-golden.txt\n")
	buf.WriteString("#\n")
	buf.WriteString("# Rendered identity of every managed resource instance the in-repo\n")
	buf.WriteString("# fixtures and the generated verification cohorts resolve, analyzed\n")
	buf.WriteString("# without provider schemas.\n")
	buf.WriteString("#\n")
	buf.WriteString("# Generated by TestIdentityGolden -update. Do not hand-edit; regenerate\n")
	buf.WriteString("# and read the diff. A MODIFIED line is an alarm - a fixture nobody\n")
	buf.WriteString("# touched now renders a different identity. An ADDED line is the\n")
	buf.WriteString("# campaign working, and its value is still worth reading.\n")
	buf.WriteString("#\n")
	buf.WriteString(identityGoldenShapeBlock(fixtureDirs+cohortDirs, fixtureInstances+cohortInstances,
		fixtureDirs, cohortDirs, fixtureInstances, cohortInstances,
		classCounts, fixtureRows+cohortRows))
	buf.WriteString("#\n")
	buf.WriteString("# dir <TAB> address <TAB> class <TAB> rendered identity <TAB> identity attributes\n")
	buf.WriteString("#\n")
	buf.WriteString(identityGoldenFixtureMarker)
	buf.WriteString("\n")
	buf.WriteString(fixtureRows)
	buf.WriteString("#\n")
	buf.WriteString(identityGoldenCohortMarker)
	buf.WriteString("\n")
	buf.WriteString(cohortRows)
	return buf.String()
}

// identityGoldenShapeBlock is the summary the pin in live/ reads back.
//
// It exists because -update is a one-word way to make this test stop
// complaining, and the resulting diff is a changed testdata file among 1331
// lines - which is not a signal anybody reads. Hoisting the shape to the top
// means a silenced regression shows up as a changed number in the first
// fifteen lines of the diff instead.
//
// That is presentation, and presentation is not enforcement, so the numbers
// are also pinned in Go next to live/admission_coverage_test.go's ratchets.
// See live/identity_golden_pin_test.go for the leg that actually fails.
//
// The counts are computed from the same walk that writes the body, so a
// hand-edited header disagrees with a recount of its own rows.
//
// The digest is the leg that makes the pin cover VALUES and not only counts.
// An audit defeated the counts-only version in one edit: injecting a defect
// that rewrote 35 rendered ImportIDs and running -update left dirs, instances
// and every class count byte-identical, so both this test and the pin in live/
// went green over 35 changed markers. A count cannot see a value move, and the
// value is the product's whole output.
//
// The per-section lines carry their own key names ("fixture-dirs", not
// "dirs") deliberately: live/identity_golden_pin_test.go's parseShapeLine
// reads every "key=value" field on a "# shape:" line into one flat map, so a
// second line spelling "dirs=" would overwrite the total with a section's
// figure and the pin would then be checking the wrong number while looking
// entirely correct.
func identityGoldenShapeBlock(dirs, instances, fixtureDirs, cohortDirs, fixtureInstances, cohortInstances int, classCounts map[string]int, body string) string {
	classes := make([]string, 0, len(classCounts))
	for class := range classCounts {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	var b strings.Builder
	fmt.Fprintf(&b, "# shape: dirs=%d instances=%d\n", dirs, instances)
	for _, class := range classes {
		fmt.Fprintf(&b, "# shape: class %s=%d\n", class, classCounts[class])
	}
	fmt.Fprintf(&b, "# shape: fixture-dirs=%d fixture-instances=%d\n", fixtureDirs, fixtureInstances)
	fmt.Fprintf(&b, "# shape: cohort-dirs=%d cohort-instances=%d\n", cohortDirs, cohortInstances)
	fmt.Fprintf(&b, "# shape: body-sha256=%s\n", identityGoldenBodyDigest(body))
	return b.String()
}

// identityGoldenBodyDigest hashes the rows and nothing else - not the header,
// which would make the digest depend on itself.
//
// It is checkout-stable for the same reason the rows are: scrubIdentityGolden
// has already replaced this checkout's absolute path, and the walk is sorted.
func identityGoldenBodyDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// renderedIdentity is the string this resolution would put into the world,
// selected by class, because exactly one of the payload fields is populated.
//
// A needs-discovery or record-backed instance renders nothing on purpose: its
// identity is not in the configuration, so there is no value here to pin. The
// class column still moves if one of those becomes concrete, which is the
// transition worth seeing.
func renderedIdentity(res identity.Resolution) string {
	switch res.Class {
	case identity.ClassConcrete:
		return res.ImportID
	case identity.ClassParentDerived:
		if res.Formula == nil {
			return ""
		}
		return res.Formula.String()
	default:
		return ""
	}
}

// renderedIdentityAttrs is the other half of what gets written: the identity
// object the configuration supplies, unjoined. A join that is right while the
// split is wrong is the same defect shape one layer down, and the projection
// reads the split rather than the string for any type the provider serves an
// identity schema for.
func renderedIdentityAttrs(res identity.Resolution) string {
	var pairs map[string]string
	switch {
	case len(res.IdentityValues) > 0:
		pairs = res.IdentityValues
	case res.Formula != nil && len(res.Formula.Attrs) > 0:
		pairs = make(map[string]string, len(res.Formula.Attrs))
		for _, a := range res.Formula.Attrs {
			f := identity.Formula{Parts: a.Parts}
			pairs[a.Name] = f.String()
		}
	default:
		return ""
	}
	names := make([]string, 0, len(pairs))
	for k := range pairs {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, k := range names {
		out = append(out, k+"="+pairs[k])
	}
	return strings.Join(out, ",")
}

// scrubIdentityGolden removes anything that would make the file differ
// between two checkouts of the same commit: the absolute path of this one
// (path.module and path.root render it straight into an identity), and the
// column and line separators.
func scrubIdentityGolden(root, s string) string {
	if root != "" {
		s = strings.ReplaceAll(s, root, "<root>")
	}
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

func identityGoldenAnalyze(ctx context.Context, dir string) (report Report, panicked string, stack string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = fmt.Sprint(r)
			stack = string(debug.Stack())
		}
	}()
	// No schemas, deliberately: acquiring them costs minutes and needs a
	// network, and this instrument has to be cheap enough that nobody skips
	// it. What it loses is a class of instance that only a provider schema
	// admits, which shows up here as a line that is absent rather than a
	// line that is wrong.
	return Dir(ctx, dir, Context{}), "", ""
}

// identityGoldenFixtureEntries is the committed half of the sweep: every
// configuration directory under the roots, labelled by its path in the
// checkout and sorted by that label.
func identityGoldenFixtureEntries(t *testing.T, root string) []identityGoldenEntry {
	t.Helper()

	dirs := identityGoldenDirs(t, root)
	out := make([]identityGoldenEntry, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, identityGoldenEntry{label: rel(root, dir), dir: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	return out
}

// identityGoldenCohortEntries renders the whole cohort roster into this
// run's own temporary directory and returns one entry per rendered tree,
// sorted by label, plus the temp root to scrub out of any rendered value.
//
// generated is false only when [identityGoldenSkipCohortsEnv] is set. A
// missing tool is a failure here, not a skip: this file's whole subject is
// that a green run over unverified identities is the worst outcome available,
// and an environment that cannot render the cohorts has to say so out loud.
//
// It reuses [flocitest.GenerateCohorts] rather than shelling out again, so
// there is exactly one way in this repository to turn the roster into trees;
// a second one would drift, and the drift would land in a golden.
func identityGoldenCohortEntries(t *testing.T) (entries []identityGoldenEntry, tempRoot string, generated bool) {
	t.Helper()

	if v := os.Getenv(identityGoldenSkipCohortsEnv); v != "" {
		t.Logf("%s=%s: the generated cohorts are not rendered on this run", identityGoldenSkipCohortsEnv, v)
		return nil, "", false
	}
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, "terraform")

	dirs := flocitest.GenerateCohorts(t)
	if len(dirs) == 0 {
		t.Fatal("the cohort roster rendered no trees at all")
	}
	if got, want := len(dirs), len(cohorts.Names()); got != want {
		t.Fatalf("rendered %d cohort trees, want the roster's %d", got, want)
	}
	// Every tree is one level under the same parent, which is the string
	// that has to disappear from any identity a path.module reaches.
	tempRoot = filepath.Dir(dirs[0])
	for _, dir := range dirs {
		entries = append(entries, identityGoldenEntry{
			label: identityGoldenCohortLabel + "/" + filepath.Base(dir),
			dir:   dir,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].label < entries[j].label })
	return entries, tempRoot, true
}

// identityGoldenDirs is every directory under the roots holding a
// configuration file, sorted. Both the file filter and the sort matter: the
// filter has to be the set the loader actually reads, and the order is the
// file's order.
func identityGoldenDirs(t *testing.T, root string) []string {
	t.Helper()

	seen := map[string]bool{}
	for _, name := range identityGoldenRoots {
		base := filepath.Join(root, name)
		if _, err := os.Stat(base); err != nil {
			t.Fatalf("%s does not exist; this sweep's roots have moved and it is covering less than it claims", base)
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
			}
			if d.IsDir() {
				if d.Name() == ".terraform" {
					return fs.SkipDir
				}
				return nil
			}
			// The same set internal/configs' loader accepts. A narrower
			// filter here would be a guard that covers less than the thing
			// it guards.
			switch {
			case strings.HasSuffix(path, ".tf"),
				strings.HasSuffix(path, ".tf.json"),
				strings.HasSuffix(path, ".tofu"),
				strings.HasSuffix(path, ".tofu.json"):
				seen[filepath.Dir(path)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %s", base, err)
		}
	}

	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// reportIdentityGoldenDiff says which identity changed and to what.
//
// "golden mismatch" over 375 directories' worth of lines is useless, and a
// raw unified diff of a file this size is not much better. So the comparison
// is keyed on "directory + address" - the identity of the LINE - and reports
// the three cases separately, because they mean different things: a modified
// identity is an alarm, an added one is usually progress, and a removed one
// means something stopped resolving.
func reportIdentityGoldenDiff(t *testing.T, want, got string) {
	t.Helper()

	wantByKey, wantOrder := identityGoldenIndex(want)
	gotByKey, gotOrder := identityGoldenIndex(got)

	var changed, added, removed []string

	for _, k := range gotOrder {
		w, ok := wantByKey[k]
		if !ok {
			added = append(added, fmt.Sprintf("  + %s\n      now %s", k, gotByKey[k]))
			continue
		}
		if w != gotByKey[k] {
			changed = append(changed, fmt.Sprintf("  ! %s\n      was %s\n      now %s", k, w, gotByKey[k]))
		}
	}
	for _, k := range wantOrder {
		if _, ok := gotByKey[k]; !ok {
			removed = append(removed, fmt.Sprintf("  - %s\n      was %s", k, wantByKey[k]))
		}
	}

	if len(changed) == 0 && len(added) == 0 && len(removed) == 0 {
		// Every line matched but the files differ: the header changed, or
		// the ordering did. Both are real and neither is per-line.
		t.Errorf("%s differs but no instance's identity did; the file's header or ordering changed.", identityGoldenPath)
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s is out of date: %d identities changed, %d added, %d removed.\n",
		identityGoldenPath, len(changed), len(added), len(removed))
	b.WriteString("A CHANGED identity is an alarm: a fixture nobody edited now renders a different\n")
	b.WriteString("string into a cloud tag, and no count in this repository moves when that happens.\n")
	b.WriteString("An ADDED one is usually the campaign working - read the value anyway.\n")
	identityGoldenSection(&b, "changed", changed)
	identityGoldenSection(&b, "added", added)
	identityGoldenSection(&b, "removed", removed)
	b.WriteString("\nIf every line above is intended, regenerate:\n")
	b.WriteString("  env -u PWD go test -C \"$(git rev-parse --show-toplevel)\" ./internal/live/check -run TestIdentityGolden -update\n")
	t.Error(b.String())
}

// identityGoldenMaxReported bounds each section of the failure message. The
// full file is on disk; what a reader needs from a test log is enough lines to
// recognise the shape of the change.
const identityGoldenMaxReported = 40

func identityGoldenSection(b *strings.Builder, label string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s (%d):\n", label, len(lines))
	shown := lines
	if len(shown) > identityGoldenMaxReported {
		shown = shown[:identityGoldenMaxReported]
	}
	for _, l := range shown {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if len(shown) < len(lines) {
		fmt.Fprintf(b, "  ... and %d more %s; diff %s for the rest.\n", len(lines)-len(shown), label, identityGoldenPath)
	}
}

// identityGoldenIndex splits the file into "dir<TAB>address" keys and the rest
// of each line, keeping the file's own order for reporting. Comment lines are
// skipped: they are prose about the file, not data in it.
func identityGoldenIndex(s string) (map[string]string, []string) {
	byKey := map[string]string{}
	var order []string
	for _, line := range strings.Split(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		key := fields[0] + "\t" + fields[1]
		if _, dup := byKey[key]; !dup {
			order = append(order, key)
		}
		byKey[key] = fields[2]
	}
	return byKey, order
}
