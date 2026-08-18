// Command refusal-probe measures the corpus's refusals without regenerating
// it, and diffs two such measurements.
//
// It exists because every agent working the language wall rebuilt this same
// throwaway program, one per branch, costing five to ten minutes each before
// any of the actual work started. Worse than the time: each rebuild made its
// own choices about what to count, so two agents reporting "sites" were not
// always reporting the same number, and reconciling them cost more time
// again. This is that program, written once.
//
// # Why not just run the generator
//
// `just corpus` acquires provider schemas across ~75 plugin subprocesses and
// takes long enough that six agents in one session backgrounded it, lost
// track of it, and stalled. It is also the wrong tool for a with/without
// comparison: it writes live/corpus-refusals.json, so two agents running it
// concurrently in the same tree clobber each other.
//
// This runs the same check.Dir the generator runs, over the same manifest,
// writes wherever you point it, and finishes in about twenty seconds.
//
// # The bound you must carry
//
// Without provider schemas this UNDERCOUNTS refusals and OVERCOUNTS what a
// fix clears. A type absent from the generated admission table is refused as
// unadmitted here, where the provider's own identity schema would have
// settled it - see check.Context.Schemas. More important for measuring a
// fix: clearing a refusal often reveals another refusal underneath it that
// only a schema-backed run can see.
//
// That is not hypothetical. One fix measured 11 sites cleared with this
// instrument and delivered 10046 -> 10046 on the schema-backed regeneration,
// because fourteen sites had merely moved from one refusal ID to another.
// Another measured 60 and delivered exactly 60. The difference was not luck:
// the second agent checked, per entry, that no other refusal count had
// changed.
//
// So: sweep with this, then verify the entries you care about against real
// schemas before reporting a number as anything but an upper bound. Every
// output carries schemas:false to make that hard to forget.
//
// # -schemas, for the questions the default mode cannot reach
//
// The bound above is not a caveat to recite, it is a blind spot with a shape,
// and three separate agents each hand-wrote their own schema-backed probe
// after hitting it. What the default mode cannot see:
//
//   - The whole stamp layer. [check.Analyze] runs it, but every resource
//     reads as "schema not available" without schemas, which
//     [stamp.SkipReason.Unknown] correctly reports as unknown rather than
//     refused - so the layer contributes nothing at all and the 110 stamp
//     sites in the committed corpus are invisible here.
//   - Any rule that returns false when schemas are nil. Its zero is not
//     evidence of anything; it is the absence of evidence, and counting it as
//     agreement is how a fix "clears" sites that were never measured.
//   - Non-AWS estates. A google_* configuration measured against no google
//     schema reports unadmitted-type for every resource in it, which is a
//     property of the run and not of the configuration.
//
// -schemas acquires each entry's OWN providers (see schemas.go) and runs the
// identical analysis with them. It costs about two and a half minutes warm
// against the default mode's twenty seconds, which is why it is opt-in, and
// it reports per entry which providers it got and which it did not - a
// partial acquisition that goes unreported is how a gate reading
// len(schemas) > 0 came to fabricate hard refusals for types whose own
// provider had simply failed to install. 35 of the 250 manifest entries are
// missing at least one provider, so that is the common case.
//
// It also reports the per-site CAUSE, not only the count: see cause.go. That
// is what the hand-built probes were for.
//
// # What the two modes actually differ by, measured
//
// Both modes over all 250 manifest entries at commit 7d66fa0968, schema-less
// then schema-backed:
//
//	sites      8767 -> 8461      blocked configurations   193 -> 206
//	instances  3587 -> 3921
//
//	Unmarked apply of a marker-only resource      0 -> 110   (the stamp layer)
//	Two resources with the same identity          0 ->  34
//	unadmitted-type                            1835 -> 1299
//	Not an identity attribute                   111 ->   36
//	Unresolvable identity                       174 ->  131
//
// Note the two directions. Sites fall, which is the documented bound - the
// schema-less mode over-reports refusals a provider's own identity schema
// would have settled. But BLOCKED CONFIGURATIONS RISE, by thirteen: the
// schema-less mode reads thirteen configurations as unblocked that a real
// run refuses, because a rule that needs a schema to fire returns false
// without one and a false there is not evidence of anything. So "upper
// bound" is true of sites and false of the verdict, and a fix validated only
// against the default mode can look like it unblocked something it did not.
//
// # What it refuses to measure, and what it refuses to compare
//
// Two silent failures of this tool were found by the agents who hit them, and
// both are the class the tool exists to prevent: an instrument reporting a
// number about something other than what the reader assumes.
//
// A fresh worktree has no .corpus - it is gitignored, and agents symlink one
// in rather than refetch 250 repositories - and a sweep there reported
// "entries 31" with exit 0 and no other output. So a sweep now refuses unless
// every manifest source expands to at least one configuration on this disk,
// and every fetched source sits at the commit the manifest pins. The manifest
// holds 21 globs rather than 250 entries, so the expectation is derived per
// source rather than hard-coded (see corpus.go). -allow-partial-corpus
// measures anyway and records what was wrong.
//
// -diff compared two sweeps of two different trees without a word. It could
// not have done otherwise: -root defaults to "." and both sweeps recorded
// ".", so the field that should have caught it compared equal. Every sweep
// now records its root resolved absolutely (symlinks and all), its commit,
// the manifest's content digest and its per-source corpus accounting, and
// -diff refuses any pair differing in an input that is not the change under
// test: another tree, another manifest, one side schema-backed, another
// provider version, a partial corpus on one side, or two different sets of
// entries.
//
// Three inputs are allowed to differ and are reported rather than refused,
// because each is normal and none of them is the change: module install state
// per entry (one corpus entry moved 59 -> 394 refused sites on that alone),
// the var files each entry was measured with, and per-provider acquisition.
//
// # Use
//
//	refusal-probe -out before.json                 # sweep the whole manifest
//	  ... make your change ...
//	refusal-probe -out after.json
//	refusal-probe -diff before.json,after.json     # what moved
//	refusal-probe -entry .corpus/vpc -v            # one entry, per-site detail
//
//	refusal-probe -schemas -out before.json        # ~2.5 min warm, real schemas
//	refusal-probe -schemas -entry .corpus/vpc -v   # one entry, per-site cause
//
// A schema-less run and a schema-backed one measure different things, so
// -diff refuses to compare them.
//
// # -onboarded, for the question none of the above asks
//
// Everything above measures the ADOPTION question: can a stranger's
// published configuration be taken over exactly as it stands. That is not
// the product's primary goal, which is a fully migrated estate - ordinary
// Terraform, plus a live block, applied, and managed from then on with no
// state file. Nothing measured that, because every corpus entry is somebody
// else's published configuration and (verified over all 250) not one
// declares a live block, a record_store or the sidecar file. So the classes
// a record_store answers were counted as language wall in every figure this
// project has produced.
//
//	refusal-probe -schemas -onboarded -out both.json    # ~3 min warm
//
// analyzes each entry TWICE: once as published, and once after
// internal/live/onboard's computed edit. See onboarded.go for the design and
// for what it still does not claim. The published-form fields are produced
// by the identical call in both modes and do not move; -diff compares the
// published form and refuses a mixed pair.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/pins"
	"github.com/intentius/choudoufu/internal/providers"
)

