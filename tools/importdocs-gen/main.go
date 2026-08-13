// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// importdocs-gen generates live/import-grammar.json, one row per TF
// resource type parsed from the provider's own Import documentation
// (issue #52, dispatched from #55: the source that kills both the 114
// hand-authored composite separators and the manual "rule-1" check the
// Lambda pilot paid by hand - see internal/live/identity/table.go's
// "Registry-ratified" comment for what that check found by hand,
// aws_lambda_alias and aws_lambda_layer_version_permission).
//
// For every TF type in live/survey-full.json's whole-provider roster, it
// fetches website/docs/r/<name>.html.markdown at the pinned provider
// release tag (GitHub raw content, hashicorp/terraform-provider-aws,
// v6.58.0 - the same release tools/survey-gen surveys), caching each file
// on disk the way tools/registry-gen caches its zip, so a full ~1400-file
// sweep costs the network exactly once. A 404 (a TF-side alias documented
// under a different canonical name, like aws_alb under lb.html.markdown)
// is cached and counted, not an error.
//
// Each doc's "## Import" section is parsed for: the literal example
// import ID a `terraform import` command or import block shows, the
// single character that joins a composite ID's segments (only when the
// doc states or shows it unambiguously), and whether the ID is built from
// configuration arguments (cross-checked against the doc's own Argument
// Reference names) versus a server-assigned opaque value. See parse.go's
// classifyGrammar for the two signals this draws on and how they combine.
// Conservative by design: composed_of_arguments and separator are null
// whenever the doc does not resolve them with confidence - a wrong
// separator is worse than an admitted unknown.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/importdocs-gen
//
// It needs network for the first fetch of each doc (or a warm cache under
// the OS user cache directory). -limit restricts the sweep to the first N
// types in the roster, for a fast dev/test loop:
//
//	go run ./tools/importdocs-gen -limit 50
//
// -accept stamps the artifact header's accepted field with today's date,
// the same vocabulary and reasoning tools/survey-gen's own -accept flag
// documents (issue #37, increment 1): omitting it leaves the field unset,
// so an unreviewed regeneration shows up in the diff as the accepted date
// disappearing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// rosterJSONRel is the whole-provider TF type roster this tool sweeps.
// importGrammarJSONRel is where the generated artifact is committed.
const (
	rosterJSONRel        = "live/survey-full.json"
	importGrammarJSONRel = "live/import-grammar.json"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick every other tools/*-gen's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/importdocs-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	limit := flag.Int("limit", 0, "sweep only the first N types in the roster (0 = every type); for a fast dev/test loop")
	accept := flag.Bool("accept", false, "stamp the artifact header's accepted field with today's date (tools/survey-gen's -accept vocabulary)")
	cacheDirOverride := flag.String("cache-dir", "", "use this directory as the doc cache instead of the OS user cache directory")
	flag.Parse()

	if err := run(*limit, *accept, *cacheDirOverride, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "importdocs-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(limit int, accept bool, cacheDirOverride string, log *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	roster, err := loadRoster(filepath.Join(root, rosterJSONRel))
	if err != nil {
		return fmt.Errorf("reading the roster from %s: %w", rosterJSONRel, err)
	}
	if limit > 0 && limit < len(roster) {
		roster = roster[:limit]
	}
	fmt.Fprintf(log, "importdocs-gen: sweeping %d types\n", len(roster))

	cacheDir := cacheDirOverride
	if cacheDir == "" {
		cacheDir, err = defaultCacheDir()
		if err != nil {
			return fmt.Errorf("resolving the doc cache directory: %w", err)
		}
	}

	rows, docsFound, docsMissing, err := sweep(context.Background(), cacheDir, roster, httpFetch)
	if err != nil {
		return err
	}

	art := buildArtifact(rows, len(roster), docsFound, docsMissing)
	if accept {
		art.Accepted = time.Now().UTC().Format("2006-01-02")
	}

	data, err := art.marshal()
	if err != nil {
		return err
	}
	out := filepath.Join(root, importGrammarJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(log,
		"importdocs-gen: wrote %s (%d types considered, %d docs found, %d docs missing, %d rows: %d composed, %d opaque, %d unsure; %d separators known, %d unsure)%s\n",
		importGrammarJSONRel, art.Counts.TypesConsidered, art.Counts.DocsFound, art.Counts.DocsMissing, art.Counts.Rows,
		art.Counts.ComposedTrue, art.Counts.ComposedFalse, art.Counts.ComposedUnsure,
		art.Counts.SeparatorKnown, art.Counts.SeparatorUnsure, acceptedSuffix(art.Accepted))
	return nil
}

// sweep fetches and parses every type in roster, in order, returning the
// rows produced plus the docs-found/docs-missing split. A fetch error
// (transport failure, unexpected status) aborts the whole sweep - unlike a
// 404 or a doc with no Import section, which are expected outcomes this
// function tallies and continues past.
func sweep(ctx context.Context, cacheDir string, roster []string, fetch fetcher) (rows []Row, docsFound, docsMissing int, err error) {
	for _, tfType := range roster {
		data, found, err := acquireDoc(ctx, cacheDir, tfType, fetch)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%s: %w", tfType, err)
		}
		if !found {
			docsMissing++
			continue
		}
		docsFound++
		if row, ok := buildRow(tfType, string(data)); ok {
			rows = append(rows, row)
		}
	}
	return rows, docsFound, docsMissing, nil
}

func acceptedSuffix(accepted string) string {
	if accepted == "" {
		return ""
	}
	return ", accepted " + accepted
}
