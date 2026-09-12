// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefulcost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestNeighbourEstateCostAgainstFloci asks how the cost of planning ONE
// estate moves as the number of OTHER ESTATES in the same account grows.
//
// That axis is not the one anything here already measures.
// internal/live/discovery's TestForeignLoadAgainstFloci grows a SINGLE
// foreign estate's resource count beside a held-still owned one, and
// slicing_bench_test.go grows the owned estate itself. Neither separates
// "how many neighbouring objects" from "how many neighbouring estates",
// and those are different questions the moment a sweep leg filters
// client-side: an account of 1,500 foreign objects in one estate and an
// account of 1,500 foreign objects in 500 estates are the same population
// under one grouping and 500 groupings under the other.
//
// Three arms, run against three independent accounts:
//
//	A  the owned estate alone
//	B  the owned estate beside ESTATES_FOREIGN_ESTATES foreign estates,
//	   ESTATES_FOREIGN_PER_ESTATE objects each
//	C  the owned estate beside ONE foreign estate holding the same TOTAL
//	   number of objects B's many estates hold between them
//
// B against A is the growth. C against B is the control that makes this an
// experiment: the two accounts hold the same number of foreign OBJECTS and
// differ only in how many distinct tofu-estate values those objects carry.
// If B and C cost the same, the driver is objects and estate count is free;
// if B costs more than C, estate count is a term of its own.
//
// Every arm is measured for BOTH sides - stock Terraform on its own state
// file and choudoufu on the record store its own apply wrote - because
// "stock is indifferent to its neighbours by construction" is a claim worth
// measuring rather than asserting, and without stock's row the growth has
// no baseline that is known to be flat.
//
// # Why the foreign load is IAM roles
//
// It has to be a type the owned estate's sweep actually looks at, or the
// arms measure nothing and report a beautiful flat line. The terralith
// fixture is IAM-majority (9 roles, 8 policies, 8 inline policies, 8
// instance profiles and 17 attachments at scale 1), and IAM is the one
// service discovery's tagging leg cannot use: taggingAPIUnservedServices in
// internal/live/discovery/tagging.go records that the Resource Groups
// Tagging API never returns IAM resources, probed against real AWS on
// 2026-09-01, so aws_iam_role sweeps through the NATIVE per-type leg -
// ListRoles over the whole account, filtered on this side. That is exactly
// the leg where a neighbouring population could cost something, which is
// why the load is put there rather than on a type whose estate filter the
// server applies for free.
//
// The neighbours are real neighbours: each carries its own tofu-estate and
// tofu-address tag pair, the way another operator's choudoufu estate would.
// They are applied with STOCK terraform, because nothing here needs them to
// be choudoufu-managed - only to be present, tagged, and not ours.
//
//	TF_FLOCI_TEST=1 env -u PWD go test ./internal/live/statefulcost/ \
//	  -run TestNeighbourEstateCostAgainstFloci -v -timeout 240m
//
//	ESTATES_FOREIGN_ESTATES    foreign estates in arm B (default 4)
//	ESTATES_FOREIGN_PER_ESTATE objects in each of them (default 3)
//	ESTATES_OWNED_SCALE        terralith-gen -scale for the owned estate (default 1)
//	ESTATES_ARMS               comma-separated subset of A,B,C (default all three)
//
// It asserts nothing about the numbers. It asserts that each arm's foreign
// load is really in the account it is supposed to be in, at the estate count
// it is supposed to have, and that every plan it timed came back empty -
// because a refused or non-empty plan's cost is not a plan's cost.
func TestNeighbourEstateCostAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "neighbourcost")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, "aws")

	estates := neighbourEnvInt(t, "ESTATES_FOREIGN_ESTATES", 4)
	perEstate := neighbourEnvInt(t, "ESTATES_FOREIGN_PER_ESTATE", 3)
	ownedScale := neighbourEnvInt(t, "ESTATES_OWNED_SCALE", 1)
	foreignObjects := estates * perEstate

	root := flocitest.RepoRoot(t)
	choudoufuBin := flocitest.BuildTofu(t)
	flocitest.PluginCacheDir(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	arms := []neighbourArm{
		{Name: "A-alone", Letter: "a", Estates: 0, PerEstate: 0},
		{Name: "B-many", Letter: "b", Estates: estates, PerEstate: perEstate},
		// One estate holding everything B's many estates hold between them.
		{Name: "C-one-big", Letter: "c", Estates: 1, PerEstate: foreignObjects},
	}
	if only := os.Getenv("ESTATES_ARMS"); only != "" {
		arms = filterArms(t, arms, only)
	}

	var cols []*column
	for _, a := range arms {
		a := a
		t.Run(a.Name, func(t *testing.T) {
			// One emulator per side, started INSIDE the subtest so its
			// cleanup fires when the arm ends: three arms times two sides
			// is six accounts, and holding all six containers at once on a
			// machine that is already running something else is not worth
			// the parallelism nothing here would use.
			//
			// Two accounts per arm rather than one, for the reason
			// steadystate_live_test.go gives: a live plan sweeps its
			// account, so the stock column's own estate would be listed by
			// it and would inflate exactly the number under test.
			cols = append(cols,
				runNeighbourSide(t, root, terraformBin, a, "stock", ownedScale),
				runNeighbourSide(t, root, choudoufuBin, a, "live", ownedScale),
			)
		})
	}

	reportNeighbourArms(t, ownedScale, estates, perEstate, cols)
}