// probeVersion is the sweep format. It exists so -diff can refuse a file
// written before the fields it now compares existed, rather than reading
// their zero values as agreement - a missing root would have compared equal
// to every other missing root, which is the exact shape of the gap this
// version closes.
const probeVersion = 2

// run is one sweep: what was measured, and what it is worth.
type run struct {
	// ProbeVersion is [probeVersion] at the time of writing.
	ProbeVersion int `json:"probe_version"`

	// Manifest is the manifest path, and Root the tree it was resolved
	// against, so a diff can refuse to compare two runs of different
	// things.
	Manifest string `json:"manifest"`
	Root     string `json:"root"`

	// RootPath is Root made absolute with symlinks resolved, and is what a
	// diff actually compares. Root alone could not do that job: it defaults
	// to "." and two sweeps of two entirely different worktrees therefore
	// both recorded "root": ".", so a guard on it would have compared equal
	// and said nothing. That gap was found by an agent who had relied on it
	// and was fine only because both its sweeps happened to share a tree.
	RootPath string `json:"root_path"`

	// Commit is RootPath's git HEAD, suffixed "+dirty" when tracked files
	// are modified. Informational, never compared: a before/after pair in
	// one tree differs here by construction. It is recorded because a
	// number that does not name the commit it was computed at has been
	// quoted past its own expiry here more than once.
	Commit string `json:"commit,omitempty"`

	// ManifestSHA is the manifest's content digest. The path is compared
	// too, but two trees can hold different manifests at one path, and the
	// manifest decides which directories are measured and with which tfvars
	// files.
	ManifestSHA string `json:"manifest_sha256,omitempty"`

	// Sources is each manifest source and what it expanded to here. It is
	// the run's account of the corpus it measured, and the derivation
	// behind the completeness refusal: the manifest holds 21 globs, not 250
	// entries, so "how many should there be" is computed and never a
	// constant.
	Sources []corpusSource `json:"sources,omitempty"`

	// CorpusProblems is empty on a full sweep and lists what was missing or
	// stale on a partial one. Written in both directions and never omitted:
	// a partial sweep must be recognizable as one from the file alone, and
	// -diff refuses to compare two sweeps whose problems differ.
	CorpusProblems []string `json:"corpus_problems"`

	// Schemas records whether provider schemas were acquired. It is written
	// in BOTH directions and never omitted: a consumer that does not see
	// the field should not be able to assume the answer, and a
	// schema-backed number and a schema-less one are not the same
	// measurement.
	Schemas bool `json:"schemas"`

	// OnboardedForm records whether each entry was ALSO analyzed in its
	// onboarded form - see onboarded.go. Written in both directions for the
	// same reason as Schemas: an entry object carrying no onboarded result
	// because the mode was off and one carrying none because the edit was
	// unmeasurable are different facts, and only this field separates them.
	//
	// It never changes the published-form fields. Those are produced by the
	// identical call in both modes, which is what lets every existing
	// consumer read an -onboarded sweep unchanged.
	OnboardedForm bool `json:"onboarded_form"`

	// InitBin is the binary used to install providers, in -schemas mode.
	// Empty otherwise.
	InitBin string `json:"init_bin,omitempty"`

	// PinsPath, PinSource and PinVersion are the -schemas mode inputs that
	// decide WHICH provider release the corpus was measured against. They
	// were previously reachable only by reading the per-provider rows, and
	// two sweeps taken against two AWS provider versions are not two
	// measurements of one thing however similar their totals look.
	PinsPath   string `json:"pins_path,omitempty"`
	PinSource  string `json:"pin_source,omitempty"`
	PinVersion string `json:"pin_version,omitempty"`

	// Providers is every (provider, constraint) pair this run tried, with
	// the version it got or the error it hit. Present only in -schemas
	// mode, where it is the answer to "what was this measured against" -
	// an AWS-only answer over a corpus containing google_* estates was one
	// agent's whole wasted slot.
	Providers []providerResult `json:"providers,omitempty"`

	// Layers maps a refusal ID to the pass that raised it, so a reader of
	// the JSON can group by layer without a second source.
	Layers map[string]string `json:"layers,omitempty"`

	// Entries are the per-entry results, sorted by name. Per-entry is the
	// unit that matters for a with/without comparison - an aggregate can
	// hold steady while two entries move in opposite directions.
	Entries []entry `json:"entries"`

	// Totals are the aggregate counts.
	Totals totals `json:"totals"`
}

