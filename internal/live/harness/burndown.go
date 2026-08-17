// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package harness

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Burndown is every quantity the project is driving somewhere, ordered by
// ID. The entries are authored; every number in them except Bound comes
// from the entry's own Measure at run time, and Bound is the one thing a
// human is supposed to move deliberately.
//
// Migrated here on 2026-08-16 from three unrelated test files, which held
// six hand-written consts between them:
//
//	live/admission_coverage_test.go        unreachedRatchetMax = 621
//	live/admission_coverage_test.go        universeFloor = 1600
//	live/admission_coverage_test.go        markerlessAdmittedOverlapMax = 0
//	tools/mapping-gen/mapping_gen_test.go  unclassifiedRatchetMax = 13
//	tools/row-gen/convergence_test.go      unannotatedMismatchRatchetMax = 0
//	tools/row-gen/convergence_test.go      annotationCountRatchetMax = 95
//
// Five of those are bounds and are entries below. universeFloor is not a
// bound at all - it is the anti-tamper leg of unreachedRatchetMax - and it
// migrated into that entry's [Denominator], which is the shape every other
// entry now has to answer for too. Working out what each of the other four
// denominators was found two that had none recorded anywhere:
// unclassifiedRatchetMax can be lowered by dropping rows from
// live/mapping.json, and annotationCountRatchetMax by un-admitting the type
// a ruling names.
func Burndown() []Entry {
	return []Entry{
		mappingUnclassified(),
		markerlessAdmittedOverlap(),
		rowgenAnnotationRulings(),
		rowgenUnannotatedMismatches(),
		unreachedTypes(),
	}
}

// providerRoster is the denominator shared by the two entries counted
// against the pinned provider's own type list.
func providerRoster(why string) *Denominator {
	return &Denominator{
		Name:  SurveyFullJSON + " counts.types",
		Floor: 1600,
		Why:   why,
		Measure: func(r *Repo) (int, error) {
			s, err := r.Survey()
			if err != nil {
				return 0, err
			}
			return s.Counts.Types, nil
		},
	}
}

func unreachedTypes() Entry {
	return Entry{
		ID:   "unreached-types",
		Unit: "provider resource types",
		Claim: "Every type the pinned provider serves is in one of three rosters - admitted by " +
			"internal/live/identity.DefaultTable, vetoed by hand in tools/row-gen/rejected.json, or " +
			"vetoed by the derived markerless rule. This counts the ones in none of them, where naming " +
			"the type in a configuration is a hard resolve error with no ledger entry saying why.",
		Bound:     621,
		Direction: AtMost,
		Measured: "internal/live/identity.DefaultTable, tools/row-gen/rejected.json and " +
			"internal/live/identity.MarkerlessTypes",
		Against: SurveyFullJSON,
		AgainstWhy: "tools/survey-gen writes it from the provider's own GetProviderSchema response, and " +
			"none of the three rosters under test contributes a type to it. No edit to the admission " +
			"table or either veto ledger can make this measurement agree with itself.",
		Instrument: "the three rosters read in process (two Go maps and one committed JSON file) against " +
			"the committed provider survey. No provider plugin, no network.",
		BlindSpots: []string{
			"It counts hard resolve errors only. internal/live/lint's schema fallback " +
				"(identity.SynthesizeTypeIdentity) admits some of this population at run time when a real " +
				"provider plugin is present - 60 of them when the count stood at 669 - and that rescue " +
				"needs a plugin, so a ratchet that subtracted it could not run in the fast tier.",
			"It describes one provider. A type from any other provider is outside both the roster and " +
				"this number.",
			"It says nothing about whether an admitted row is correct, only that the type was reached.",
		},
		Denominator: providerRoster(
			"This count is a difference against the roster, so deleting rows from live/survey-full.json " +
				"lowers it exactly as effectively as admitting a type does, and is the cheaper edit. " +
				"hashicorp/aws has never lost a hundred resource types in a release."),
		Tracker: "#245, #246",
		History: []string{
			"669 while the hand ledger stood at 949/81 and again at 944/86 - the batch that moved five " +
				"types from the ledger into the table did not change this count at all, which is why it " +
				"exists rather than a count of the ledger.",
			"665 when the markerless rule landed (#249); 649 while that rule read only the " +
				"CloudFormation registry's verdict.",
			"621 once tools/importdocs-gen's soleid scrape settled 28 untaggable types the registry " +
				"models nothing for.",
		},
		Measure: func(r *Repo) (Reading, error) {
			universe, err := r.SurveyTypes()
			if err != nil {
				return Reading{}, err
			}
			rejected, err := r.Rejected()
			if err != nil {
				return Reading{}, err
			}
			admitted := map[string]bool{}
			for _, t := range identity.AdmittedTypes() {
				admitted[t] = true
			}
			var unreached []string
			for t := range universe {
				if admitted[t] {
					continue
				}
				if _, ok := rejected.Rejected[t]; ok {
					continue
				}
				if _, ok := identity.MarkerlessTypes[t]; ok {
					continue
				}
				unreached = append(unreached, t)
			}
			sort.Strings(unreached)
			return Reading{
				Value:      len(unreached),
				Population: unreached,
				Note: fmt.Sprintf("%d admitted, %d hand-vetoed, %d markerless-vetoed, over a roster of %d",
					len(admitted), len(rejected.Rejected), len(identity.MarkerlessTypes), len(universe)),
			}, nil
		},
	}
}

