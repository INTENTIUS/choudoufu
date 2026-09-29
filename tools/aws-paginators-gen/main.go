// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command aws-paginators-gen vendors botocore's paginator data into
// live/aws-paginating-operations.json: for every botocore service
// directory, the API operations botocore declares a paginator for.
//
// Why derived rather than maintained (issue #1214's ruling). The AWS CLI
// applies --query to each page of an auto-paginated listing before merging
// them, so a --query that reduces a list to a scalar reads one page at a
// time - issue #1042 ("100 100 100 35" where 335 was meant) and issue
// #1206 (an arn plus fifteen literal "None"s, which surfaced two layers
// later as an EMPTY ownership marker). Deciding whether a call site is in
// that class needs one fact, "does this operation paginate", and botocore
// ships it: it is the same data the AWS CLI itself consults to decide
// whether to page. A hand-maintained list would be wrong in both
// directions and would rot with nothing to say so.
//
// Why vendored rather than imported at test time. CI must not depend on a
// Python package being installed, and a guard that skips itself when the
// package is absent is permanently green - the failure CLAUDE.md names by
// hand. So the snapshot is committed, the guard reads only the committed
// file, and this tool's -check mode is what a human (or a machine that
// does have botocore) runs to prove the file is still what botocore says.
//
// Usage:
//
//	go run ./tools/aws-paginators-gen              # rewrite the snapshot
//	go run ./tools/aws-paginators-gen -check       # fail on drift, write nothing
//	go run ./tools/aws-paginators-gen -baseline    # rewrite the call-site baseline
//
// -check is the check. It is deliberately NOT a `go test`: a test would
// have to skip where botocore is absent, and a skipping guard is the thing
// this design refuses. `just aws-paginators-check` is the one command.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	residue "github.com/intentius/choudoufu/live"
)

func main() {
	var (
		check       = flag.Bool("check", false, "compare the committed snapshot against botocore and exit non-zero on any difference")
		baseline    = flag.Bool("baseline", false, "rewrite live/aws-page-query-baseline.json from the crossing scripts as they stand")
		audit       = flag.Bool("audit", false, "print every --query call site with its verdict, grouped by operation; reads only the committed snapshot")
		dataDir     = flag.String("botocore-data", "", "botocore's data directory (default: ask python3 where botocore is)")
		repoRootArg = flag.String("root", ".", "repository root")
	)
	flag.Parse()

	root, err := filepath.Abs(*repoRootArg)
	if err != nil {
		fail(err)
	}

	if *baseline {
		if err := writeBaseline(root); err != nil {
			fail(err)
		}
		return
	}

	if *audit {
		if err := printAudit(root); err != nil {
			fail(err)
		}
		return
	}

	snap, err := scan(*dataDir)
	if err != nil {
		fail(err)
	}
	version := snap.BotocoreVersion
	snap.Digest = snap.ContentDigest()
	want, err := snap.Marshal()
	if err != nil {
		fail(err)
	}

	out := filepath.Join(root, residue.PaginatingOperationsPath)
	if *check {
		got, err := os.ReadFile(out)
		if err != nil {
			fail(err)
		}
		if diff := describeDrift(got, snap); diff != "" {
			fmt.Fprintf(os.Stderr, "%s is stale against botocore %s:\n%s\nrun: go run ./tools/aws-paginators-gen\n", residue.PaginatingOperationsPath, version, diff)
			os.Exit(1)
		}
		fmt.Printf("%s matches botocore %s (%d services, %d operations)\n", residue.PaginatingOperationsPath, version, len(snap.Services), countOps(snap))
		return
	}

	if err := os.WriteFile(out, want, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s: botocore %s, %d services, %d operations\n", residue.PaginatingOperationsPath, version, len(snap.Services), countOps(snap))
}

func countOps(s *residue.PaginatingOperations) int {
	n := 0
	for _, ops := range s.Services {
		n += len(ops)
	}
	return n
}

// extractScript is the whole extraction, run inside python so that the
// CLI spelling of each operation comes from botocore's own xform_name -
// literally the function awscli uses to name its subcommands - rather than
// from a Go reimplementation of it.
//
// That is not a stylistic preference. The first version of this tool
// PascalCased the CLI name in Go, which turns "describe-db-instances" into
// "DescribeDbInstances" while the API operation is "DescribeDBInstances".
// All 14 of this tree's `rds describe-db-instances --query '...[0]'` call
// sites were classified as safe by that one-letter difference - a false
// negative in a guard whose whole purpose is not to have any. Acronym
// casing is exactly the kind of rule that looks derivable and is not.
//
// Both paginators-1.json and paginators-1.sdk-extras.json are read: the
// sdk-extras files are overlays botocore's own loader merges in, so the CLI
// pages for those operations too, and leaving them out would make the
// snapshot narrower than the tool it describes. A service directory with
// several API versions contributes the UNION of their paginators - which
// version a shell call site talks to is not visible, and a union can only
// over-flag, the direction #1214's ruling chose everywhere else.
const extractScript = `
import json, os, sys
import botocore
from botocore import xform_name

data = sys.argv[1] if len(sys.argv) > 1 and sys.argv[1] else os.path.join(os.path.dirname(botocore.__file__), "data")
out = {"botocore_version": botocore.__version__, "service_dirs": 0, "paginator_files": 0, "services": {}}
for svc in sorted(os.listdir(data)):
    sdir = os.path.join(data, svc)
    if not os.path.isdir(sdir):
        continue
    out["service_dirs"] += 1
    ops = {}
    for ver in sorted(os.listdir(sdir)):
        vdir = os.path.join(sdir, ver)
        if not os.path.isdir(vdir):
            continue
        for name in ("paginators-1.json", "paginators-1.sdk-extras.json"):
            path = os.path.join(vdir, name)
            if not os.path.exists(path):
                continue
            out["paginator_files"] += 1
            with open(path) as f:
                doc = json.load(f)
            for op in doc.get("pagination", {}):
                ops[op] = xform_name(op, "-")
    if ops:
        out["services"][svc] = ops
json.dump(out, sys.stdout)
`

// scan runs extractScript and parses what it prints.
func scan(dataDir string) (*residue.PaginatingOperations, error) {
	cmd := exec.Command("python3", "-c", extractScript, dataDir)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("extracting botocore's paginator data (%v): %s\ninstall botocore (pip install botocore) or pass -botocore-data", err, strings.TrimSpace(errb.String()))
	}
	var snap residue.PaginatingOperations
	if err := json.Unmarshal(out.Bytes(), &snap); err != nil {
		return nil, fmt.Errorf("parsing the extraction: %w", err)
	}
	if len(snap.Services) == 0 {
		return nil, fmt.Errorf("no paginator data found - a snapshot with no services would classify every call as non-paginating")
	}
	snap.Generated = "go run ./tools/aws-paginators-gen  -  DO NOT EDIT  -  " + time.Now().UTC().Format("2006-01-02")
	return &snap, nil
}

