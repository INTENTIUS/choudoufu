// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// survey-gen generates live/survey.json and live/survey-full.json, the
// provider's own per-type signals (issue #25, increments 1 and 2; #41).
//
// It boots the pinned AWS provider release the same way the gated test tier
// does - a temp working directory requiring the provider, `terraform init`
// to download it, go-plugin to launch the binary - and reads one
// GetProviderSchema response. That response carries the raw signals the
// survey records per type (a top-level tags argument, a native list
// resource, a resource identity schema) plus the identity attribute
// composition; one ImportResourceState probe per type adds whether the
// provider has a classic Importer at all. classify.go records those facts
// and identity.Report's schema-only admission verdict, and nothing else:
// the seven-token "path" classification it used to derive from them, and
// live/SURVEY.md that carried the same taxonomy by hand, were retired under
// #696 in favour of the readiness tiers (tools/readiness-gen).
//
// The curated roster live/survey.json covers is hand-owned
// (tools/survey-gen/roster.txt, adjudicated under #100 item 1: which 68
// types make up "the top set" is editorial, not schema).
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/survey-gen
//
// It needs network for the provider download (or a warm
// TF_PLUGIN_CACHE_DIR) and a terraform binary on PATH (-init-bin overrides).
//
// A -all flag surveys the provider's entire resource-type roster instead
// of the curated 68, and writes the result to a second artifact,
// live/survey-full.json (issue #41). It always still writes survey.json
// from the curated roster, unchanged, so survey.json never depends on
// whether -all was passed:
//
//	go run ./tools/survey-gen -all
//
// A second mode rewrites every derived span this tool owns, each between
// survey-gen marker comments, from committed artifacts and the compiled
// admission table, with no provider and no network: live/LIMITATIONS.md's
// residue-roster spans and its untaggable-admitted entry (issue #54);
// live/MARKERS.md's estate-grant governance spans, which say how much of the
// admitted table an IAM condition on a marker tag reaches and name what it
// cannot; and live/COVERAGE.md's admitted-set count and type enumeration
// (issue #54):
//
//	go run ./tools/survey-gen -render
//
// See render.go, residue_render.go, untaggable_render.go,
// governance_render.go and contract_render.go.
//
// A -accept flag stamps the written artifact's header with today's date
// (the "accepted" field), the only way that field is ever written -
// reusing tools/registry-gen/pin.go's SpecPin.Accepted vocabulary so a
// provider bump reads as a decision rather than one opaque regeneration
// replacing another (issue #37, increment 1):
//
//	go run ./tools/survey-gen -accept
//
// Regenerating without it leaves the field out of the artifact, so an
// unreviewed bump shows up in the diff as the accepted date disappearing.
// It combines with -all: `go run ./tools/survey-gen -accept -all` stamps
// both live/survey.json and live/survey-full.json with the same date.
package main

import (
	"github.com/intentius/choudoufu/internal/live/pins"

	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/intentius/choudoufu/internal/providers"
)

// Path literals and pins, centralized on purpose: the rename phase that
// moved stateless/ to live/ and the module path to
// github.com/intentius/choudoufu had this block as its one stop in this
// tool, and a later path move should stay just as cheap.
const (
	// surveyJSONRel is where the generated artifact is committed, relative
	// to the repository root.
	surveyJSONRel = "live/survey.json"

	// surveyFullJSONRel is the -all artifact: the same per-type fields as
	// surveyJSONRel, over the provider's entire resource-type roster
	// instead of the curated 68 (issue #41).
	surveyFullJSONRel = "live/survey-full.json"

	// rosterRel is the hand-owned curated roster live/survey.json covers.
	rosterRel = "tools/survey-gen/roster.txt"

	// providerSource pins the provider surveyed. The default version is
	// internal/live/pins.AWSProviderVersion, one constant shared with
	// tools/corpus-gen so the instrument that ranks admission failures and
	// the artifacts that define admission cannot describe different
	// providers again (#117). The estate fixtures pin their own release
	// deliberately; see the pins package doc.
	providerSource = "hashicorp/aws"

	// defaultInitBin downloads the provider. Stock terraform, the same
	// binary the gated test tier drives; -init-bin swaps it for choudoufu
	// or tofu, which resolve the same release via registry.opentofu.org.
	defaultInitBin = "terraform"
)

// providerVersion is the release this run surveys - internal/live/pins.AWSProviderVersion
// unless -provider-version overrides it (issue #441). A var, not a const,
// only so main can override it from the flag before run() reads it; every
// call site (schemas.go's acquireSchemas, classify.go's buildSurvey) reads
// this package variable exactly as it read the constant before, so a plain
// `go run ./tools/survey-gen` with no flag surveys the pinned release
// byte-identically to before this change.
//
// The override exists for `just provider-bump`'s movement report
// (tools/provider-bump-report), not for moving the pin itself: writing
// live/survey.json and live/survey-full.json at a version other than
// internal/live/pins.AWSProviderVersion leaves live/pins_drift_test.go
// failing until a human edits that constant too, which is the point - a
// bump is a report a maintainer reviews and commits, never a side effect of
// running this tool.
var providerVersion = pins.AWSProviderVersion