type totals struct {
	Entries    int `json:"entries"`
	Readable   int `json:"readable"`
	Blocked    int `json:"blocked"`
	Sites      int `json:"sites"`
	Instances  int `json:"instances"`
	Shadowed   int `json:"shadowed"`
	Unresolved int `json:"unresolved_modules"`

	// Warnings counts non-fatal sites. They do not block anything, but a
	// change that turns a refusal into a warning must not read as a change
	// that removed it.
	Warnings int `json:"warnings"`

	// UnsetVarSites counts refused sites whose own source text references a
	// required input variable no value was found for. Those refusals may be
	// artifacts of the missing value rather than of the configuration.
	UnsetVarSites int `json:"unset_var_sites"`

	// EntriesPartialSchemas counts entries where at least one declared
	// provider's schemas could not be acquired, and EntriesNoSchemas those
	// where none could. -schemas mode only, and reported even at zero: a
	// run that silently failed to acquire a provider and answered anyway is
	// the failure this pair exists to make visible.
	EntriesPartialSchemas int `json:"entries_partial_schemas"`
	EntriesNoSchemas      int `json:"entries_no_schemas"`

	// Onboarding is the second form's aggregate, in -onboarded mode only.
	// It is a nested object rather than a set of onboarded_* siblings so
	// that no reader can pick "blocked" out of this struct and get the
	// wrong form's number.
	Onboarding *onboardingTotals `json:"onboarding,omitempty"`
}

type entry struct {
	Name     string `json:"name"`
	Origin   string `json:"origin"`
	Readable bool   `json:"readable"`
	Blocked  bool   `json:"blocked"`

	// Sites is this entry's total, and Refusals the per-ID breakdown. A
	// fix that moves sites between IDs without changing the total is the
	// single most common outcome on this codebase, so both are recorded.
	Sites     int            `json:"sites"`
	Refusals  map[string]int `json:"refusals"`
	Instances int            `json:"instances"`
	Shadowed  int            `json:"shadowed"`

	// Warnings is the non-fatal per-ID breakdown, kept apart from Refusals
	// so nothing can add the two together by accident.
	Warnings map[string]int `json:"warnings,omitempty"`

	// Causes is refusal ID -> cause -> sites, where a cause is read off
	// something structured rather than guessed. See [causeCatalog.site].
	// The counts under one ID sum to that ID's site count, including the
	// empty-string key for sites carrying no cause axis at all.
	Causes map[string]map[string]int `json:"causes,omitempty"`

	// Providers is what -schemas mode acquired for THIS entry. An entry
	// with a false Present here has a schema map missing a provider its own
	// configuration needs, and every refusal it reports for a type that
	// provider serves is suspect.
	Providers []entryProvider `json:"providers,omitempty"`

	// UnsetVarSites is this entry's share of [totals.UnsetVarSites].
	UnsetVarSites int `json:"unset_var_sites,omitempty"`

	// Unresolved counts module calls check.Load could not read without
	// installing them. Every count above is a floor for an entry with
	// registry module calls, not a measurement - roughly a sixth of the
	// refusal surface was found missing on one such entry.
	Unresolved int `json:"unresolved_modules"`

	// ModulesInstalled reports whether this entry's directory carries an
	// installed module tree. Written in both directions, because the
	// interesting reading is "no entry has one" and omitempty would make
	// that indistinguishable from "the field was never written".
	//
	// It is the input behind the largest unrecorded swing a sweep could
	// take: 59 refused sites to 394 on one entry, from nothing but whether
	// the install had run. Two trees differing only here produce totals
	// that cannot be subtracted.
	ModulesInstalled bool `json:"modules_installed"`

	// VarFiles is what the manifest handed this entry, resolved against the
	// root. An estate measured with its operator's tfvars and the same
	// estate measured without them are different measurements, and the
	// files are discovered from disk, so two trees can disagree about them
	// while agreeing about everything else.
	VarFiles []string `json:"var_files,omitempty"`

	// Onboarding is what internal/live/onboard concluded about this entry,
	// and Onboarded the analysis of the edited form. Both are nil outside
	// -onboarded mode; Onboarded alone is nil when the edit was
	// unmeasurable, which is why Onboarding records the reason.
	//
	// Everything above this point describes the PUBLISHED form in both
	// modes. Nothing here feeds it.
	Onboarding *onboardingRecord `json:"onboarding,omitempty"`
	Onboarded  *formResult       `json:"onboarded,omitempty"`
}

// providerResult is one (provider, constraint) pair's outcome for the whole
// run.
type providerResult struct {
	Provider   string `json:"provider"`
	Constraint string `json:"constraint,omitempty"`
	Version    string `json:"version,omitempty"`
	Types      int    `json:"resource_types,omitempty"`

	// Pinned is the one provider held at an exact version whatever a
	// requiring configuration asked for; Locked means the version came from
	// the checked-in lock file rather than from resolving the constraint
	// today. An unlocked, unpinned row is the only kind that can move
	// between two runs on an unchanged tree.
	Pinned bool `json:"pinned,omitempty"`
	Locked bool `json:"locked"`

	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// entryProvider is one provider one entry needed, and whether this run had
// it.
type entryProvider struct {
	Provider string `json:"provider"`
	Version  string `json:"version,omitempty"`
	Present  bool   `json:"present"`
	Error    string `json:"error,omitempty"`
}

func main() {
	var (
		manifest = flag.String("manifest", "live/corpus-manifest.json", "manifest to resolve")
		root     = flag.String("root", ".", "repository root the manifest resolves against")
		out      = flag.String("out", "", "write the sweep here as JSON (default: summary to stdout)")
		diff     = flag.String("diff", "", "compare two sweeps: before.json,after.json")
		one      = flag.String("entry", "", "measure only entries whose name contains this")
		verbose  = flag.Bool("v", false, "with -entry, print every refused site")

		withSchemas = flag.Bool("schemas", false, "acquire each entry's own provider schemas and analyze with them (~4 min warm over the whole manifest, against ~20s without); the default mode cannot see the stamp layer, any rule whose false is merely the absence of schemas, or a non-AWS estate at all")
		initBin     = flag.String("init-bin", "terraform", "binary used to install providers, with -schemas")
		pinsPath    = flag.String("provider-pins", "live/corpus-provider-pins.json", "corpus-gen's checked-in lock file, read (never written) so this probe and live/corpus-refusals.json see the same provider releases")
		pinSource   = flag.String("provider-source", "hashicorp/aws", "the one provider held at an exact version rather than at whatever each entry's own constraint resolves to")
		pinVersion  = flag.String("provider-version", pins.AWSProviderVersion, "exact version for -provider-source")
		quiet       = flag.Bool("quiet", false, "suppress the per-entry progress log")

		onboarded = flag.Bool("onboarded", false, "also analyze each entry in its ONBOARDED form - internal/live/onboard's computed edit: a live sidecar declaring record_store \"local\", with the backend or cloud block removed. The published-form numbers are unaffected; this adds a second column. See onboarded.go")

		allowPartial = flag.Bool("allow-partial-corpus", false, "measure anyway when a manifest source expands to no configurations on this disk, or a fetched source sits at a commit the manifest does not pin; the sweep records what was wrong and -diff then refuses to compare it against a full sweep")
	)
	flag.Parse()

	if *diff != "" {
		if err := runDiff(*diff); err != nil {
			fmt.Fprintln(os.Stderr, "refusal-probe:", err)
			os.Exit(1)
		}
		return
	}

	opts := sweepOptions{
		manifest:     *manifest,
		root:         *root,
		only:         *one,
		verbose:      *verbose,
		schemas:      *withSchemas,
		initBin:      *initBin,
		pinsPath:     *pinsPath,
		pinSrc:       *pinSource,
		pinVer:       *pinVersion,
		allowPartial: *allowPartial,
		onboarded:    *onboarded,
	}
	if !*quiet {
		opts.log = os.Stderr
	}

	r, err := sweep(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "refusal-probe:", err)
		os.Exit(1)
	}

	if *out != "" {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "refusal-probe:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "refusal-probe:", err)
			os.Exit(1)
		}
	}
	summarize(r, *out)
}