func markerlessAdmittedOverlap() Entry {
	return Entry{
		ID:   "markerless-veto-admitted-overlap",
		Unit: "types in both the admission table and the markerless veto",
		Claim: "No row in internal/live/identity.DefaultTable names a type the derived markerless rule " +
			"vetoes. A row for a vetoed type is the shipped table contradicting a derived veto, and it " +
			"subtracts from unreached-types without anything supporting it.",
		Bound:      0,
		Direction:  AtMost,
		Measured:   "internal/live/identity.DefaultTable",
		Against:    "internal/live/identity.MarkerlessTypes, and through it " + SurveyFullJSON + "'s signals.taggable",
		AgainstWhy: "The two rosters are different derivations from different evidence even though one -emit run writes both: the table's rows come from the ratified rows plus the import-doc grammar, the veto from the provider survey's own taggability signal. internal/live/stamp's TestPinnedTaggabilityMatchesTheSurvey ties that signal to the run-time marker writer, so the chain ends at the provider schema rather than at another row-gen output.",
		Instrument: "two in-process Go maps intersected. No artifact, no provider, no network.",
		BlindSpots: []string{
			"It cannot see a type the rule should veto but does not - it bounds the contradiction, not " +
				"the rule's recall.",
			"A row pasted by hand for a vetoed type is what this catches; tools/row-gen's PROPOSE stage " +
				"has never been able to offer one, so the generated path is not the risk.",
		},
		Denominator: &Denominator{
			Name:  "internal/live/identity.MarkerlessTypes",
			Floor: 100,
			Why: "The overlap goes to zero two ways: by retracting the offending rows, which is the " +
				"point, or by emptying the veto roster, which is not. The rule vetoes 150 types on the " +
				"pinned release and that population is a property of how many provider types have no " +
				"tags argument, so a collapse to double digits is a rule change and not a provider one.",
			Measure: func(*Repo) (int, error) { return len(identity.MarkerlessTypes), nil },
		},
		Tracker: "#249",
		History: []string{
			"77 for as long as the rule was applied only to what may be admitted next, while 77 rows an " +
				"earlier batch let through stayed in the table.",
			"0 on 2026-08-16, once -emit filtered the emitted rows by the same roster. Zero is the " +
				"ceiling and the floor: a non-zero count means a row reached the table by a route -emit " +
				"does not filter.",
		},
		Measure: func(*Repo) (Reading, error) {
			var both []string
			for t := range identity.MarkerlessTypes {
				if _, ok := identity.DefaultTable[t]; ok {
					both = append(both, t)
				}
			}
			sort.Strings(both)
			return Reading{
				Value:      len(both),
				Population: both,
				Note:       "veto reason: " + identity.MarkerlessReason,
			}, nil
		},
	}
}

