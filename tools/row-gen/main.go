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
// Every mode but -emit only prints: the maintainer stance from #37 is that
// a wrong row touches live infrastructure, so a human pastes, edits and
// ratifies every block this tool proposes. -emit is the exception issue #96
// added, and it writes the two generated tables outright - see emit.go for
// why that is a fixed point over already-ratified rows rather than a fresh
// derivation.
//
// One consequence of that is worth stating where the modes are listed, since
// it is not what a generator usually behaves like (issue #263): a row that
// leaves the table cannot be brought back by re-running -emit, because the
// table it reads is the table it wrote last time. -emit therefore refuses to
// write a smaller table unless -allow-retraction says so. retraction.go is
// the whole of that gate and states the measurement behind it.
//
//	go run ./tools/row-gen              # every service batch, full report
//	go run ./tools/row-gen -service Lambda
//
// -mismatches switches to a second mode entirely: for every type -emit
// would admit, it diffs a fresh proposal against the ratified entry and
// writes live/rowgen-mismatches.json - a measurement of this tool's own
// proposals against ground truth, not a row for live infrastructure. See
// comparison.go and mismatches.go for the comparison and the artifact.
//
//	go run ./tools/row-gen -mismatches
//
// -schema-precedence writes live/schema-precedence.json, issue #387's own
// measurement over the same ratified rows: does the provider's identity
// schema reproduce the row? See schemafirst.go.
//
//	go run ./tools/row-gen -schema-precedence
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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Path literals, centralized on purpose (see tools/survey-gen/main.go's and
// tools/mapping-gen/main.go's const blocks for why): every artifact this
// tool reads, relative to the repository root. row-gen writes nothing
// except, in -mismatches and -schema-precedence mode, the artifact each writes.
const (
	registryJSONRel      = "live/registry.json"
	mappingJSONRel       = "live/mapping.json"
	surveyJSONRel        = "live/survey-full.json"
	carveSeedJSONRel     = "tools/mapping-gen/carve-seed.json"
	importGrammarJSONRel = "live/import-grammar.json"
	annotationsJSONRel   = "tools/row-gen/annotations.json"
	schemaFactsJSONRel   = "live/registry-schema-facts.json"
)

// Path literals continued: logicalSchemasJSONRel lives beside its own
// generator in logicalschemas.go, since -logical-schemas is the only mode
// that writes it and -emit the only one that reads it. mismatchesJSONRel
// and schemaPrecedenceJSONRel do the same, each beside the single mode that
// writes it (mismatches.go, schemafirst.go).

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
	mismatches := flag.Bool("mismatches", false, "measure row-gen's fresh proposals against tools/row-gen/ratified.json's ratified entries and write live/rowgen-mismatches.json, instead of printing the pastable-row report")
	schemaPrecedence := flag.Bool("schema-precedence", false, "issue #387: measure whether the provider's own identity schema reproduces each config-identified ratified row, and write live/schema-precedence.json (see schemafirst.go)")
	propose := flag.Bool("propose", false, "issue #65's PROPOSE stage: print only the rule classes with a 100% historical adoption record and their not-yet-admitted candidates, instead of the full pastable-row report (see propose.go)")
	sources := flag.Bool("sources", false, "issue #106: compare the sources that describe each type's identity - the provider's schema, the scraped docs, and the ratified table - and write live/identity-sources.json")
	compositeImport := flag.Bool("composite-import", false, "issue #337: classify the markerless types whose documented import is composite and whose provider serves no wire identity schema, by whether the page's own Attribute Reference proves the exported `id` is the whole import string, and write live/composite-import-roster.json (see compositeimport.go)")
	evidenceGap := flag.Bool("evidence-gap", false, "issue #428: partition the bucketEvidenceOnly population applySchemaFirstArgName still leaves behind - a provider identity schema exists but does not, by itself, cover the type, or no identity schema exists at all - into named families with a per-family note on what evidence source would actually close the gap, and write tools/row-gen/evidence-schema-gap.json (see evidencegap.go)")
	emit := flag.Bool("emit", false, "issue #96: write generated Go source for internal/live/identity.DefaultTable and internal/live/lint's admittedTypesV0 (one generated file per table; nothing hand-written participates), rendering every non-RecordBacked row from tools/row-gen/ratified.json, instead of printing blocks to paste by hand (see emit.go)")
	logicalSchemas := flag.Bool("logical-schemas", false, "read the record-store effects providers' own schemas and write live/logical-schemas.json, the evidence -emit derives every RecordBacked row from (see logicalschemas.go). Needs -init-bin; every other mode is offline")
	initBin := flag.String("init-bin", "terraform", "the binary -logical-schemas runs `init` with to install each provider")
	allowRetraction := flag.Bool(strings.TrimPrefix(allowRetractionFlag, "-"), false, "issue #263: let -emit write a table with FEWER admitted types than the one it read. Off by default because a retracted type stops resolving for every configuration that names it, which is a support change worth stating rather than noticing - not because it is unrecoverable. Re-emitting after fixing the cause DOES restore the rows; they are ratified in tools/row-gen/ratified.json, which no generator writes (see retraction.go)")
	flag.Parse()

	if *logicalSchemas {
		if err := runLogicalSchemas(*initBin, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *mismatches {
		if err := runMismatches(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *schemaPrecedence {
		if err := runSchemaPrecedence(os.Stdout, os.Stderr); err != nil {
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

	if *sources {
		if err := runSources(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *compositeImport {
		if err := runCompositeImport(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *emit {
		if err := runEmit(os.Stdout, os.Stderr, *allowRetraction); err != nil {
			fmt.Fprintf(os.Stderr, "row-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *evidenceGap {
		if err := runEvidenceGap(os.Stdout, os.Stderr); err != nil {
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
	fmt.Fprintf(errOut, "row-gen: %d types (%d server-assigned, %d client-named, %d composite, %d assembled, %d needs-hand-separator, %d fold-child, %d evidence-only)\n",
		len(proposals), counts.ServerAssigned, counts.ClientNamed, counts.Composite, counts.Assembled, counts.NeedsHandSeparator, counts.FoldChild, counts.EvidenceOnly)
	return nil
}

// loadProposals reads the five committed artifacts (the four
// classifyAll always needed, plus live/import-grammar.json which
// classifyAll's own precedence pass reads too) and returns the full,
// classified mapped set - the shared entry point run, runMismatches and
// runPropose all build on, so no two modes can see a differently-classified
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

// indexByType is the small lookup both comparison.go's buildComparison and
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
// (applyImportGrammarDemotions) and, since the mismatch ledger, the
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
	var unmapped []proposal
	for _, r := range rows {
		switch {
		case r.Via == "fold":
			folds = append(folds, r)
		case r.CFNType != nil:
			entry, ok := registry[*r.CFNType]
			if !ok {
				return nil, fmt.Errorf("%s: mapped to %s, which is not in %s (a stale mapping against the current registry)", r.TFType, *r.CFNType, registryJSONRel)
			}
			mapped = append(mapped, classifyMapped(r.TFType, *r.CFNType, entry, survey, importGrammar, carveSeed))
		case cfnModelledVia[r.Via]:
			// A via that PROMISES a cfn_type and has none is still the
			// broken invariant it always was; only the vias that promise
			// nothing fall through to classifyUnmapped below.
			return nil, fmt.Errorf("%s: via=%s but cfn_type is null (a mapping.json invariant broke)", r.TFType, r.Via)
		default:
			unmapped = append(unmapped, classifyUnmapped(r.TFType, r.Via, r.Note))
		}
	}

	out := make([]proposal, 0, len(mapped)+len(folds)+len(unmapped))
	out = append(out, mapped...)
	for _, r := range folds {
		if r.FoldParent == nil {
			return nil, fmt.Errorf("%s: via=fold but fold_parent is null (a mapping.json invariant broke)", r.TFType)
		}
		out = append(out, classifyFold(r.TFType, *r.FoldParent, mapped, admitted))
	}
	out = append(out, unmapped...)
	applyImportGrammarDemotions(out, importGrammar)
	applyImportGrammarPrecedence(out, importGrammar)
	// Schema-first (#428, evidenceschema.go) runs LAST, deliberately after
	// the import-grammar precedence pass rather than ahead of it: an early
	// measurement here put it first, on the "provider identity schema
	// outranks import grammar" reasoning resolveArgName already states, and
	// that made this pass front-run rows applyImportGrammarPrecedence's own
	// rules (tryArgumentReferenceConfirmedGuess, tryGrammarComposite) were
	// already going to promote out of bucketEvidenceOnly anyway - a
	// same-population re-derivation with a different Rule string, not new
	// coverage, and it left live/rowgen-buckets.json's evidence_only count
	// unchanged (measured: 22 rows relabeled, 0 net bucket movement).
	// Running last instead means this pass only ever sees a row every other
	// evidence source in this file already had its turn on and still could
	// not name - the true "314" the issue's own numbers describe - so a
	// Covered row here is coverage neither the registry nor import-grammar
	// could supply, not credit taken from a rule that would have gotten
	// there anyway. See evidenceschema.go's own doc comment.
	applySchemaFirstArgName(out, survey)
	return out, nil
}