// sweepOptions is one sweep's inputs.
type sweepOptions struct {
	manifest string
	root     string
	only     string
	verbose  bool

	// schemas switches on acquisition. Everything below is ignored when it
	// is false, and the run then behaves exactly as it did before -schemas
	// existed.
	schemas  bool
	initBin  string
	pinsPath string
	pinSrc   string
	pinVer   string

	// allowPartial proceeds past [corpusState]'s problems instead of
	// refusing. The run still records them.
	allowPartial bool

	// onboarded adds the second form. See onboarded.go.
	onboarded bool

	log io.Writer
}

func sweep(opts sweepOptions) (*run, error) {
	manifestPath := filepath.Join(opts.root, opts.manifest)
	m, err := check.ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	refs, err := m.Resolve(opts.root)
	if err != nil {
		return nil, err
	}

	sources, problems := corpusState(opts.root, m)
	if len(problems) > 0 && !opts.allowPartial {
		return nil, corpusProblemRefusal(realPath(opts.root), sources, problems)
	}

	r := &run{
		ProbeVersion:   probeVersion,
		Manifest:       opts.manifest,
		Root:           opts.root,
		RootPath:       realPath(opts.root),
		Commit:         treeCommit(opts.root),
		ManifestSHA:    fileDigest(manifestPath),
		Sources:        sources,
		CorpusProblems: problems,
		Schemas:        opts.schemas,
		OnboardedForm:  opts.onboarded,
	}
	if opts.onboarded {
		r.Totals.Onboarding = &onboardingTotals{}
	}
	ctx := context.Background()

	causes, err := newCauseCatalog()
	if err != nil {
		return nil, err
	}

	var acq *acquirer
	if opts.schemas {
		if opts.initBin == "" {
			return nil, fmt.Errorf("-schemas needs -init-bin: a binary that can install providers")
		}
		locks, err := loadProviderLocks(filepath.Join(opts.root, opts.pinsPath))
		if err != nil {
			return nil, err
		}
		var pinned addrs.Provider
		if opts.pinVer != "" {
			p, diags := addrs.ParseProviderSourceString(opts.pinSrc)
			if diags.HasErrors() {
				return nil, fmt.Errorf("parsing -provider-source %q: %w", opts.pinSrc, diags.Err())
			}
			pinned = p
		}
		acq = newAcquirer(opts.initBin, pinned, opts.pinVer, locks, opts.log)
		r.InitBin = opts.initBin
		r.PinsPath = opts.pinsPath
		r.PinSource = opts.pinSrc
		r.PinVersion = opts.pinVer
	}

	r.Layers = map[string]string{}

	for _, ref := range refs {
		if opts.only != "" && !strings.Contains(ref.Name, opts.only) {
			continue
		}

		varFiles := make([]string, 0, len(ref.VarFiles))
		for _, v := range ref.VarFiles {
			varFiles = append(varFiles, filepath.Join(opts.root, v))
		}

		var rep check.Report
		var provRows []entryProvider
		var schemas map[string]providers.Schema
		switch {
		case acq == nil:
			// No Schemas: that is the whole bound the default mode carries.
			rep = check.Dir(ctx, ref.Dir, check.Context{}, varFiles...)
		default:
			logf(opts.log, "refusal-probe: %s\n", ref.Name)
			// Load and Analyze separately, the way tools/corpus-gen does:
			// the loaded configuration is what says which providers this
			// entry needs, and check.Dir hides it between the two calls.
			load := check.Load(ctx, ref.Dir, varFiles...)
			if load.Config != nil {
				schemas, provRows = acq.schemasFor(providerNeeds(load.Config))
			}
			rep = check.Analyze(ctx, load.Config, check.Context{Schemas: schemas})
			rep.Load = load
		}

		// Marks refusals that read a required variable with no value. It
		// only marks; it changes no verdict and no count.
		unsetSites := rep.AttributeUnsetVariables(rep.Load.UnsetVariables(), rep.Load.Sources())

		e := entry{
			Name:             ref.Name,
			Origin:           ref.Origin,
			Readable:         rep.Readable(),
			Blocked:          rep.Blocked(),
			Sites:            rep.Sites(),
			Instances:        rep.Instances,
			Shadowed:         rep.Shadowed,
			Unresolved:       len(rep.Load.UnresolvedModules),
			UnsetVarSites:    unsetSites,
			Refusals:         map[string]int{},
			Providers:        provRows,
			ModulesInstalled: modulesInstalled(ref.Dir),
			VarFiles:         ref.VarFiles,
		}
		for _, f := range rep.Findings {
			e.Refusals[f.ID] += len(f.Sites)
			r.Layers[f.ID] = string(f.Layer)
			for _, s := range f.Sites {
				cause := causes.site(f.Layer, s, f.ID)
				if e.Causes == nil {
					e.Causes = map[string]map[string]int{}
				}
				if e.Causes[f.ID] == nil {
					e.Causes[f.ID] = map[string]int{}
				}
				e.Causes[f.ID][cause]++
				if opts.verbose && opts.only != "" {
					fmt.Printf("%s\t%s:%d\t%s\t%s\n", ref.Name, s.File, s.Line, f.ID, cause)
				}
			}
		}
		for _, f := range rep.Warnings {
			if e.Warnings == nil {
				e.Warnings = map[string]int{}
			}
			e.Warnings[f.ID] += len(f.Sites)
			r.Totals.Warnings += len(f.Sites)
		}

		if opts.onboarded {
			measureOnboarded(ctx, &e, ref.Dir, varFiles, schemas, rep, causes, r, opts)
		}

		r.Entries = append(r.Entries, e)

		r.Totals.Entries++
		if e.Readable {
			r.Totals.Readable++
		}
		if e.Blocked {
			r.Totals.Blocked++
		}
		r.Totals.Sites += e.Sites
		r.Totals.Instances += e.Instances
		r.Totals.Shadowed += e.Shadowed
		r.Totals.Unresolved += e.Unresolved
		r.Totals.UnsetVarSites += e.UnsetVarSites

		// Partial acquisition, counted per entry rather than folded into a
		// run-wide "did any provider load". An entry whose google schema
		// failed while its random schema succeeded is not a schema-backed
		// measurement of that entry, and the run-wide view cannot say so.
		if acq != nil && len(provRows) > 0 {
			var present int
			for _, p := range provRows {
				if p.Present {
					present++
				}
			}
			switch {
			case present == 0:
				r.Totals.EntriesNoSchemas++
			case present < len(provRows):
				r.Totals.EntriesPartialSchemas++
			}
		}
	}

	if acq != nil {
		r.Providers = acq.results()
	}

	if r.Totals.Entries == 0 {
		// Zero entries with an exit status of 0 is the failure this whole
		// file is about, in its smallest form: -entry with a typo prints a
		// summary of nothing and looks like a configuration that refuses
		// nothing.
		if opts.only != "" {
			return nil, fmt.Errorf("-entry %q matched none of the %d configurations the manifest resolves to.\n"+
				"The filter is a substring of the entry name as it appears in the artifact, e.g. \".corpus/vpc\" or \"live/e2e/estates\".\n"+
				"Run without -entry to list what is there.", opts.only, len(refs))
		}
		return nil, fmt.Errorf("the manifest resolved to no configurations at all under %s; nothing was measured", r.RootPath)
	}

	// The per-source accounting and the sweep must agree, or the sweep's own
	// account of what it measured is wrong - which is worse than not having
	// one, because it is the thing a later reader would check against.
	if opts.only == "" {
		var accounted int
		for _, s := range r.Sources {
			accounted += s.Entries
		}
		if accounted != r.Totals.Entries {
			return nil, fmt.Errorf("the probe's per-source accounting says %d configurations and the sweep measured %d.\n"+
				"corpusState in corpus.go duplicates check.Manifest.Resolve's matching rules and has drifted from them; fix corpusState, not this message.",
				accounted, r.Totals.Entries)
		}
	}

	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].Name < r.Entries[j].Name })
	return r, nil
}