func mappingUnclassified() Entry {
	return Entry{
		ID:   "mapping-unclassified",
		Unit: "unclassified rows",
		Claim: "No row in live/mapping.json is a shrug: a via:\"none\" row with only the generic " +
			"unexplained note, meaning nobody has said either what CloudFormation type it corresponds to " +
			"or why it corresponds to none.",
		Bound:      13,
		Direction:  AtMost,
		Measured:   MappingJSON + " counts.unclassified",
		Against:    "the artifact's own rows, and " + SurveyFullJSON + " for the roster size",
		AgainstWhy: "The bound is checked against a count recomputed from the rows rather than against the summary field, so a summary that disagrees with its own body fails instead of passing. The denominator is pinned to the provider survey, which mapping-gen does not write.",
		Instrument: "the committed mapping artifact read as JSON. Deliberately not a regeneration: " +
			"tools/mapping-gen's TestMappingJSONMatchesCommittedInputs already ties the artifact to its " +
			"inputs, and this ratchet stays independent of that test's shape.",
		BlindSpots: []string{
			"It counts rows nobody has classified, not rows classified wrongly. A row folded onto the " +
				"wrong parent reads as classified here.",
			"The taxonomy's three terminal buckets (tf-only, cfn-unmodeled, deprecated-service) are " +
				"classifications, so moving a row into one of them lowers this count without teaching " +
				"anything new about the type.",
		},
		Denominator: &Denominator{
			Name:  MappingJSON + " row count",
			Floor: 1600,
			Why: "The unclassified count is a subset of the rows, so dropping TF types from the mapping " +
				"roster lowers it without classifying anything. The row count must also equal the " +
				"provider survey's own type count, which is what makes this floor external to " +
				"mapping-gen rather than a second number mapping-gen writes.",
			Measure: func(r *Repo) (int, error) {
				m, err := r.Mapping()
				if err != nil {
					return 0, err
				}
				s, err := r.Survey()
				if err != nil {
					return 0, err
				}
				if len(m.Rows) != s.Counts.Types {
					return 0, fmt.Errorf("%s has %d rows but %s serves %d types; the mapping no longer covers the provider roster one row per type",
						MappingJSON, len(m.Rows), SurveyFullJSON, s.Counts.Types)
				}
				return len(m.Rows), nil
			},
		},
		Tracker: "#53",
		History: []string{
			"754 via:\"none\" rows before the first classification pass, 713 after.",
			"13 today, with the family sweeps landed and enforceNoBareNone on.",
		},
		Measure: func(r *Repo) (Reading, error) {
			m, err := r.Mapping()
			if err != nil {
				return Reading{}, err
			}
			var unclassified []string
			for _, row := range m.Rows {
				if row.Via != "none" {
					continue
				}
				unclassified = append(unclassified, row.TFType)
			}
			sort.Strings(unclassified)
			if len(unclassified) != m.Counts.Unclassified {
				return Reading{}, fmt.Errorf(
					"%s has %d via:\"none\" rows but its own counts.unclassified says %d; the summary and "+
						"the body disagree, so neither can be quoted",
					MappingJSON, len(unclassified), m.Counts.Unclassified)
			}
			return Reading{
				Value:      len(unclassified),
				Population: unclassified,
				Note:       fmt.Sprintf("recomputed from %d rows and it agrees with counts.unclassified", len(m.Rows)),
			}, nil
		},
	}
}

