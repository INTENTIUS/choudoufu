// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// mapping-gen generates live/mapping.json, the TF-to-CFN type mapping
// artifact everything in #40 flows through (issue #43).
//
// It joins two committed rosters - live/survey-full.json's 1,691 Terraform
// AWS resource types (issue #41) and live/registry.json's 1,653
// CloudFormation Registry types (issue #42) - against the curated overlay
// at tools/mapping-gen/overlay.json plus issue #52's two generated/sourced
// tables (names_data.hcl's service-alias join and former2's per-resource
// pairing, each committed as its own artifact - namesdata-generated.json,
// former2-rows.json - so a default run needs no network, same as every
// other artifact this tool reads), and writes one row per TF type:
//
//	{"tf_type": "aws_s3_bucket", "cfn_type": "AWS::S3::Bucket", "via": "name", "fold_parent": null, "note": null}
//
// via is name (the heuristic in heuristic.go derived the CFN type from the
// TF type's own name), alias (the overlay asserts the pair by hand),
// service-alias (servicealias.go's heuristic v2 matched the TF type's
// resource tokens against one CFN service's own resource names, the
// service now coming from names_data.hcl's generated table, the overlay's
// service_aliases kept only for prefixes the source disagrees with or does
// not cover), former2 (iann0036/former2's own independent per-resource
// CFN/TF pairing, for a TF type none of the above could resolve), fold (the
// TF type is a property-child of a CFN parent - fold_parent carries the
// parent's CFN type and cfn_type stays null), or none (no CFN counterpart;
// note says why when known).
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/mapping-gen
//
// -refresh-names-data and -refresh-former2 re-fetch (network, or an
// explicit -names-data-hcl/-former2-src override that also seeds the disk
// cache) and rewrite this tool's two committed source artifacts; each
// prints the pin block to paste into the matching *_source.go if the fetched
// content's digest has moved (or, with the matching accept env set, proceeds
// and prints the new block anyway).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Path literals, centralized on purpose (see tools/survey-gen/main.go's
// const block for why): the artifacts this tool reads and writes, relative
// to the repository root.
const (
	// tfRosterRel is the TF-side roster: issue #41's whole-provider survey.
	tfRosterRel = "live/survey-full.json"

	// cfnRosterRel is the CFN-side roster: issue #42's registry artifact.
	cfnRosterRel = "live/registry.json"

	// curatedMDRel is the hand-curated 68-type table the pin test measures
	// against; mapping-gen itself only reads its type-name column.
	curatedMDRel = "live/SURVEY.md"

	// overlayJSONRel is the curated overlay this tool joins the two rosters
	// against: aliases the name heuristic cannot derive, folds (TF
	// sub-resources a CFN parent absorbs), nones (TF types with no CFN
	// counterpart at all, with a reason when known), and service_aliases
	// entries the generated names_data.hcl table disagrees with or does
	// not cover.
	overlayJSONRel = "tools/mapping-gen/overlay.json"

	// mappingJSONRel is where the generated artifact is committed.
	mappingJSONRel = "live/mapping.json"

	// namesDataGeneratedRel and former2RowsRel are issue #52's two
	// committed source artifacts, relative to tools/mapping-gen itself
	// (they are go:embed'd from there, so also read from there by the
	// refresh flags for a consistent single path).
	namesDataGeneratedRel = "tools/mapping-gen/namesdata-generated.json"
	former2RowsRel        = "tools/mapping-gen/former2-rows.json"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick survey-gen's and registry-gen's repoRoot use, so the tool runs
// from any directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/mapping-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	refreshNamesData := flag.Bool("refresh-names-data", false, "re-fetch names_data.hcl and rewrite "+namesDataGeneratedRel)
	namesDataOverride := flag.String("names-data-hcl", "", "use this names_data.hcl instead of the cache/download; also seeds the cache (with -refresh-names-data)")
	refreshFormer2 := flag.Bool("refresh-former2", false, "re-fetch former2's js/services and rewrite "+former2RowsRel)
	former2Override := flag.String("former2-src", "", "use this former2 tarball or concatenated services text instead of the cache/download; also seeds the cache (with -refresh-former2)")
	accept := flag.Bool("accept", false, "proceed past a source content-pin mismatch instead of refusing (same effect as CHOUDOUFU_ACCEPT_NAMES_DATA / CHOUDOUFU_ACCEPT_FORMER2)")
	flag.Parse()

	if err := run(*refreshNamesData, *namesDataOverride, *refreshFormer2, *former2Override, *accept); err != nil {
		fmt.Fprintf(os.Stderr, "mapping-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(refreshNamesData bool, namesDataOverride string, refreshFormer2 bool, former2Override string, accept bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	tfTypes, err := loadTFRoster(filepath.Join(root, tfRosterRel))
	if err != nil {
		return fmt.Errorf("reading the TF roster from %s: %w", tfRosterRel, err)
	}

	cfnRoster := registryJSONRoster{path: filepath.Join(root, cfnRosterRel)}
	cfnTypes, err := cfnRoster.Types()
	if err != nil {
		return fmt.Errorf("reading the CFN roster from %s: %w", cfnRosterRel, err)
	}
	cfnWithPrimaryID, err := cfnRoster.WithPrimaryIdentifier()
	if err != nil {
		return fmt.Errorf("reading primaryIdentifier presence from %s: %w", cfnRosterRel, err)
	}

	overlay, err := loadOverlay(filepath.Join(root, overlayJSONRel))
	if err != nil {
		return fmt.Errorf("reading the overlay from %s: %w", overlayJSONRel, err)
	}

	tfSet := make(map[string]bool, len(tfTypes))
	for _, t := range tfTypes {
		tfSet[t] = true
	}
	cfnSet := make(map[string]bool, len(cfnTypes))
	for _, t := range cfnTypes {
		cfnSet[t] = true
	}

	if refreshNamesData {
		if err := doRefreshNamesData(root, namesDataOverride, cfnTypes, accept); err != nil {
			return err
		}
	}
	if refreshFormer2 {
		if err := doRefreshFormer2(root, former2Override, cfnWithPrimaryID, cfnSet, tfSet, accept); err != nil {
			return err
		}
	}

	generatedAliases := PinnedNamesData.Aliases
	former2Usable, former2Drops := filterFormer2Rows(PinnedFormer2.Rows, cfnWithPrimaryID, cfnSet, tfSet)

	mapping, needsAlias, contradictions, err := buildMapping(tfTypes, cfnTypes, Sources{
		Overlay:                 overlay,
		GeneratedServiceAliases: generatedAliases,
		Former2:                 former2Usable,
	})
	if err != nil {
		return err
	}

	data, err := mapping.marshal()
	if err != nil {
		return err
	}

	out := filepath.Join(root, mappingJSONRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "mapping-gen: wrote %s (%d types: %d mapped, %d fold, %d none)\n",
		mappingJSONRel, mapping.Counts.Types, mapping.Counts.Mapped, mapping.Counts.Fold, mapping.Counts.None)
	fmt.Fprintf(os.Stderr, "mapping-gen: per-source mapped counts: %v\n", mapping.Counts.ByVia)
	fmt.Fprintf(os.Stderr, "mapping-gen: former2 contributed %d row(s) (%d raw rows dropped: see the reasons in %s)\n",
		countVia(mapping, viaFormer2), len(former2Drops), former2RowsRel)

	// former2 contradictions never block generation (the artifact's own
	// header carries the report - see Mapping.Former2Contradictions) - only
	// TestFormer2ContradictionsAcknowledged in mapping_gen_test.go gates on
	// them, the same "test failure until resolved" shape
	// TestServiceAliasCrossHeuristicCollisions already uses for its own
	// allowlist.
	if len(contradictions) > 0 {
		fmt.Fprintf(os.Stderr, "mapping-gen: former2 contradicts %d already-mapped row(s) (see former2_contradictions in %s; must be acknowledged in mapping_gen_test.go's allowlist):\n",
			len(contradictions), mappingJSONRel)
		for _, c := range contradictions {
			fmt.Fprintf(os.Stderr, "  %s: mapped %s (via %s), former2 says %s\n", c.TFType, c.MappedCFN, c.MappedVia, c.Former2CFN)
		}
	}

	// The service-alias heuristic's ambiguous hits are never written into
	// mapping.json (see servicealias.go's package comment) - printed here
	// instead, so a human can turn one of the candidates into an explicit
	// overlay alias, but nothing downstream reads this report back.
	if len(needsAlias) > 0 {
		fmt.Fprintf(os.Stderr, "mapping-gen: %d TF type(s) matched more than one CFN type within their aliased service (needs a hand overlay alias, not mapped):\n", len(needsAlias))
		for _, n := range needsAlias {
			fmt.Fprintf(os.Stderr, "  %s -> %s\n", n.TFType, strings.Join(n.Candidates, " | "))
		}
	}
	return nil
}

func countVia(m Mapping, via string) int {
	n := 0
	for _, r := range m.Rows {
		if r.Via == via {
			n++
		}
	}
	return n
}

// doRefreshNamesData re-fetches names_data.hcl and rewrites
// namesdata-generated.json in place; the caller still needs to move
// namesDataTag/the pin's expectations in namesdata_source.go by hand and
// commit both files together, the same review-a-diff shape registry-gen's
// own -zip/CHOUDOUFU_ACCEPT_REGISTRY_SPEC refresh has.
func doRefreshNamesData(root, override string, cfnTypes []string, accept bool) error {
	cachePath, err := namesDataCachePath()
	if err != nil {
		return err
	}
	raw, err := acquireNamesData(context.Background(), cachePath, override, os.Stderr)
	if err != nil {
		return err
	}
	acceptEnv := accept || os.Getenv(namesDataAcceptEnv) != ""
	if err := AssertNamesDataPinned(raw, PinnedNamesData.Pin, acceptEnv, func(m string) { fmt.Fprintln(os.Stderr, m) }); err != nil {
		return err
	}

	accepted := PinnedNamesData.Pin.Accepted
	if accepted == "" || namesDataDigest(raw) != PinnedNamesData.Pin.Digest {
		// The content moved (or this is the first-ever acceptance, digest
		// ""): stamp today's date, same as pasting registry-gen's own
		// printed pin block does by hand - a bump reads as a decision, not
		// one opaque hash silently replacing another.
		accepted = time.Now().UTC().Format("2006-01-02")
	}
	art, err := buildNamesDataArtifact(raw, cfnTypes, accepted)
	if err != nil {
		return err
	}
	data, err := art.marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, namesDataGeneratedRel), data, 0o644); err != nil { //nolint:gosec // a committed artifact
		return err
	}
	fmt.Fprintf(os.Stderr, "mapping-gen: wrote %s (%d families, %d prefixes, %d mismatches; digest %s)\n",
		namesDataGeneratedRel, art.Pin.Families, art.Pin.Prefixes, art.Pin.Mismatches, art.Pin.Digest)
	return nil
}