func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

func summarize(r *run, wrote string) {
	byID := map[string]int{}
	cfgs := map[string]int{}
	for _, e := range r.Entries {
		for id, n := range e.Refusals {
			byID[id] += n
			cfgs[id]++
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return byID[ids[i]] > byID[ids[j]] })

	summarizeTree(r)
	fmt.Printf("entries %d  readable %d  blocked %d  sites %d  instances %d  shadowed %d  warnings %d\n",
		r.Totals.Entries, r.Totals.Readable, r.Totals.Blocked,
		r.Totals.Sites, r.Totals.Instances, r.Totals.Shadowed, r.Totals.Warnings)
	fmt.Printf("unresolved module calls %d - every count above is a floor for the entries carrying them\n",
		r.Totals.Unresolved)
	fmt.Printf("refused sites reading an unset required variable: %d - those may be artifacts of the missing value\n",
		r.Totals.UnsetVarSites)
	summarizeSchemaBound(r)
	fmt.Println()
	for _, id := range ids {
		fmt.Printf("%5d sites %4d cfg  %-10s %s\n", byID[id], cfgs[id], r.Layers[id], id)
	}
	summarizeCauses(r)
	summarizeOnboarding(r)
	if wrote != "" {
		fmt.Printf("\nwrote %s\n", wrote)
	}
}