// neighbourArm is one account's foreign population: how many foreign
// estates, and how many objects in each.
type neighbourArm struct {
	Name      string
	Letter    string
	Estates   int
	PerEstate int
}

func (a neighbourArm) objects() int { return a.Estates * a.PerEstate }

// runNeighbourSide stands one arm up for one side in its own account and
// returns that side's timed column. side is "stock" (stock Terraform on its
// own state file) or "live" (choudoufu on the record store its own apply
// wrote).
func runNeighbourSide(t *testing.T, root, bin string, a neighbourArm, side string, ownedScale int) *column {
	t.Helper()

	live := side == "live"
	label := a.Name + "/stock-terraform"
	if live {
		label = a.Name + "/choudoufu-live"
	}

	port := flocitest.StartFloci(t, "cdf-nbr-"+a.Letter+"-"+side)
	raw := flocitest.Endpoint(port)
	proxy := flocitest.NewCountingProxy(t, raw)
	rawEnv := awsEnv(raw)
	t.Logf("%s: emulator %s, counting proxy %s", label, raw, proxy.Endpoint())

	// ── the neighbours ──────────────────────────────────────────────────
	// Applied through the raw endpoint, not the proxy: the proxy is there
	// to count the plans under test, and routing a 1,500-object apply
	// through it buys nothing.
	if a.objects() > 0 {
		foreignPrefix := "fx" + a.Letter + side[:1]
		foreignDir := filepath.Join(t.TempDir(), "foreign")
		writeForeignEstates(t, foreignDir, foreignPrefix, a.Estates, a.PerEstate)
		mustRun(t, foreignDir, rawEnv, terraformBin, "init", "-input=false", "-no-color")
		start := time.Now()
		if out, err := run(t, foreignDir, rawEnv, terraformBin, "apply", "-input=false", "-auto-approve", "-no-color"); err != nil {
			t.Fatalf("%s: applying %d foreign estates x %d objects: %v\n%s", label, a.Estates, a.PerEstate, err, tailOf(out, 40))
		} else {
			t.Logf("%s: foreign apply %s - %d estates x %d objects = %d (%s)",
				label, time.Since(start).Round(time.Second), a.Estates, a.PerEstate, a.objects(), applyLine(out))
		}
		assertForeignLoad(t, label, foreignDir, rawEnv, foreignPrefix, a)
	} else {
		assertNoForeignRoles(t, label, rawEnv, "fx")
		t.Logf("%s: no foreign estates - this is the arm the others are measured against", label)
	}

	// ── the owned estate, applied by the side's own binary ───────────────
	ownedPrefix := "n" + a.Letter + side[:1]
	ownedDir := filepath.Join(t.TempDir(), "owned")
	generate(t, root, ownedDir, ownedScale, ownedPrefix)
	estateName := "nbr-" + a.Letter + "-" + side
	if live {
		addLiveBlock(t, ownedDir, estateName)
	}
	env := awsEnv(proxy.Endpoint())
	mustRun(t, ownedDir, env, bin, "init", "-input=false", "-no-color")
	start := time.Now()
	if out, err := run(t, ownedDir, env, bin, "apply", "-input=false", "-auto-approve", "-no-color"); err != nil {
		t.Fatalf("%s: applying the owned estate at -scale %d: %v\n%s", label, ownedScale, err, tailOf(out, 60))
	} else {
		t.Logf("%s: owned apply %s (%s)", label, time.Since(start).Round(time.Second), applyLine(out))
	}
	if live {
		// Without it this column is the adoption path under another name,
		// exactly as steadystate_live_test.go's own check says.
		records := filepath.Join(ownedDir, ".tofu-records")
		if fi, err := os.Stat(records); err != nil || !fi.IsDir() {
			t.Fatalf("%s: no record store at %s after choudoufu's own apply: %v", label, records, err)
		}
	}

	// Pagination across the three timed runs, logged beside the call
	// counts because it is the one thing that could make a flat call count
	// mean the wrong thing. A whole-account list leg that answers in ONE
	// call however many objects the account holds is either genuinely
	// filtered or an emulator that does not page the way real AWS does, and
	// those two readings are told apart by whether a continuation cursor
	// ever goes out. Zero pages at a foreign population large enough to
	// need them is a fact about this emulator that any claim drawn from
	// these numbers has to carry.
	pagesBefore := proxy.PaginationTotal()
	col := timePlans(t, &column{
		Label: label, Bin: bin, Dir: ownedDir,
		Endpoint: proxy.Endpoint(),
		Args:     []string{"plan", "-input=false", "-no-color"},
	}, proxy)
	t.Logf("%s: %d paginated requests across the three timed runs (%v)",
		label, proxy.PaginationTotal()-pagesBefore, proxy.PaginationCounts())

	// ── still correct, not only still cheap ─────────────────────────────
	assertOwnedPlanCorrect(t, label, ownedDir, env, bin, ownedPrefix, a, live)
	return col
}