// describeDrift compares the committed bytes with the freshly generated
// snapshot and reports what moved, ignoring only the _generated stamp (a
// date, which changes on every run and means nothing). An empty string
// means they agree - byte for byte, not merely in content: the committed
// file has exactly one canonical rendering, and a reformatted copy has to
// fail here as well as in the offline test.
func describeDrift(got []byte, fresh *residue.PaginatingOperations) string {
	var g residue.PaginatingOperations
	if err := json.Unmarshal(got, &g); err != nil {
		return fmt.Sprintf("  committed file does not parse: %v", err)
	}
	w := *fresh
	var lines []string
	if g.BotocoreVersion != w.BotocoreVersion {
		lines = append(lines, fmt.Sprintf("  botocore_version: committed %s, installed %s", g.BotocoreVersion, w.BotocoreVersion))
	}
	for svc, ops := range w.Services {
		old, ok := g.Services[svc]
		if !ok {
			lines = append(lines, fmt.Sprintf("  + service %s (%d operations)", svc, len(ops)))
			continue
		}
		for _, op := range diffSets(ops, old) {
			lines = append(lines, fmt.Sprintf("  + %s %s", svc, op))
		}
		for _, op := range diffSets(old, ops) {
			lines = append(lines, fmt.Sprintf("  - %s %s", svc, op))
		}
	}
	for svc := range g.Services {
		if _, ok := w.Services[svc]; !ok {
			lines = append(lines, fmt.Sprintf("  - service %s", svc))
		}
	}
	// A pure formatting difference with no content difference still has to
	// fail, so the last comparison is over bytes, with the fresh snapshot
	// re-rendered under the COMMITTED file's own _generated stamp so the
	// date cannot be the thing that differs.
	stamped := *fresh
	stamped.Generated = g.Generated
	if canonical, err := stamped.Marshal(); err != nil {
		lines = append(lines, fmt.Sprintf("  could not render the fresh snapshot: %v", err))
	} else if !bytes.Equal(got, canonical) && len(lines) == 0 {
		lines = append(lines, "  content agrees but the committed file is not in canonical form (two-space indent, sorted keys, one trailing newline) - regenerate rather than hand-editing")
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// diffSets returns the operation names in a whose (name, CLI spelling)
// pair is not in b - so a change to either half shows up, not just an
// added or removed operation.
func diffSets(a, b map[string]string) []string {
	var out []string
	for api, cli := range a {
		if b[api] != cli {
			out = append(out, api)
		}
	}
	sort.Strings(out)
	return out
}

// writeBaseline re-measures every crossing script and rewrites
// live/aws-page-query-baseline.json. See live/awspagequery_baseline.go for
// what the baseline is and why the guard is a per-script ratchet rather
// than a flat ban.
func writeBaseline(root string) error {
	snap, err := residue.LoadPaginatingOperations(filepath.Join(root, residue.PaginatingOperationsPath))
	if err != nil {
		return err
	}
	counts, err := residue.MeasurePageQueryBaseline(filepath.Join(root, "live"), snap)
	if err != nil {
		return err
	}
	data, err := residue.MarshalPageQueryBaseline(counts)
	if err != nil {
		return err
	}
	out := filepath.Join(root, residue.PageQueryBaselinePath)
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	fmt.Printf("wrote %s: %d script(s) carrying %d call site(s)\n", residue.PageQueryBaselinePath, len(counts), total)
	return nil
}

// printAudit is #1214's item 2: the existing `| [0]` population read
// against the derived list rather than assumed safe. It prints every
// --query call site in the crossing scripts with its verdict, grouped by
// (service, operation), and separates the `| [0]` idiom the issue named
// from the rest so the two questions can be answered independently.
//
// It reads only the committed snapshot, so it runs on a machine with no
// botocore and produces the same numbers the guard enforces.
func printAudit(root string) error {
	liveDir := filepath.Join(root, "live")
	snap, err := residue.LoadPaginatingOperations(filepath.Join(root, residue.PaginatingOperationsPath))
	if err != nil {
		return err
	}
	sources, err := residue.CrossingScriptSources(liveDir)
	if err != nil {
		return err
	}
	type verdict struct {
		flagged, safe int
		pages         bool
		known         bool
	}
	byOp := map[string]*verdict{}
	var scripts []string
	for rel := range sources {
		scripts = append(scripts, rel)
	}
	sort.Strings(scripts)

	totalQueries, bracketZero, bracketZeroFlagged, unattributed := 0, 0, 0, 0
	for _, rel := range scripts {
		uses, _ := residue.AnalyzeAWSPageQueries(sources[rel])
		for _, u := range uses {
			totalQueries++
			isBracketZero := strings.Contains(u.Query, "[0]")
			if isBracketZero {
				bracketZero++
			}
			if u.Service == "" {
				unattributed++
				continue
			}
			key := u.Service + " " + u.Operation
			v := byOp[key]
			if v == nil {
				pages, err := snap.Paginates(u.Service, u.Operation)
				if err != nil {
					return err
				}
				v = &verdict{pages: pages, known: true}
				byOp[key] = v
			}
			if u.Reducing && v.pages {
				v.flagged++
				if isBracketZero {
					bracketZeroFlagged++
				}
			} else {
				v.safe++
			}
		}
	}

	var keys []string
	for k := range byOp {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if byOp[keys[i]].flagged != byOp[keys[j]].flagged {
			return byOp[keys[i]].flagged > byOp[keys[j]].flagged
		}
		return keys[i] < keys[j]
	})
	fmt.Printf("%-52s %8s %8s %s\n", "aws <service> <operation>", "in class", "out", "paginates")
	for _, k := range keys {
		v := byOp[k]
		fmt.Printf("%-52s %8d %8d %v\n", k, v.flagged, v.safe, v.pages)
	}
	fmt.Printf("\n%d --query call site(s) across %d script(s)\n", totalQueries, len(scripts))
	fmt.Printf("%d carry the `[0]` idiom #1214 names; %d of those are on a paginating operation\n", bracketZero, bracketZeroFlagged)
	fmt.Printf("%d --query call site(s) could not be attributed to an `aws <service> <operation>` pair\n", unattributed)
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "aws-paginators-gen:", err)
	os.Exit(1)
}
