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
//
// -convergence switches to a second mode entirely (rowgen-convergence): for
// every type internal/live/identity.DefaultTable already admits, it diffs a
// fresh proposal against the ratified entry and writes
// live/rowgen-convergence.json, the one artifact this tool does write - a
// measurement of its own proposals against ground truth, not a row for
// live infrastructure. See convergence.go's own doc comment.
//
//	go run ./tools/row-gen -convergence
//
// -propose switches to a third mode (issue #65's PROPOSE stage): only the
// proposals whose classification rule has a spotless, large-enough
// historical record - see propose.go's own doc comment for the exact bar
// and the report's own printed contract for what a human approving those
// proposals is and is not trusting. Still prints only; still writes
// nothing.
//
//	go run ./tools/row-gen -propose
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Path literals, centralized on purpose (see tools/survey-gen/main.go's and
// tools/mapping-gen/main.go's const blocks for why): every artifact this
// tool reads, relative to the repository root. row-gen writes nothing
// except, in -convergence mode, convergenceJSONRel itself.
const (
	registryJSONRel      = "live/registry.json"
	mappingJSONRel       = "live/mapping.json"
	surveyJSONRel        = "live/survey-full.json"
	carveSeedJSONRel     = "tools/mapping-gen/carve-seed.json"
	importGrammarJSONRel = "live/import-grammar.json"
	annotationsJSONRel   = "tools/row-gen/annotations.json"
	convergenceJSONRel   = "live/rowgen-convergence.json"
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
	convergence := flag.Bool("convergence", false, "measure row-gen's fresh proposals against internal/live/identity.DefaultTable's ratified entries and write live/rowgen-convergence.json, instead of printing the pastable-row report")
	propose := flag.Bool("propose", false, "issue #65's PROPOSE stage: print only the rule classes with a 100% historical adoption record and their not-yet-admitted candidates, instead of the full pastable-row report (see propose.go)")
	emit := flag.Bool("emit", false, "issue #96: write generated Go source for internal/live/identity.DefaultTable and internal/live/lint's admittedTypesV0 (a generated + an override-ledger file per table), instead of printing anything to paste by hand (see emit.go)")
	flag.Parse()

	if *convergence {
		if err := runConvergence(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *propose {
		if err := runPropose(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *emit {
		if err := runEmit(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

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
	proposals, err := loadProposals(root)
	if err != nil {
		return err
	}

	fmt.Fprint(out, renderReport(proposals, service))
	counts := tally(proposals)
	fmt.Fprintf(errOut, "row-gen: %d mapped types (%d server-assigned, %d client-named, %d composite, %d needs-hand-separator, %d fold-child, %d evidence-only)\n",
		len(proposals), counts.ServerAssigned, counts.ClientNamed, counts.Composite, counts.NeedsHandSeparator, counts.FoldChild, counts.EvidenceOnly)
	return nil
}

// loadProposals reads the five committed artifacts (the four
// classifyAll always needed, plus live/import-grammar.json which
// classifyAll's own precedence pass reads too) and returns the full,
// classified mapped set - the shared entry point run and runConvergence
// both build on, so the two modes can never see a differently-classified
// proposal for the same type.
func loadProposals(root string) ([]proposal, error) {
	registry, err := loadRegistry(filepath.Join(root, registryJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", registryJSONRel, err)
	}
	mapping, err := loadMapping(filepath.Join(root, mappingJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", mappingJSONRel, err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", surveyJSONRel, err)
	}
	carveSeed, err := loadCarveSeed(filepath.Join(root, carveSeedJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", carveSeedJSONRel, err)
	}
	importGrammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", importGrammarJSONRel, err)
	}
	return classifyAll(mapping, registry, survey, carveSeed, importGrammar)
}

// runConvergence is -convergence's entry point: loads the same mapped set
// run() classifies, plus tools/row-gen/annotations.json's own rulings,
// builds the comparison (convergence.go's buildConvergence), writes
// live/rowgen-convergence.json, and prints the headline numbers - adopted-
// unchanged % and the unannotated mismatch count - to out, the two metrics
// the rowgen-convergence task names as what this tool's report output
// should surface.
func runConvergence(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	proposals, err := loadProposals(root)
	if err != nil {
		return err
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", annotationsJSONRel, err)
	}

	art := buildConvergence(proposals, annotations)

	if problems := validateAnnotations(art, annotations); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(errOut, "row-gen: annotations.json: %s\n", p)
		}
		return fmt.Errorf("%d stale or invalid annotation(s) in %s", len(problems), annotationsJSONRel)
	}

	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", convergenceJSONRel, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, convergenceJSONRel), data, 0o644); err != nil { //nolint:gosec // a fixed path inside the checkout
		return fmt.Errorf("writing %s: %w", convergenceJSONRel, err)
	}

	fmt.Fprintf(out, "wrote %s\n", convergenceJSONRel)
	fmt.Fprintf(errOut, "row-gen -convergence: %d/%d admitted types compared (%d not in the mapped set), %.2f%% adopted-unchanged, %d genuine mismatches (%d annotated, %d unannotated, %d scrape-gap)\n",
		art.Summary.Compared, art.Summary.AdmittedTotal, art.Summary.NotInMappedSet,
		art.Summary.AdoptedUnchangedPct, art.Summary.GenuineMismatches, art.Summary.Annotated, art.Summary.UnannotatedMismatches, art.Summary.ScrapeGapMismatches)
	return nil
}

// runPropose is -propose's entry point: buildProposeReport (propose.go) does
// the whole computation, so this only has to print its two halves in the
// same places every other mode uses - the pastable report to out, the
// one-line summary admission-pipeline's REPORT stage greps for to errOut.
func runPropose(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	report, summary, err := buildProposeReport(root)
	if err != nil {
		return err
	}
	fmt.Fprint(out, report)
	fmt.Fprintln(errOut, summary)
	return nil
}

// indexByType is the small lookup both convergence.go's buildConvergence and
// the test suite (loadAllForTest's callers) need over a classified mapped
// set: last TFType wins, but classifyAll never produces two proposals for
// the same TF type, so that never matters in practice.
func indexByType(proposals []proposal) map[string]proposal {
	m := make(map[string]proposal, len(proposals))
	for _, p := range proposals {
		m[p.TFType] = p
	}
	return m
}

// classifyAll runs every mapped-set row through classifyMapped or
// classifyFold, then applies the import-grammar demotion pass
// (applyImportGrammarDemotions) and, since rowgen-convergence, the
// precedence pass (applyImportGrammarPrecedence) that resolves what the
// demotion pass only used to flag: composites live/import-grammar.json's
// own evidence structures, and registry-primaryIdentifier disagreements the
// ratification batches were correcting by hand every time - see that
// function's own doc comment. cfn_type rows are classified first, since
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
	applyImportGrammarPrecedence(out, importGrammar)
	return out, nil
}