// assertOwnedPlanCorrect runs one more plan, untimed and after the timed
// ones so it cannot warm anything they paid for, and reads its body rather
// than its verdict.
//
// "No changes." is already the strongest single statement available about a
// live plan's correctness - every declared instance resolved to a live
// object carrying this estate's marker, or the plan would propose creating
// it - and timePlans fails the run without it. What that verdict cannot
// say is whether a neighbour leaked into the answer, which is the specific
// way this test's own fixture could go wrong: a foreign object adopted,
// offered for adoption, or named in a warning would mean the estate filter
// did not hold, and at 500 neighbours that would be a correctness finding
// well before it was a cost one.
func assertOwnedPlanCorrect(t *testing.T, label, dir string, env []string, bin, ownedPrefix string, a neighbourArm, live bool) {
	t.Helper()

	out, err := run(t, dir, env, bin, "plan", "-input=false", "-no-color")
	if err != nil {
		t.Errorf("%s: the verification plan failed: %v\n%s", label, err, tailOf(out, 40))
		return
	}
	if !strings.Contains(out, "No changes.") {
		t.Errorf("%s: the verification plan is not empty (%s)\n%s", label, planSummary(out), changedResources(out))
		return
	}
	if a.objects() > 0 {
		if idx := strings.Index(out, "fx"+a.Letter); idx >= 0 {
			t.Errorf("%s: a foreign estate is named in the owned estate's own plan output, so the estate filter did not hold:\n%s",
				label, strings.TrimSpace(out[idx:min(idx+400, len(out))]))
		}
	}
	if !live {
		return
	}
	// A positive statement to sit beside the negative ones: the record
	// store this column planned from still holds entries. An empty plan
	// over an empty estate is also an empty plan.
	entries, err := os.ReadDir(filepath.Join(dir, ".tofu-records"))
	if err != nil {
		t.Errorf("%s: reading the record store: %v", label, err)
		return
	}
	if len(entries) == 0 {
		t.Errorf("%s: the record store is empty after an apply and four plans", label)
	}
}

// ── the foreign load ────────────────────────────────────────────────────

