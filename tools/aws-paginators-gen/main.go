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

	dir := *dataDir
	version := ""
	if dir == "" {
		dir, version, err = locateBotocore()
		if err != nil {
			fail(err)
		}
	}

	snap, err := scan(dir, version)
	if err != nil {
		fail(err)
	}
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
		if diff := describeDrift(got, want); diff != "" {
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

// locateBotocore asks python3 where botocore lives and which release it is.
// Shelling out rather than hardcoding a path keeps the tool usable on a
// machine whose site-packages sits somewhere else.
func locateBotocore() (dir, version string, err error) {
	cmd := exec.Command("python3", "-c", "import botocore, os; print(os.path.dirname(botocore.__file__)); print(botocore.__version__)")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("python3 could not import botocore (%v): %s\ninstall it (pip install botocore) or pass -botocore-data", err, strings.TrimSpace(errb.String()))
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("unexpected output locating botocore: %q", out.String())
	}
	return filepath.Join(lines[0], "data"), lines[1], nil
}

// scan walks <data>/<service>/<api-version>/paginators-1*.json.
//
// Both paginators-1.json and paginators-1.sdk-extras.json are read: the
// sdk-extras files are overlays botocore's own loader merges in, so the
// CLI pages for those operations too, and leaving them out would make the
// snapshot narrower than the tool it describes.
//
// A service directory with several API versions contributes the UNION of
// their paginators. The CLI talks to one version, but which one is not
// visible at a shell call site, and a union can only over-flag - the
// direction #1214's ruling chose everywhere else.
func scan(dataDir, version string) (*residue.PaginatingOperations, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, fmt.Errorf("reading botocore data directory %s: %w", dataDir, err)
	}
	snap := &residue.PaginatingOperations{
		Generated:       "go run ./tools/aws-paginators-gen  -  DO NOT EDIT  -  " + time.Now().UTC().Format("2006-01-02"),
		BotocoreVersion: version,
		Services:        map[string][]string{},
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		snap.ServiceDirs++
		svc := e.Name()
		versions, err := os.ReadDir(filepath.Join(dataDir, svc))
		if err != nil {
			return nil, err
		}
		ops := map[string]bool{}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			for _, name := range []string{"paginators-1.json", "paginators-1.sdk-extras.json"} {
				p := filepath.Join(dataDir, svc, v.Name(), name)
				data, err := os.ReadFile(p)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return nil, err
				}
				snap.PaginatorFiles++
				var doc struct {
					Pagination map[string]json.RawMessage `json:"pagination"`
				}
				if err := json.Unmarshal(data, &doc); err != nil {
					return nil, fmt.Errorf("%s: %w", p, err)
				}
				for op := range doc.Pagination {
					ops[op] = true
				}
			}
		}
		if len(ops) == 0 {
			continue
		}
		list := make([]string, 0, len(ops))
		for op := range ops {
			list = append(list, op)
		}
		sort.Strings(list)
		snap.Services[svc] = list
	}
	if len(snap.Services) == 0 {
		return nil, fmt.Errorf("no paginator data under %s - a snapshot with no services would classify every call as non-paginating", dataDir)
	}
	return snap, nil
}

// describeDrift compares the committed bytes with the freshly generated
// ones and reports what moved, ignoring only the _generated stamp (a date,
// which changes on every run and means nothing). An empty string means
// they agree.
func describeDrift(got, want []byte) string {
	var g, w residue.PaginatingOperations
	if err := json.Unmarshal(got, &g); err != nil {
		return fmt.Sprintf("  committed file does not parse: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		return fmt.Sprintf("  generated snapshot does not parse: %v", err)
	}
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
	// fail: the committed file has exactly one canonical rendering.
	if len(lines) == 0 && !bytes.Equal(stripGenerated(got), stripGenerated(want)) {
		lines = append(lines, "  content agrees but the committed file is not in canonical form (two-space indent, sorted keys, trailing newline)")
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func stripGenerated(b []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return b
	}
	delete(m, "_generated")
	out, _ := json.MarshalIndent(m, "", "  ")
	return out
}

func diffSets(a, b []string) []string {
	in := map[string]bool{}
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "aws-paginators-gen:", err)
	os.Exit(1)
}
