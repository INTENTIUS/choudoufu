// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// row-gen turns registry evidence into pastable, ratifiable admission rows
// (issue #44, #40's phase 4, #37's increment 2). For every TF type in
// live/mapping.json with a cfn_type (via name or alias), it classifies the
// type from live/registry.json's evidence - primaryIdentifier against the
// read-only and create-only property sets - into one of four buckets:
// proposed server-assigned, proposed client-named, needs a hand-chosen
// composite separator, or evidence-only. For via:fold rows (TF types
// mapping-gen decided are property-children of a CFN parent) it prints the
// property-child evidence and, when the parent is itself proposed, a
// parent-derived admission note.
//
// The tool only prints. It never writes internal/live/lint/admission.go or
// internal/live/identity/table.go: the maintainer stance from #37 is that a
// wrong row touches live infrastructure, so a human pastes, edits and
// ratifies every block this tool proposes.
//
//	go run ./tools/row-gen              # every service batch, full report
//	go run ./tools/row-gen -service Lambda
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Path literals, centralized on purpose (see tools/survey-gen/main.go's and
// tools/mapping-gen/main.go's const blocks for why): every artifact this
// tool reads, relative to the repository root. row-gen writes nothing.
const (
	registryJSONRel      = "live/registry.json"
	mappingJSONRel       = "live/mapping.json"
	surveyJSONRel        = "live/survey-full.json"
	carveSeedJSONRel     = "tools/mapping-gen/carve-seed.json"
	importGrammarJSONRel = "live/import-grammar.json"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick survey-gen's, registry-gen's and mapping-gen's repoRoot use, so
// the tool runs from any directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/row-gen/main.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func main() {
	service := flag.String("service", "", "restrict the report to one CFN service batch (e.g. Lambda); empty prints every batch")
	flag.Parse()

	if err := run(*service, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
		os.Exit(1)
	}
}

// run loads the four committed artifacts, classifies every row in the
// mapped set, and prints the service-batched report to out. The four
// summary counts always go to err, whether or not -service restricted the
// report, so a caller piping just the report to a file still sees them.
func run(service string, out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	registry, err := loadRegistry(filepath.Join(root, registryJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", registryJSONRel, err)
	}
	mapping, err := loadMapping(filepath.Join(root, mappingJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", mappingJSONRel, err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", surveyJSONRel, err)
	}
	carveSeed, err := loadCarveSeed(filepath.Join(root, carveSeedJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", carveSeedJSONRel, err)
	}
	importGrammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", importGrammarJSONRel, err)
	}

	proposals, err := classifyAll(mapping, registry, survey, carveSeed, importGrammar)
	if err != nil {
		return err
	}

	fmt.Fprint(out, renderReport(proposals, service))
	counts := tally(proposals)
	fmt.Fprintf(errOut, "row-gen: %d mapped types (%d server-assigned, %d client-named, %d needs-hand-separator, %d fold-child, %d evidence-only)\n",
		len(proposals), counts.ServerAssigned, counts.ClientNamed, counts.NeedsHandSeparator, counts.FoldChild, counts.EvidenceOnly)
	return nil
}

// classifyAll runs every mapped-set row through classifyMapped or
// classifyFold, then applies the import-grammar demotion pass
// (applyImportGrammarDemotions). cfn_type rows are classified first, since
// classifyFold needs their results to answer "is the fold parent itself
// proposed" - and, since issue #68, also consults identity.AdmittedTypes()
// to answer "is the fold parent already admitted", independent of what this
// run's own registry-only evidence says about that parent's shape.
func classifyAll(rows []mappingRow, registry map[string]registryEntry, survey map[string]surveyEntry, carveSeed map[string]string, importGrammar map[string]importGrammarRow) ([]proposal, error) {
	admitted := make(map[string]bool)
	for _, t := range identity.AdmittedTypes() {
		admitted[t] = true
	}

	var mapped []proposal
	var folds []mappingRow
	for _, r := range rows {
		if r.Via == "fold" {
			folds = append(folds, r)
			continue
		}
		if r.CFNType == nil {
			return nil, fmt.Errorf("%s: via=%s but cfn_type is null (a mapping.json invariant broke)", r.TFType, r.Via)
		}
		entry, ok := registry[*r.CFNType]
		if !ok {
			return nil, fmt.Errorf("%s: mapped to %s, which is not in %s (a stale mapping against the current registry)", r.TFType, *r.CFNType, registryJSONRel)
		}
		mapped = append(mapped, classifyMapped(r.TFType, *r.CFNType, entry, survey, importGrammar, carveSeed))
	}

	out := make([]proposal, 0, len(mapped)+len(folds))
	out = append(out, mapped...)
	for _, r := range folds {
		if r.FoldParent == nil {
			return nil, fmt.Errorf("%s: via=fold but fold_parent is null (a mapping.json invariant broke)", r.TFType)
		}
		out = append(out, classifyFold(r.TFType, *r.FoldParent, mapped, admitted))
	}
	applyImportGrammarDemotions(out, importGrammar)
	return out, nil
}