// summarizeTree says what was measured before saying what was found. A
// summary that opened with a total was how "entries 31" got read as a sweep
// of the corpus: the number was fine, the corpus under it was 12% of the one
// the reader assumed.
func summarizeTree(r *run) {
	commit := r.Commit
	if commit == "" {
		commit = "(not a git checkout)"
	}
	var withModules int
	for _, e := range r.Entries {
		if e.ModulesInstalled {
			withModules++
		}
	}
	fmt.Printf("tree %s at %s\n", r.RootPath, commit)
	fmt.Printf("corpus %d source(s), %d entries measured, %d with modules installed\n",
		len(r.Sources), r.Totals.Entries, withModules)
	if len(r.CorpusProblems) > 0 {
		fmt.Printf("PARTIAL CORPUS - %d problem(s), measured anyway under -allow-partial-corpus:\n", len(r.CorpusProblems))
		for _, p := range r.CorpusProblems {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  every total below is about this corpus, not the one the manifest describes")
	}
}

// summarizeSchemaBound states the run's own bound, in both directions. A
// schema-less run says what it cannot see; a schema-backed one says what it
// actually acquired, because "we had schemas" is not the same claim as "we
// had the schemas this corpus needed".
func summarizeSchemaBound(r *run) {
	if !r.Schemas {
		fmt.Println("schemas false - undercounts refusals, overcounts what a fix clears; verify per-entry before reporting")
		fmt.Println("  the stamp layer contributes nothing at all in this mode, and a non-AWS estate reads as")
		fmt.Println("  unadmitted-type throughout for a reason belonging to the run. Use -schemas for either.")
		return
	}

	var available, unavailable int
	for _, p := range r.Providers {
		if p.Available {
			available++
			continue
		}
		unavailable++
	}
	fmt.Printf("schemas true - %d provider requirement(s) acquired, %d not (init-bin %s)\n",
		available, unavailable, r.InitBin)
	fmt.Printf("  entries with SOME provider missing: %d; with NO provider acquired: %d\n",
		r.Totals.EntriesPartialSchemas, r.Totals.EntriesNoSchemas)
	if unavailable > 0 {
		fmt.Println("  unavailable:")
		for _, p := range r.Providers {
			if p.Available {
				continue
			}
			fmt.Printf("    %-40s %s\n", p.Provider, firstLine(p.Error))
		}
		fmt.Println("  a refusal on a type one of these serves is an artifact of the acquisition, not of the")
		fmt.Println("  configuration - read the per-entry providers list before believing any entry above")
	}
	var floating []string
	for _, p := range r.Providers {
		if !p.Pinned && !p.Locked {
			floating = append(floating, p.Provider+"@"+p.Constraint)
		}
	}
	if len(floating) > 0 {
		fmt.Printf("  %d requirement(s) not in the lock file, so resolved to whatever is current today: %s\n",
			len(floating), strings.Join(floating, ", "))
	}
}

// summarizeCauses prints the per-cause breakdown under each refusal ID that
// has one. The count alone was never the question a probe was built to
// answer.
func summarizeCauses(r *run) {
	byCause := map[string]map[string]int{}
	for _, e := range r.Entries {
		for id, causes := range e.Causes {
			if byCause[id] == nil {
				byCause[id] = map[string]int{}
			}
			for cause, n := range causes {
				byCause[id][cause] += n
			}
		}
	}
	ids := make([]string, 0, len(byCause))
	for id := range byCause {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if r.Layers[ids[i]] != r.Layers[ids[j]] {
			return r.Layers[ids[i]] < r.Layers[ids[j]]
		}
		return ids[i] < ids[j]
	})

	fmt.Println("\nby cause:")
	const maxRows = 12
	for _, id := range ids {
		causes := byCause[id]
		// A single unexplained bucket says nothing the site count did not.
		if len(causes) == 1 {
			if _, only := causes[""]; only {
				continue
			}
		}
		fmt.Printf("  %s %s\n", r.Layers[id], id)
		keys := make([]string, 0, len(causes))
		for k := range causes {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if causes[keys[i]] != causes[keys[j]] {
				return causes[keys[i]] > causes[keys[j]]
			}
			return keys[i] < keys[j]
		})
		shown := keys
		if len(shown) > maxRows {
			shown = shown[:maxRows]
		}
		for _, k := range shown {
			label := k
			if label == "" {
				label = "(no structured cause on the site)"
			}
			fmt.Printf("    %6d  %s\n", causes[k], label)
		}
		if rest := len(keys) - len(shown); rest > 0 {
			var restSites int
			for _, k := range keys[len(shown):] {
				restSites += causes[k]
			}
			fmt.Printf("    %6d  ... across %d more cause(s), in the JSON\n", restSites, rest)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func runDiff(spec string) error {
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		return fmt.Errorf("-diff wants before.json,after.json")
	}
	before, err := load(parts[0])
	if err != nil {
		return err
	}
	after, err := load(parts[1])
	if err != nil {
		return err
	}
	if err := diffPreconditions(parts[0], before, parts[1], after); err != nil {
		return err
	}

	fmt.Printf("before %s at %s\nafter  %s at %s\n\n",
		before.RootPath, commitOrUnknown(before.Commit), after.RootPath, commitOrUnknown(after.Commit))
	fmt.Printf("sites      %d -> %d  (%+d)\n", before.Totals.Sites, after.Totals.Sites,
		after.Totals.Sites-before.Totals.Sites)
	fmt.Printf("instances  %d -> %d  (%+d)\n", before.Totals.Instances, after.Totals.Instances,
		after.Totals.Instances-before.Totals.Instances)
	fmt.Printf("blocked    %d -> %d  (%+d)\n", before.Totals.Blocked, after.Totals.Blocked,
		after.Totals.Blocked-before.Totals.Blocked)
	// Unresolved module calls belong beside the totals rather than in a
	// footnote: every count above is a floor for the entries carrying them,
	// so a pair of sweeps that disagree here disagree about how much of the
	// corpus was visible at all.
	fmt.Printf("unresolved %d -> %d  (%+d) module calls\n", before.Totals.Unresolved, after.Totals.Unresolved,
		after.Totals.Unresolved-before.Totals.Unresolved)
	fmt.Println()

	// Per-ID, because sites moving between IDs at a constant total is this
	// codebase's most common fix outcome and an aggregate hides it.
	bID, aID := byID(before), byID(after)
	ids := union(bID, aID)
	fmt.Println("by refusal ID:")
	var moved bool
	for _, id := range ids {
		if bID[id] == aID[id] {
			continue
		}
		moved = true
		fmt.Printf("  %+6d  %5d -> %-5d  %s\n", aID[id]-bID[id], bID[id], aID[id], id)
	}
	if !moved {
		fmt.Println("  (nothing)")
	}

	// Per-entry regressions get their own section. A fix that clears sites
	// overall while making one entry worse is the shape that has cost this
	// project real instances, and it must not be reported as a win.
	bE, aE := byEntry(before), byEntry(after)
	var worse, better []string
	for name, a := range aE {
		b, ok := bE[name]
		if !ok {
			continue
		}
		switch {
		case a.Sites > b.Sites || a.Instances < b.Instances:
			worse = append(worse, fmt.Sprintf("  %s  sites %d -> %d, instances %d -> %d",
				name, b.Sites, a.Sites, b.Instances, a.Instances))
		case a.Sites < b.Sites || a.Instances > b.Instances:
			better = append(better, fmt.Sprintf("  %s  sites %d -> %d, instances %d -> %d",
				name, b.Sites, a.Sites, b.Instances, a.Instances))
		}
	}
	sort.Strings(worse)
	sort.Strings(better)

	fmt.Printf("\nentries improved: %d\n", len(better))
	for _, s := range better {
		fmt.Println(s)
	}
	fmt.Printf("\nentries WORSE: %d\n", len(worse))
	for _, s := range worse {
		fmt.Println(s)
	}
	if len(worse) > 0 {
		fmt.Println("\nan entry losing instances is a resolution the change took away; do not report the")
		fmt.Println("aggregate without these beside it")
	}
	diffEnvironment(before, after)

	if before.Schemas {
		fmt.Println("\nschemas true in both runs. Check the per-entry providers lists agree too: a provider that")
		fmt.Println("acquired in one run and not the other moves sites for a reason belonging to the run.")
		fmt.Printf("entries with a provider missing: %d -> %d; with none: %d -> %d\n",
			before.Totals.EntriesPartialSchemas, after.Totals.EntriesPartialSchemas,
			before.Totals.EntriesNoSchemas, after.Totals.EntriesNoSchemas)
	} else {
		fmt.Println("\nschemas false in both runs - upper bound, verify per-entry with real schemas")
	}
	return nil
}

func commitOrUnknown(commit string) string {
	if commit == "" {
		return "(not a git checkout)"
	}
	return commit
}

// diffPreconditions is everything that has to agree before a subtraction
// means anything.
//
// The rule each of these encodes is the same one: an input that differs
// between two sweeps and is not the change under test will show up in the
// delta wearing the change's name. -diff already refused a schema-less run
// against a schema-backed one on exactly that reasoning. These are the rest
// of the inputs, found by asking what else the sweep reads from its
// environment.
func diffPreconditions(beforeName string, before *run, afterName string, after *run) error {
	// Version first. Every check below reads a field that did not exist in
	// version 1, and an absent field unmarshals to the zero value, which
	// compares equal to the other side's absent field. A guard that passes
	// because both sides are silent is the shape being fixed here, not one
	// to reintroduce.
	// Ordered deliberately: a map here would name whichever file Go's
	// iteration happened to reach first, so the same two inputs would
	// produce two different messages across runs.
	for _, side := range []struct {
		name string
		r    *run
	}{{beforeName, before}, {afterName, after}} {
		name, r := side.name, side.r
		if r.ProbeVersion < probeVersion {
			return fmt.Errorf("%s was written by refusal-probe format %d, and this is format %d.\n"+
				"The older format did not record the tree, the manifest's contents or the corpus it measured, so the "+
				"checks that make a comparison meaningful cannot run and would pass by reading absent fields as agreement.\n"+
				"Re-run the sweep to regenerate it.", name, r.ProbeVersion, probeVersion)
		}
	}

	if before.Manifest != after.Manifest {
		return fmt.Errorf("these measure different manifests (%q vs %q); the comparison would be meaningless",
			before.Manifest, after.Manifest)
	}
	if before.ManifestSHA != after.ManifestSHA {
		return fmt.Errorf("both runs name %s but its contents differ (%s vs %s).\n"+
			"The manifest decides which directories are measured and with which tfvars files, so the two sweeps "+
			"have different populations under one name. Re-run both against one manifest.",
			before.Manifest, short(before.ManifestSHA), short(after.ManifestSHA))
	}
	// A schema-less run and a schema-backed one are not two measurements of
	// the same thing. The schema-less one cannot see the stamp layer at all,
	// reports a non-AWS estate as unadmitted throughout, and reads any rule
	// that needs a schema as false. Subtracting one from the other produces
	// a number with no meaning, and it would look exactly like a large win.
	if before.Schemas != after.Schemas {
		return fmt.Errorf("one run has schemas and the other does not (%s: %v, %s: %v); "+
			"they measure different things and a mixed comparison is worse than none - re-run so both agree",
			beforeName, before.Schemas, afterName, after.Schemas)
	}
	// The onboarded axis, for the same reason as the schema axis. The
	// published-form fields an -onboarded sweep writes are identical to a
	// default sweep's, so this guard is not about them: it is about a
	// reader picking the onboarded totals out of one file and the published
	// totals out of the other. -diff compares the PUBLISHED form on both
	// sides and says so, and refusing the mixed pair keeps that sentence
	// true of every file it will ever print.
	if before.OnboardedForm != after.OnboardedForm {
		return fmt.Errorf("one run measured the onboarded form and the other did not (%s: %v, %s: %v).\n"+
			"-diff compares the published form on both sides, so the pair would compare fine and answer a question neither file was asked. "+
			"Re-run so both agree, or read the onboarded columns out of one -onboarded sweep, which measures both forms itself",
			beforeName, before.OnboardedForm, afterName, after.OnboardedForm)
	}
	if before.Schemas && before.PinVersion != after.PinVersion {
		return fmt.Errorf("the two schema-backed runs pinned %s at different versions (%q vs %q).\n"+
			"That is a comparison of two provider releases with a code change mixed in. Re-run both at one version.",
			before.PinSource, before.PinVersion, after.PinVersion)
	}
	if before.Schemas && before.PinSource != after.PinSource {
		return fmt.Errorf("the two schema-backed runs pinned different providers (%q vs %q); re-run both against one",
			before.PinSource, after.PinSource)
	}

	// Root, resolved. The unresolved field could not do this job: it
	// defaults to "." and two sweeps of two different worktrees both record
	// ".", so a guard on it compares equal and says nothing - which is
	// precisely how two trees came to be compared silently.
	if before.RootPath != after.RootPath {
		return fmt.Errorf("these sweeps measured two different trees:\n"+
			"  %s: %s (at %s)\n"+
			"  %s: %s (at %s)\n"+
			"Each tree has its own .corpus, its own manifest and its own uncommitted work, so the delta carries "+
			"all of that and not only the change under test.\n"+
			"Re-run both sweeps against one tree - the probe writes wherever -out points, so before and after can "+
			"live side by side in the same checkout.",
			beforeName, before.RootPath, commitOrUnknown(before.Commit),
			afterName, after.RootPath, commitOrUnknown(after.Commit))
	}

	// Corpus completeness. A partial sweep is allowed to exist - that is
	// what -allow-partial-corpus is for - but it can only be compared
	// against a sweep that was partial in the same way.
	if !sameStrings(before.CorpusProblems, after.CorpusProblems) {
		return fmt.Errorf("the two sweeps saw different corpora on disk.\n"+
			"  %s: %s\n"+
			"  %s: %s\n"+
			"A source present for one sweep and absent for the other contributes its whole refusal count to the "+
			"delta. Re-run both with the same corpus - go run ./tools/corpus-fetch.",
			beforeName, problemSummary(before.CorpusProblems),
			afterName, problemSummary(after.CorpusProblems))
	}

	// The entry sets themselves. byEntry skips a name it cannot find on
	// both sides, so unequal populations previously produced a per-entry
	// section over the intersection and totals over the union - and a
	// 250-entry sweep against a 31-entry one reads as an enormous win.
	onlyBefore, onlyAfter := entryDiff(before, after)
	if len(onlyBefore) > 0 || len(onlyAfter) > 0 {
		return fmt.Errorf("the two sweeps measured different sets of configurations (%d and %d entries).\n"+
			"  only in %s: %s\n"+
			"  only in %s: %s\n"+
			"The totals above each other would be over different populations. If one side used -entry, re-run it "+
			"without; otherwise the corpus changed between the sweeps.",
			before.Totals.Entries, after.Totals.Entries,
			beforeName, sample(onlyBefore),
			afterName, sample(onlyAfter))
	}

	return nil
}

// diffEnvironment reports the inputs that are allowed to differ but change
// what the numbers mean. None of these is fatal: a provider that failed to
// install on one run and not the other is normal, and so is a module tree
// that was installed between two sweeps. What is not acceptable is that
// happening silently, which is what used to happen.
func diffEnvironment(before, after *run) {
	bE, aE := byEntry(before), byEntry(after)

	var modulesChanged, varFilesChanged []string
	for name, a := range aE {
		b, ok := bE[name]
		if !ok {
			continue
		}
		if b.ModulesInstalled != a.ModulesInstalled {
			modulesChanged = append(modulesChanged, fmt.Sprintf("  %s  modules installed %v -> %v, unresolved %d -> %d, sites %d -> %d",
				name, b.ModulesInstalled, a.ModulesInstalled, b.Unresolved, a.Unresolved, b.Sites, a.Sites))
		}
		if !sameStrings(b.VarFiles, a.VarFiles) {
			varFilesChanged = append(varFilesChanged, fmt.Sprintf("  %s  var files %v -> %v", name, b.VarFiles, a.VarFiles))
		}
	}
	sort.Strings(modulesChanged)
	sort.Strings(varFilesChanged)

	if len(modulesChanged) > 0 {
		fmt.Printf("\nMODULE INSTALL STATE CHANGED on %d entry(s) - their site deltas are that, not your change:\n", len(modulesChanged))
		for _, s := range modulesChanged {
			fmt.Println(s)
		}
		fmt.Println("one corpus entry moved 59 -> 394 refused sites on this input alone")
	}
	if len(varFilesChanged) > 0 {
		fmt.Printf("\nVAR FILES CHANGED on %d entry(s) - a refusal that was only an unset variable will move here:\n", len(varFilesChanged))
		for _, s := range varFilesChanged {
			fmt.Println(s)
		}
	}

	if !before.Schemas {
		return
	}
	bP := map[string]providerResult{}
	for _, p := range before.Providers {
		bP[p.Provider+"@"+p.Constraint] = p
	}
	var moved []string
	for _, a := range after.Providers {
		b, ok := bP[a.Provider+"@"+a.Constraint]
		if !ok {
			moved = append(moved, fmt.Sprintf("  %s %s  not required by the before run", a.Provider, a.Constraint))
			continue
		}
		switch {
		case b.Available != a.Available:
			moved = append(moved, fmt.Sprintf("  %s %s  acquired %v -> %v (%s)", a.Provider, a.Constraint, b.Available, a.Available, firstLine(a.Error)))
		case b.Version != a.Version:
			moved = append(moved, fmt.Sprintf("  %s %s  version %s -> %s", a.Provider, a.Constraint, b.Version, a.Version))
		}
	}
	sort.Strings(moved)
	if len(moved) > 0 {
		fmt.Printf("\nPROVIDER ACQUISITION CHANGED on %d requirement(s) - the entries needing them measured different schemas:\n", len(moved))
		for _, s := range moved {
			fmt.Println(s)
		}
		fmt.Println("a refusal on a type one of these serves moved for a reason belonging to the run, not to the change")
	}
}

// entryDiff is the names present in exactly one of two sweeps.
func entryDiff(before, after *run) (onlyBefore, onlyAfter []string) {
	bE, aE := byEntry(before), byEntry(after)
	for name := range bE {
		if _, ok := aE[name]; !ok {
			onlyBefore = append(onlyBefore, name)
		}
	}
	for name := range aE {
		if _, ok := bE[name]; !ok {
			onlyAfter = append(onlyAfter, name)
		}
	}
	sort.Strings(onlyBefore)
	sort.Strings(onlyAfter)
	return onlyBefore, onlyAfter
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sample renders at most a few names, with a count for the rest, so a
// refusal message stays readable when a whole corpus is on one side.
func sample(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	const max = 5
	shown := names
	var rest int
	if len(shown) > max {
		rest = len(shown) - max
		shown = shown[:max]
	}
	s := strings.Join(shown, ", ")
	if rest > 0 {
		s += fmt.Sprintf(", and %d more", rest)
	}
	return s
}

func problemSummary(problems []string) string {
	if len(problems) == 0 {
		return "the full corpus"
	}
	return fmt.Sprintf("%d problem(s): %s", len(problems), sample(problems))
}

func load(path string) (*run, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

func byID(r *run) map[string]int {
	m := map[string]int{}
	for _, e := range r.Entries {
		for id, n := range e.Refusals {
			m[id] += n
		}
	}
	return m
}

func byEntry(r *run) map[string]entry {
	m := make(map[string]entry, len(r.Entries))
	for _, e := range r.Entries {
		m[e.Name] = e
	}
	return m
}

func union(a, b map[string]int) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di := b[out[i]] - a[out[i]]
		dj := b[out[j]] - a[out[j]]
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}
