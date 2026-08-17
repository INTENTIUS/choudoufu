// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

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
// real ID is invisible to this file in the same way: 550 of the 1320
// instances it pins are NEEDS_DISCOVERY with nothing in the value column. It
// covers the 658 that are CONCRETE and the 95 whose formula is symbolic.
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
// It also moves when tools/estate-gen rewrites live/e2e/estates, because
// those .tf files are in the sweep. That is wanted: a regenerated cohort
// whose rendered identities changed is a change to what the e2e run will put
// into a cloud tag, and nothing else reports it.

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

// TestIdentityGolden pins the rendered identity of every managed resource
// instance the in-repo fixtures resolve.
//
// Regenerate with:
//
//	env -u PWD go test -C "$(git rev-parse --show-toplevel)" ./internal/live/check -run TestIdentityGolden -update
//
// then READ the diff. A modified line is an alarm; an added line is progress
// whose rendered value you should still look at.
func TestIdentityGolden(t *testing.T) {
	root := flocitest.RepoRoot(t)
	dirs := identityGoldenDirs(t, root)
	if len(dirs) < 300 {
		t.Fatalf("found only %d configuration directories under %v; the walk is not reaching the tree it is supposed to cover, so a green result here proves nothing",
			len(dirs), identityGoldenRoots)
	}

	got := renderIdentityGolden(t, root, dirs)

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

// renderIdentityGolden produces the whole file: one line per resolved
// instance, ordered by directory then by address, both of which are sorted
// strings rather than map iterations.
func renderIdentityGolden(t *testing.T, root string, dirs []string) string {
	t.Helper()

	var buf strings.Builder
	buf.WriteString("# internal/live/check/testdata/identity-golden.txt\n")
	buf.WriteString("#\n")
	buf.WriteString("# Rendered identity of every managed resource instance the in-repo\n")
	buf.WriteString("# fixtures resolve, analyzed without provider schemas.\n")
	buf.WriteString("#\n")
	buf.WriteString("# Generated by TestIdentityGolden -update. Do not hand-edit; regenerate\n")
	buf.WriteString("# and read the diff. A MODIFIED line is an alarm - a fixture nobody\n")
	buf.WriteString("# touched now renders a different identity. An ADDED line is the\n")
	buf.WriteString("# campaign working, and its value is still worth reading.\n")
	buf.WriteString("#\n")
	buf.WriteString("# dir <TAB> address <TAB> class <TAB> rendered identity <TAB> identity attributes\n")

	var instances int
	for _, dir := range dirs {
		report, panicked, stack := identityGoldenAnalyze(t.Context(), dir)
		if panicked != "" {
			// Not this test's job to assert on, but silently emitting zero
			// lines for a crashing directory would make the golden shrink
			// and read as a deletion rather than a crash.
			t.Errorf("%s panicked during analysis: %s\n%s", rel(root, dir), panicked, stack)
			continue
		}
		if !report.Readable() {
			continue
		}
		rd := rel(root, dir)
		for _, res := range report.Identities {
			buf.WriteString(rd)
			buf.WriteByte('\t')
			buf.WriteString(res.Addr.String())
			buf.WriteByte('\t')
			buf.WriteString(string(res.Class))
			buf.WriteByte('\t')
			buf.WriteString(scrubIdentityGolden(root, renderedIdentity(res)))
			buf.WriteByte('\t')
			buf.WriteString(scrubIdentityGolden(root, renderedIdentityAttrs(res)))
			buf.WriteByte('\n')
			instances++
		}
	}
	t.Logf("swept %d configuration directories under %v; %d instances resolved", len(dirs), identityGoldenRoots, instances)
	if instances == 0 {
		t.Fatal("no instance resolved anywhere in the tree; the sweep is broken rather than the fixtures being empty")
	}
	return buf.String()
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