// foreignConfig is the whole neighbour population as ONE configuration.
//
// One configuration and one apply, not one per estate: 500 applies is a
// different test with a different bottleneck. Which estate an object belongs
// to is carried by its tofu-estate tag AND encoded in its name, so the two
// can be checked against each other from the account side afterwards
// without trusting either alone.
const foreignConfig = `# Generated by TestNeighbourEstateCostAgainstFloci. Not a fixture; a load.
#
# %[2]d foreign estates of %[3]d objects each = %[4]d objects, each carrying
# its own tofu-estate/tofu-address pair, the way another operator's
# choudoufu estate would. Applied with stock terraform: nothing here needs
# them to be choudoufu-managed, only to be present, tagged, and not ours.

resource "aws_iam_role" "neighbour" {
  count = %[4]d

  name = format("%[1]s-%%05d-%%d", floor(count.index / %[3]d), count.index %% %[3]d)

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = {
    tofu-estate  = format("%[1]s-e%%05d", floor(count.index / %[3]d))
    tofu-address = format("aws_iam_role.n%%d", count.index %% %[3]d)
  }
}
`

const foreignVersions = `terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }
}

` + flociProviderBlock

func writeForeignEstates(t *testing.T, dir, prefix string, estates, perEstate int) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	main := fmt.Sprintf(foreignConfig, prefix, estates, perEstate, estates*perEstate)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o600); err != nil {
		t.Fatalf("writing the foreign configuration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(foreignVersions), 0o600); err != nil {
		t.Fatalf("writing the foreign provider wiring: %v", err)
	}
}

// assertForeignLoad proves the neighbours are in the ACCOUNT, at the estate
// count this arm asked for, before anything is timed against them. A
// neighbour-load measurement whose load silently failed to materialise
// would report a beautiful flat line and be believed.
//
// Three independent statements, because no one of them is enough:
//
//  1. iam ListRoles, read from the account with the AWS CLI and not through
//     terraform at all: how many roles carrying this arm's prefix exist,
//     and how many distinct estate segments their NAMES carry.
//  2. stock terraform's own plan over the foreign configuration, which
//     refreshes every one of those roles and compares the tofu-estate and
//     tofu-address tags it reads back against the ones declared. An empty
//     plan is an exhaustive, per-object statement that the TAGS are what
//     this test says they are - the thing ListRoles cannot answer, because
//     neither it nor the Resource Groups Tagging API returns an IAM role's
//     tags (probed 2026-09-12 against the pinned image, and against real
//     AWS on 2026-09-01 for the tagging API - issue #692).
//  3. a direct ListRoleTags read of the first, middle and last role, which
//     binds the name encoding (1) counts to the tag values (2) verifies,
//     so a configuration that named its roles one way and tagged them
//     another could not satisfy both.
func assertForeignLoad(t *testing.T, label, dir string, env []string, prefix string, a neighbourArm) {
	t.Helper()

	names := listRoleNames(t, env, prefix)
	if len(names) != a.objects() {
		t.Fatalf("%s: the account holds %d roles named %s-*, want %d - the foreign load is not what this arm is measuring",
			label, len(names), prefix, a.objects())
	}
	segments := map[string]bool{}
	for _, n := range names {
		parts := strings.Split(strings.TrimPrefix(n, prefix+"-"), "-")
		if len(parts) != 2 {
			t.Fatalf("%s: role %q does not carry this test's own name encoding", label, n)
		}
		segments[parts[0]] = true
	}
	if len(segments) != a.Estates {
		t.Fatalf("%s: the account's foreign roles carry %d distinct estate segments in their names, want %d",
			label, len(segments), a.Estates)
	}

	if out, err := run(t, dir, env, terraformBin, "plan", "-input=false", "-no-color"); err != nil {
		t.Fatalf("%s: refreshing the foreign load: %v\n%s", label, err, tailOf(out, 40))
	} else if !strings.Contains(out, "No changes.") {
		t.Fatalf("%s: the foreign load does not read back as applied (%s), so its tofu-estate tags are not established:\n%s",
			label, planSummary(out), changedResources(out))
	}

	distinct := map[string]bool{}
	for _, i := range []int{0, len(names) / 2, len(names) - 1} {
		name := fmt.Sprintf("%s-%05d-%d", prefix, i/a.PerEstate, i%a.PerEstate)
		tags := roleTags(t, env, name)
		want := fmt.Sprintf("%s-e%05d", prefix, i/a.PerEstate)
		if tags["tofu-estate"] != want {
			t.Fatalf("%s: role %s carries tofu-estate=%q, want %q - the name encoding and the tags disagree",
				label, name, tags["tofu-estate"], want)
		}
		distinct[tags["tofu-estate"]] = true
	}
	t.Logf("%s: foreign load verified in the account - %d roles, %d distinct estates by name, tags read back on %d of them (%v)",
		label, len(names), len(segments), len(distinct), sortedKeys(distinct))
}