func rowgenUnannotatedMismatches() Entry {
	return Entry{
		ID:   "rowgen-unannotated-mismatches",
		Unit: "unruled mismatches",
		Claim: "Every admitted row tools/row-gen's classifier fails to reproduce carries a ruling in " +
			"tools/row-gen/annotations.json naming what a fuller extraction would have to capture. This " +
			"counts the ones that do not.",
		Bound:      0,
		Direction:  AtMost,
		Measured:   ConvergenceJSON + " summary.unannotated_mismatches",
		Against:    AnnotationsJSON,
		AgainstWhy: "The value is recomputed as genuine_mismatches minus annotated and cross-checked against the ledger's own size, so the artifact's summary field cannot be the only witness to its own claim. row-gen writes the artifact; the ledger is hand-authored and reviewed.",
		Instrument: "the committed convergence artifact plus the committed ledger, both read as JSON. " +
			"Not a regeneration - tools/row-gen's TestConvergenceArtifactMatchesCommitted is the drift " +
			"half.",
		BlindSpots: []string{
			"This is generator-autonomy debt and not user-visible coverage. tools/row-gen/emit.go:41 " +
				"copies every field of a ratified row verbatim, so a mismatch changes nothing a user " +
				"experiences. adopted_unchanged from the same artifact is not coverage either and must " +
				"not be quoted as such.",
			"It compares only the mapped set. The types in summary.not_in_mapped_set have no proposal " +
				"to compare at all and are outside this number - the -emit gate holds them to the same " +
				"bar separately.",
		},
		Denominator: &Denominator{
			Name:  ConvergenceJSON + " summary.compared",
			Floor: 800,
			Why: "A mismatch count falls when the compared set shrinks. The compared set is the admitted " +
				"types the mapping reaches, so a loadMapping filter or an un-admission lowers this " +
				"count without any extractor improving.",
			Measure: func(r *Repo) (int, error) {
				c, err := r.Convergence()
				if err != nil {
					return 0, err
				}
				return c.Summary.Compared, nil
			},
		},
		Tracker: "#132",
		History: []string{
			"241 after the ratify-remainder batch; 215 once importdocs-widen's parse and the " +
				"import-precedence rules landed; 194 once the fold-row guard came out.",
			"114 through issue #132's seven extractor commits, then 0 once every remaining mismatch was " +
				"ruled and -emit began refusing an unruled one. It stays 0: a new unannotated mismatch " +
				"is either a regression or an unruled admission.",
		},
		Measure: func(r *Repo) (Reading, error) {
			c, err := r.Convergence()
			if err != nil {
				return Reading{}, err
			}
			a, err := r.Annotations()
			if err != nil {
				return Reading{}, err
			}
			// Recomputed from the rows, not read off the summary. A
			// header that disagrees with its own body is the shape
			// live/survey-full.json's counts.types check already guards
			// against, and this artifact's summary is what the migrated
			// const used to trust outright.
			var unruled, mismatched []string
			for _, t := range c.Types {
				if t.Matched {
					continue
				}
				mismatched = append(mismatched, t.TFType)
				if _, ruled := a.Rulings[t.TFType]; !ruled || !t.Annotated {
					unruled = append(unruled, t.TFType)
				}
			}
			sort.Strings(unruled)
			if len(mismatched) != c.Summary.GenuineMismatches {
				return Reading{}, fmt.Errorf(
					"%s has %d unmatched rows but its own summary.genuine_mismatches says %d; the header "+
						"and the body disagree, so neither can be quoted",
					ConvergenceJSON, len(mismatched), c.Summary.GenuineMismatches)
			}
			if len(unruled) != c.Summary.UnannotatedMismatches {
				return Reading{}, fmt.Errorf(
					"%s has %d unmatched rows with no ruling in %s but its own "+
						"summary.unannotated_mismatches says %d; the artifact is counting rulings the "+
						"ledger does not carry",
					ConvergenceJSON, len(unruled), AnnotationsJSON, c.Summary.UnannotatedMismatches)
			}
			return Reading{
				Value:      len(unruled),
				Population: unruled,
				Note: fmt.Sprintf("recomputed from %d compared rows: %d unmatched, every one of them named by one of the ledger's %d rulings",
					len(c.Types), len(mismatched), len(a.Rulings)),
			}, nil
		},
	}
}