// repoRoot resolves the checkout's root from this file's own location, the
// same trick flocitest.RepoRoot uses, so the tool runs from any directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/survey-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	initBin := flag.String("init-bin", defaultInitBin,
		"binary that downloads the pinned provider (terraform, tofu or choudoufu)")
	render := flag.Bool("render", false,
		"rewrite the derived spans this tool owns in live/LIMITATIONS.md, live/MARKERS.md and live/COVERAGE.md from committed artifacts instead of regenerating the survey (needs no provider)")
	all := flag.Bool("all", false,
		"also survey the provider's entire resource-type roster and write live/survey-full.json (issue #41); live/survey.json is still written unchanged")
	accept := flag.Bool("accept", false,
		"stamp the artifact header's accepted field with today's date, ratifying the regenerated rows for review (tools/registry-gen/pin.go's SpecPin.Accepted vocabulary); omit to regenerate without ratifying, which drops any previously accepted date out of the diff")
	providerVersionFlag := flag.String("provider-version", pins.AWSProviderVersion,
		"survey this hashicorp/aws release instead of the pinned internal/live/pins.AWSProviderVersion (issue #441). Written artifacts then disagree with live/pins_drift_test.go until the pin itself is bumped by hand - a movement dry run (see `just provider-bump`) passes the pinned version back to itself so the override is exercised with nothing left to commit")
	flag.Parse()
	providerVersion = *providerVersionFlag

	if *render {
		if err := runRender(); err != nil {
			fmt.Fprintf(os.Stderr, "survey-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(*initBin, *all, *accept); err != nil {
		fmt.Fprintf(os.Stderr, "survey-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(initBin string, all, accept bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	roster, err := readRoster(filepath.Join(root, rosterRel))
	if err != nil {
		return fmt.Errorf("reading the roster from %s: %w", rosterRel, err)
	}
	fmt.Fprintf(os.Stderr, "survey-gen: %d types in %s\n", len(roster), rosterRel)

	workdir, err := os.MkdirTemp("", "survey-gen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	schemas, importable, err := acquireSchemas(initBin, workdir, os.Stderr)
	if err != nil {
		return err
	}

	today := time.Now().UTC().Format("2006-01-02")
	return writeSurveys(root, schemas, importable, roster, all, accept, today, os.Stderr)
}

// writeSurveys writes live/survey.json from the curated roster and, when
// all is true, also live/survey-full.json from the provider's entire
// resource-type roster (issue #41). survey.json's bytes never depend on
// all - the curated write happens first and the same way either way - which
// is what keeps `survey-gen` without -all byte-identical to today's output.
//
// Split out of run so the roster-selection logic (the actual delta issue
// #41 describes: buildSurvey already classifies provider-wide once given
// every type name) is testable against fake schemas, with no provider and
// no network.
//
// accept and today govern the Accepted header field (issue #37, increment
// 1): when accept is true, both written artifacts carry today verbatim in
// their accepted field; when it is false, today is unused and the field is
// left unset, which is how an unreviewed regeneration surfaces in the diff.
func writeSurveys(root string, schemas providers.GetProviderSchemaResponse, importable map[string]bool, roster []string, all, accept bool, today string, log io.Writer) error {
	survey := buildSurvey(schemas, roster, importable)
	if accept {
		survey.Accepted = today
	}
	data, err := survey.marshal()
	if err != nil {
		return err
	}

	out := filepath.Join(root, surveyJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(log, "survey-gen: wrote %s (%d types: %d taggable, %d with list resources, %d with identity schemas)%s\n",
		surveyJSONRel, len(survey.Types), survey.Counts.Taggable, survey.Counts.ListResource, survey.Counts.IdentitySchema, acceptedSuffix(survey.Accepted))

	if !all {
		return nil
	}

	full := buildSurvey(schemas, allResourceTypeNames(schemas), importable)
	full.GeneratedBy = "tools/survey-gen (go run ./tools/survey-gen -all)"
	if accept {
		full.Accepted = today
	}
	fullData, err := full.marshal()
	if err != nil {
		return err
	}

	fullOut := filepath.Join(root, surveyFullJSONRel)
	if err := os.WriteFile(fullOut, fullData, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(log, "survey-gen: wrote %s (%d types: %d taggable, %d with list resources, %d with identity schemas)%s\n",
		surveyFullJSONRel, len(full.Types), full.Counts.Taggable, full.Counts.ListResource, full.Counts.IdentitySchema, acceptedSuffix(full.Accepted))
	return nil
}

// acceptedSuffix renders ", accepted <date>" for a log line when accepted
// is set, or nothing when the run did not pass -accept.
func acceptedSuffix(accepted string) string {
	if accepted == "" {
		return ""
	}
	return ", accepted " + accepted
}