// assertNoForeignRoles is arm A's half of the same statement: the account
// really is empty of neighbours. An arm A run against a dirty account would
// be the baseline every other number is divided by, quietly wrong.
func assertNoForeignRoles(t *testing.T, label string, env []string, prefix string) {
	t.Helper()

	if names := listRoleNames(t, env, prefix); len(names) != 0 {
		t.Fatalf("%s: the account already holds %d roles named %s* before anything was applied", label, len(names), prefix)
	}
}

// listRoleNames reads the account's IAM roles with the AWS CLI and returns
// the ones whose name starts with prefix. The CLI paginates on its own, so
// this is one command whatever the population.
func listRoleNames(t *testing.T, env []string, prefix string) []string {
	t.Helper()

	out, err := run(t, t.TempDir(), env, "aws", "iam", "list-roles", "--output", "json")
	if err != nil {
		t.Fatalf("aws iam list-roles: %v\n%s", err, tailOf(out, 20))
	}
	var parsed struct {
		Roles []struct {
			RoleName string `json:"RoleName"`
		} `json:"Roles"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("decoding aws iam list-roles: %v\n%s", err, tailOf(out, 20))
	}
	var names []string
	for _, r := range parsed.Roles {
		if strings.HasPrefix(r.RoleName, prefix) {
			names = append(names, r.RoleName)
		}
	}
	return names
}

// roleTags reads one role's tags straight from the account.
func roleTags(t *testing.T, env []string, name string) map[string]string {
	t.Helper()

	out, err := run(t, t.TempDir(), env, "aws", "iam", "list-role-tags", "--role-name", name, "--output", "json")
	if err != nil {
		t.Fatalf("aws iam list-role-tags --role-name %s: %v\n%s", name, err, tailOf(out, 20))
	}
	var parsed struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("decoding aws iam list-role-tags: %v\n%s", err, tailOf(out, 20))
	}
	tags := map[string]string{}
	for _, tag := range parsed.Tags {
		tags[tag.Key] = tag.Value
	}
	return tags
}

// ── reporting ───────────────────────────────────────────────────────────

func reportNeighbourArms(t *testing.T, ownedScale, estates, perEstate int, cols []*column) {
	t.Helper()

	t.Logf("")
	t.Logf("NEIGHBOUR-ESTATE COST REPORT owned_scale=%d foreign_estates=%d per_estate=%d foreign_objects=%d",
		ownedScale, estates, perEstate, estates*perEstate)
	t.Logf("emulator=%s commit=%s", flocitest.Image(), flocitest.HeadCommit(t))
	t.Logf("A = the owned estate alone; B = %d foreign estates x %d objects; C = 1 foreign estate x %d objects",
		estates, perEstate, estates*perEstate)
	t.Logf("")
	t.Logf("%-26s %-26s %-22s %s", "arm/side", "seconds (3 runs)", "API calls (3 runs)", "verdicts")
	for _, c := range cols {
		t.Logf("%-26s %-26s %-22s %s", c.Label, joinFloats(c.Seconds), joinInts(c.Calls), strings.Join(c.Verdicts, ","))
	}
	for _, c := range cols {
		t.Logf("")
		t.Logf("%s: API calls by action, third run", c.Label)
		for _, k := range sortedIntKeys(c.ByAPILast) {
			t.Logf("  %-46s %d", k, c.ByAPILast[k])
		}
	}
}

// ── small helpers ───────────────────────────────────────────────────────

func neighbourEnvInt(t *testing.T, name string, def int) int {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q is not a positive integer", name, v)
	}
	return n
}

func filterArms(t *testing.T, arms []neighbourArm, only string) []neighbourArm {
	t.Helper()

	want := map[string]bool{}
	for _, part := range strings.Split(only, ",") {
		want[strings.ToLower(strings.TrimSpace(part))] = true
	}
	var kept []neighbourArm
	for _, a := range arms {
		if want[a.Letter] {
			kept = append(kept, a)
		}
	}
	if len(kept) == 0 {
		t.Fatalf("ESTATES_ARMS=%q selected no arm; the arms are A, B and C", only)
	}
	return kept
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