func rowgenAnnotationRulings() Entry {
	return Entry{
		ID:   "rowgen-annotation-rulings",
		Unit: "rulings",
		Claim: "tools/row-gen/annotations.json is a list of named extractor gaps that only ever shrinks. " +
			"With unruled mismatches held at zero, nothing else stops the ledger growing, because adding " +
			"a ruling is always easier than fixing an extractor.",
		Bound:      93,
		Direction:  AtMost,
		Measured:   AnnotationsJSON,
		Against:    ConvergenceJSON,
		AgainstWhy: "Every ruling has to name a type the convergence artifact compared or lists as unmapped, and row-gen writes that artifact from the shipped table rather than from the ledger. A ruling for a type nothing compares is a ruling nothing can retire.",
		Instrument: "the committed ledger read as JSON, cross-checked against the committed convergence " +
			"artifact's type list.",
		BlindSpots: []string{
			"Size is not quality. A ruling whose Exit names no reachable fix counts the same as one that " +
				"does; tools/row-gen's TestAnnotationsAgreeWithMismatches is what forbids a stale one.",
			"Like the mismatch count, this is generator-autonomy debt. It ranks no user-visible work.",
		},
		Denominator: &Denominator{
			Name:  ConvergenceJSON + " summary.admitted_total",
			Floor: 850,
			Why: "The cheapest way to delete a ruling is to un-admit the type it names, which moves the " +
				"type into tools/row-gen/rejected.json and lowers this count while removing support. " +
				"Pinning the admitted total makes that trade visible.",
			Measure: func(r *Repo) (int, error) {
				c, err := r.Convergence()
				if err != nil {
					return 0, err
				}
				return c.Summary.AdmittedTotal, nil
			},
		},
		Tracker: "#132",
		History: []string{
			"128 at the ratchet's introduction: 107 genuine mismatches plus 21 types with no proposal to " +
				"compare.",
			"122, 119, 116 through 2026-08-15 and 16 as classifyUnmapped, tryDocumentedShorterForm and " +
				"the plain-prose enumeration signal each retired a batch of rulings.",
			"95 once the ten record-backed effects rows were derived inside -emit instead of carried as " +
				"unreproduced table rows. That bump also recorded that the constant had already been " +
				"stale by nine, which is the failure a ratchet is supposed to make visible.",
			"93 on 2026-08-16 when this entry was migrated into the harness: the committed ledger was " +
				"already two below its own const, so for the second time in two days the number was not " +
				"bounding anything. Nothing was found to have deleted the two; the const was lowered to " +
				"the measurement rather than the measurement explained.",
		},
		Measure: func(r *Repo) (Reading, error) {
			a, err := r.Annotations()
			if err != nil {
				return Reading{}, err
			}
			c, err := r.Convergence()
			if err != nil {
				return Reading{}, err
			}
			known := make(map[string]bool, len(c.Types))
			for _, t := range c.Types {
				known[t.TFType] = true
			}
			delete(known, "")
			var orphaned []string
			for t := range a.Rulings {
				if _, admitted := identity.DefaultTable[t]; !known[t] && !admitted {
					orphaned = append(orphaned, t)
				}
			}
			if len(orphaned) > 0 {
				sort.Strings(orphaned)
				return Reading{}, fmt.Errorf(
					"%d ruling(s) in %s name a type neither %s compares nor the admission table carries, "+
						"so nothing can ever retire them: %s",
					len(orphaned), AnnotationsJSON, ConvergenceJSON, strings.Join(orphaned, " "))
			}
			return Reading{
				Value:      len(a.Rulings),
				Population: SortedKeys(a.Rulings),
				Note: fmt.Sprintf("every ruling names one of the %d types the convergence artifact carries, over %d admitted types",
					len(known), c.Summary.AdmittedTotal),
			}, nil
		},
	}
}