// doRefreshFormer2 is doRefreshNamesData's former2 twin.
func doRefreshFormer2(root, override string, cfnWithPrimaryID, cfnKnown, tfKnown map[string]bool, accept bool) error {
	cachePath, err := former2CachePath()
	if err != nil {
		return err
	}
	text, err := acquireFormer2Services(context.Background(), cachePath, override, os.Stderr)
	if err != nil {
		return err
	}
	acceptEnv := accept || os.Getenv(former2AcceptEnv) != ""
	if err := AssertFormer2Pinned(text, PinnedFormer2.Pin, acceptEnv, func(m string) { fmt.Fprintln(os.Stderr, m) }); err != nil {
		return err
	}

	accepted := PinnedFormer2.Pin.Accepted
	if accepted == "" || former2Digest(text) != PinnedFormer2.Pin.Digest {
		accepted = time.Now().UTC().Format("2006-01-02")
	}
	art := buildFormer2Artifact(text, cfnWithPrimaryID, cfnKnown, tfKnown, accepted)
	data, err := art.marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, former2RowsRel), data, 0o644); err != nil { //nolint:gosec // a committed artifact
		return err
	}
	fmt.Fprintf(os.Stderr, "mapping-gen: wrote %s (%d raw rows, %d usable, %d dropped; digest %s)\n",
		former2RowsRel, art.Pin.RawRows, art.Pin.UsableRows, art.Pin.Dropped, art.Pin.Digest)
	return nil
}
