// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command corpus-gen measures which live-path refusals actually fire on real
// OpenTofu configurations, and writes the ranked table as a generated
// artifact.
//
// This is GitHub issue #102. The product promise is that people's existing
// OpenTofu works under live markers with narrow exceptions, and until this
// ran, nothing in the repository measured that: live/rowgen-convergence.json
// measures whether a generator agrees with a human-ratified table row, which
// is generator-autonomy debt rather than anything a user experiences. The
// output here is meant to replace every row count in the tracker as the
// thing work is prioritized by.
//
// It shares its whole analysis with "choudoufu live-check" (#114) - both are
// front ends over internal/live/check, one folding many configurations into
// a ranking and one rendering a verdict for a person. That is deliberate: if
// they were separate implementations, the compatibility claim this project
// publishes and the answer a user gets about their own repository would
// eventually disagree.
//
// # Providers, honestly (#211)
//
// Every entry gets schemas for the providers ITS OWN configuration declares
// or implies - never a single global map handed to every entry regardless
// of what it actually uses. Before #211 this program acquired
// hashicorp/aws once and passed that same map to every entry, so a
// google_*, tfe_*, postgresql_*, elasticsearch_*, sentry_* or restapi_*
// type could never reach the schema fallback
// (identity.SynthesizeTypeIdentity, wired in at admission.go:26-35): it
// read as unadmitted for a reason belonging to the run rather than to the
// corpus, which is the exact outcome this file's own #102 exists to
// prevent, one level up.
//
// "Which providers does this estate declare" is answered by
// [*configs.Config.ProviderRequirements], the same recursive resolver a
// real "tofu init" calls to decide what to install: it walks every module
// in the tree the loader could read, folding in both explicit
// required_providers entries and the implicit dependency every resource
// creates from its type prefix. That is the honest source because it is
// not this program's own idea of what a "declared" or "inferred" provider
// is - it is what installing this exact configuration would already do.
//
// Acquisition is cached by (provider FQN, version constraint) across the
// WHOLE corpus, not per entry: 250-odd entries share a much smaller set of
// distinct provider requirements, and many entries need no fetch at all
// because an earlier entry already satisfied the same one. One provider is
// still exempted into an exact pin - not by name in any control flow, but
// by whichever provider -provider-source/-provider-version name (default
// hashicorp/aws, unchanged from before #211): that keeps the schema
// fallback's view of that one provider locked to the same release
// tools/survey-gen built the generated admission table's own evidence from
// (#117), so the dominant AWS-heavy share of this corpus does not start
// reading spurious provider-version-skew warnings against a table it never
// moved. Every other required provider - there is no hardcoded list of
// them, whatever [getproviders.Requirements] resolves is what gets tried -
// is acquired using its own configuration's version constraint, or with no
// constraint at all when the configuration named none, exactly the choice
// "tofu init" would make for that same operator - the FIRST time this
// program ever sees that (provider, constraint) pair. #222: whatever
// version that resolves to is then locked into
// live/corpus-provider-pins.json (see [providerPin]), and every later run
// requests that exact version instead of asking the registry to resolve
// "latest" again, so the corpus a person reads today does not silently
// become a blend of however many releases happened to be current on
// whatever day each entry's schemas were last fetched. Updating a lock -
// deliberately picking up a new release - means editing or deleting that
// provider's entry in the pins file, the same deliberateness a bump to the
// AWS pin's own version already requires.
//
// A provider that cannot be fetched (no network, unpublished, a private
// registry) is recorded as a third state, per entry, in
// [check.CorpusEntry.ProviderSchemas]: distinct from both "schemas were off
// for the whole run" and "the fallback ran and lost" - see
// [check.EntryProviderSchema]. It is locked too, by its error text rather
// than a version it never successfully resolved.
//
// # What the corpus is, and what it is not
//
// The corpus is whatever the manifest names. The manifest checked in here
// seeds it with this repository's own estate fixtures, which are useful for
// exercising the instrument and are NOT field data: they were written by
// this project, against what this project already admits, so a ranking over
// them measures the fixtures. Every entry carries an origin, every artifact
// repeats the origin breakdown, and the honest reading of a run over
// in-repo fixtures alone is "the instrument works", not "this is what real
// configurations hit". Point -manifest at real estates for that.
package main

import (
	"github.com/intentius/choudoufu/internal/live/pins"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/getproviders"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "corpus-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		manifestPath = flag.String("manifest", "live/corpus-manifest.json", "corpus manifest to read")
		outPath      = flag.String("out", "live/corpus-refusals.json", "generated artifact to write")
		root         = flag.String("root", ".", "repository root that relative manifest paths are resolved against")
		initBin      = flag.String("init-bin", "", "binary used to install providers for schema reading; empty runs without schemas")
		pinSource    = flag.String("provider-source", "hashicorp/aws", "the one provider pinned to an exact version (see -provider-version) rather than acquired at whatever version each entry's own configuration constrains it to (#211); every other provider any entry declares or implies is still acquired, generically, from that entry's own required_providers")
		pinVersion   = flag.String("provider-version", pins.AWSProviderVersion, "exact version pinned for -provider-source (default: internal/live/pins.AWSProviderVersion, the same pin survey-gen builds the admission evidence from - #117)")
		pinsPath     = flag.String("provider-pins", "live/corpus-provider-pins.json", "checked-in lock file (#222): every other provider's resolved version is read from here when present, instead of floating to whatever its own constraint resolves to today; a (provider, constraint) pair seen for the first time is acquired at its own constraint and appended here")
		noSchemas    = flag.Bool("no-schemas", false, "run without provider schemas, and say so in the artifact")
		quiet        = flag.Bool("quiet", false, "suppress the progress log")
	)
	flag.Parse()

	logOut := os.Stderr
	if *quiet {
		logOut = nil
	}

	manifest, err := check.ReadManifest(underRoot(*root, *manifestPath))
	if err != nil {
		return err
	}

	entries, err := manifest.Resolve(*root)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("the manifest matched no directories; nothing to measure")
	}

	var pinned addrs.Provider
	if *pinVersion != "" {
		p, diags := addrs.ParseProviderSourceString(*pinSource)
		if diags.HasErrors() {
			return fmt.Errorf("parsing -provider-source %q: %w", *pinSource, diags.Err())
		}
		pinned = p
	}
	pinsFile := underRoot(*root, *pinsPath)
	basePins, err := loadProviderPins(pinsFile)
	if err != nil {
		return err
	}
	acquirer := newSchemaAcquirer(*initBin, *noSchemas, pinned, *pinVersion, basePins, logOut)

	ctx := context.Background()
	corpus := check.NewCorpus()
	origins := map[string]int{}

	for _, entry := range entries {
		logf(logOut, "corpus-gen: %s\n", entry.Name)

		var varFiles []string
		for _, vf := range entry.VarFiles {
			varFiles = append(varFiles, underRoot(*root, vf))
		}

		// Load first, analyze second - [check.Dir] does exactly these two
		// calls in sequence, but this program needs the loaded configuration
		// in between them to work out which providers IT declares, so the
		// two steps are inlined here rather than hidden behind Dir.
		load := check.Load(ctx, entry.Dir, varFiles...)

		var schemas map[string]providers.Schema
		var schemaRows []check.EntryProviderSchema
		if load.Config != nil {
			schemas, schemaRows = acquirer.schemasFor(providerNeeds(load.Config))
		}

		report := check.Analyze(ctx, load.Config, check.Context{Schemas: schemas})
		report.Load = load
		// Attribution before folding, so a rate-capable entry's profile can
		// flag the refusals that are artifacts of running without the
		// operator's tfvars (#161, #175). It only marks; it never changes a
		// verdict or a count.
		report.AttributeUnsetVariables(report.Load.UnsetVariables(), report.Load.Sources())
		corpus.Add(entry.Name, entry.Origin, report, entry.VarFiles...)
		if last := corpus.LastEntry(); last != nil {
			last.ProviderSchemas = schemaRows
		}
		origins[entry.Origin]++
	}
	corpus.Finish()

	artifact := Artifact{
		Corpus:     corpus,
		SchemaNote: acquirer.note(*noSchemas, *initBin),
		Origins:    originRows(origins),
	}

	out := underRoot(*root, *outPath)
	if err := writeArtifact(out, artifact); err != nil {
		return err
	}
	logf(logOut, "corpus-gen: wrote %s\n", out)

	if !*noSchemas && *initBin != "" {
		newPins := acquirer.mergedPins(basePins)
		if err := writeProviderPins(pinsFile, newPins); err != nil {
			return err
		}
		logf(logOut, "corpus-gen: wrote %s (%d locked)\n", pinsFile, len(newPins))
	}

	fmt.Print(renderTable(artifact))
	return nil
}

// Artifact is the generated file. It carries the corpus fold plus the two
// facts a reader needs before trusting a number in it: whether provider
// schemas were available, and where the configurations came from.
type Artifact struct {
	*check.Corpus

	// SchemaNote says whether schemas were read, and honestly records every
	// provider any corpus entry actually needed (#211) - never a single
	// global provider standing in for all of them. Without a provider's
	// schemas, types absent from the generated admission table that ONLY
	// that provider's own identity schema would have admitted read as
	// refused, and "unadmitted-type" tops the ranking for a reason that is
	// an artifact of the run rather than a property of the corpus.
	SchemaNote SchemaNote `json:"schemas"`

	// Origins counts the corpus by where its configurations came from.
	// In-repo fixtures measure this project's own idea of what works; only
	// third-party configurations measure the product promise.
	Origins []OriginCount `json:"origins"`
}

// SchemaNote records the schema situation behind a run: whether schemas
// were attempted at all, and one row per distinct provider (by FQN and
// version constraint) any corpus entry declared or implied.
type SchemaNote struct {
	Present bool `json:"present"`

	// Providers is every distinct provider this run's corpus needed,
	// across every entry, each acquired at most once (#211's caching unit).
	// Ranked to a stable order: available first (most types first), then
	// unavailable, ties broken by provider name.
	Providers []ProviderSchemaResult `json:"providers,omitempty"`

	// Caveat is the plain-language consequence, carried in the artifact so
	// that a reader who opens the file without reading this program is
	// told what the numbers are worth.
	Caveat string `json:"caveat,omitempty"`
}

// ProviderSchemaResult is one provider's schema-acquisition outcome for the
// whole run, keyed by (provider, constraint) - the same key
// [schemaAcquirer] caches acquisition by.
type ProviderSchemaResult struct {
	// Provider is the FQN for display ("hashicorp/google").
	Provider string `json:"provider"`

	// Constraint is the version constraint this run resolved the provider
	// under: the canonical form of whatever the requiring entry's own
	// required_providers declared, or empty for "no constraint - whatever
	// init resolves as latest", which is what an implicit-only dependency
	// (no required_providers entry at all) gets.
	Constraint string `json:"constraint,omitempty"`

	// Version is the exact release this run actually acquired, when
	// Available. For the one pinned provider (-provider-source /
	// -provider-version) this is the pin itself; for every other provider
	// it is read back from what "init" actually installed
	// ([pluginschema.InstalledVersion]), since an unconstrained or ranged
	// requirement does not by itself say which release that will be.
	Version string `json:"version,omitempty"`

	// Types is how many resource type schemas this provider contributed.
	Types int `json:"resource_types,omitempty"`

	// Pinned is true for the one provider -provider-source/-provider-version
	// names (default hashicorp/aws) - see the package doc comment for why
	// that one provider keeps an exact pin rather than floating with
	// whatever a corpus entry's own constraint (or lack of one) resolves
	// to.
	Pinned bool `json:"pinned,omitempty"`

	// Available is whether this run actually has this provider's resource
	// type schemas to offer the schema fallback.
	Available bool `json:"available"`

	// Error is why not, when Available is false - #211's third state, told
	// apart from "schemas were off for the whole run" (which never
	// produces a row here at all: see [SchemaNote.Present]) and from "the
	// fallback ran and the type still isn't admitted" (Available true).
	Error string `json:"error,omitempty"`

	// Locked is true when (Provider, Constraint) was already present in
	// live/corpus-provider-pins.json before this run started, so
	// acquisition used that checked-in outcome instead of resolving the
	// requirement fresh (#222). False marks a (provider, constraint) pair
	// this run saw for the first time: it floated to whatever "latest"
	// (or the matching release for a ranged constraint) resolves to today,
	// and got appended to the pins file for every run after this one to
	// lock to - see the package doc comment on [providerPin].
	Locked bool `json:"locked"`
}

// OriginCount is one origin's share of the corpus.
type OriginCount struct {
	Origin  string `json:"origin"`
	Configs int    `json:"configs"`
}

// providerNeed is one provider this run must try to acquire schemas for,
// and the version constraint (if any) the requiring configuration itself
// declared.
type providerNeed struct {
	Provider   addrs.Provider
	Constraint string
}

func (n providerNeed) key() string {
	return n.Provider.String() + "@" + n.Constraint
}

// providerNeeds resolves the providers one loaded configuration declares or
// implies, using [*configs.Config.ProviderRequirements] - the full-tree
// resolver real "tofu init" itself calls, folding in both explicit
// required_providers entries and the implicit dependency every resource's
// type prefix creates. Built-in providers (only "terraform" today) are
// excluded: they need no schema fetch, and every type they carry
// (terraform_data) is already in the generated admission table directly.
//
// Diagnostics from ProviderRequirements (an invalid version constraint
// string, for instance) are not fatal here: whatever the resolver DID
// manage to populate is still worth trying to acquire, and the load-level
// diagnostics this entry's report already carries are where a reader
// learns the configuration itself has a problem.
func providerNeeds(cfg *configs.Config) []providerNeed {
	reqs, _, _ := cfg.ProviderRequirements()

	out := make([]providerNeed, 0, len(reqs))
	for provider, constraints := range reqs {
		if provider.IsZero() || provider.IsBuiltIn() {
			continue
		}
		out = append(out, providerNeed{
			Provider:   provider,
			Constraint: getproviders.VersionConstraintsString(constraints),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func logf(out *os.File, format string, args ...any) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, format, args...)
}

func underRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func originRows(counts map[string]int) []OriginCount {
	out := make([]OriginCount, 0, len(counts))
	for origin, n := range counts {
		out = append(out, OriginCount{Origin: origin, Configs: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Configs != out[j].Configs {
			return out[i].Configs > out[j].Configs
		}
		return out[i].Origin < out[j].Origin
	})
	return out
}

// writeArtifact writes the JSON. Sorted lists, no timestamps: the same
// corpus and the same provider releases must produce a byte-identical file
// given the same acquisition results, the property every other generated
// artifact in live/ holds.
//
// Before #222 this was true in theory but not in practice: every provider
// except the one flag-pinned by -provider-source/-provider-version could
// float to whatever "latest" resolved to on the day of the run, so two
// runs on an otherwise unchanged tree, days apart, could disagree with no
// code change and no test failure. live/corpus-provider-pins.json (see
// [providerPin]) closes that: once a (provider, constraint) pair has been
// acquired once, every later run requests that exact recorded version
// instead of letting it float, so a corpus regenerated on an unchanged
// tree - including an unchanged pins file - now reproduces byte-for-byte,
// and [TestEveryAcquiredProviderIsLocked] in live/pins_drift_test.go
// checks it against that checked-in file rather than against itself.
func writeArtifact(path string, artifact Artifact) error {
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o600)
}

// renderTable prints the ranking for a human standing at the terminal. The
// artifact is the durable output; this is so the person who just ran it can
// see the answer.
func renderTable(artifact Artifact) string {
	var b strings.Builder

	totals := artifact.Totals
	fmt.Fprintf(&b, "\n%d configuration(s), %d unreadable.\n", totals.Configs, totals.Configs-totals.Loaded)
	for _, pop := range artifact.Populations {
		fmt.Fprintf(&b, "  %s: %d configuration(s), %d blocked, %d clean (%s - not a compatibility rate)\n",
			pop.Origin, pop.Configs, pop.Blocked, pop.Clean, pop.ReadsAs)
	}
	fmt.Fprintf(&b, "%d of %d known refusals fired, across %d site(s) and %d resolved instance(s).\n",
		totals.RefusalsFired, totals.RefusalsInSet, totals.Sites, totals.Instances)

	fmt.Fprintf(&b, "\n%-6s %-6s %-9s %s\n", "CONFIGS", "SITES", "LAYER", "REFUSAL")
	for _, refusal := range artifact.Refusals {
		if refusal.Configs == 0 {
			continue
		}
		fmt.Fprintf(&b, "%-7d %-6d %-9s %s\n", refusal.Configs, refusal.Sites, refusal.Layer, refusal.ID)
	}

	if totals.RefusalsFired < totals.RefusalsInSet {
		fmt.Fprintf(&b, "\n%d refusal(s) fired on nothing in this corpus. They are in the artifact with a zero,\n",
			totals.RefusalsInSet-totals.RefusalsFired)
		b.WriteString("which is the half of the answer an instrument built from observed output cannot have.\n")
	}

	if ladder := artifact.Ladder; ladder != nil {
		fmt.Fprintf(&b, "\nOnboarding ladder over the rate-capable population(s) (%s):\n",
			strings.Join(ladder.Origins, ", "))
		for _, row := range ladder.Classes {
			fmt.Fprintf(&b, "    %-26s %d\n", row.Class, row.Configs)
		}
		if len(ladder.UnadmittedDemand) > 0 {
			b.WriteString("Unadmitted-type demand (configs declaring each type):\n")
			shown := ladder.UnadmittedDemand
			const maxDemandRows = 20
			if len(shown) > maxDemandRows {
				shown = shown[:maxDemandRows]
			}
			for _, row := range shown {
				fmt.Fprintf(&b, "    %-4d %s\n", row.Configs, row.Type)
			}
			if rest := len(ladder.UnadmittedDemand) - len(shown); rest > 0 {
				fmt.Fprintf(&b, "    ... and %d more in the artifact\n", rest)
			}
		}
	}

	b.WriteString("\nCorpus origins:\n")
	for _, origin := range artifact.Origins {
		fmt.Fprintf(&b, "    %-24s %d\n", origin.Origin, origin.Configs)
	}

	if !artifact.SchemaNote.Present {
		fmt.Fprintf(&b, "\n%s\n", artifact.SchemaNote.Caveat)
	} else {
		b.WriteString("\nProviders acquired for this run:\n")
		for _, p := range artifact.SchemaNote.Providers {
			switch {
			case p.Available:
				fmt.Fprintf(&b, "    %-40s %-12s %d types\n", p.Provider, p.Version, p.Types)
			default:
				fmt.Fprintf(&b, "    %-40s UNAVAILABLE: %s\n", p.Provider, p.Error)
			}
		}
	}

	// Same shape as internal/command/views' live-check report, and empty for
	// the same reason: Unchecked is a list that can be emptied, and "Not
	// checked: ." is what the unconditional version prints when it is.
	fmt.Fprintf(&b, "\nChecked: %s.", strings.Join(layerNames(artifact.Checked), ", "))
	if len(artifact.Unchecked) > 0 {
		fmt.Fprintf(&b, " Not checked: %s.", strings.Join(layerNames(artifact.Unchecked), ", "))
	}
	b.WriteString("\n")
	for _, partial := range artifact.Partial {
		// Named on its own line rather than folded into either list above.
		// A partly checked stage read as checked overstates the run by
		// however many of its refusals still need a cloud, and read as
		// unchecked understates it by the ones this run computed; the share
		// is the only rendering that is true of both.
		fmt.Fprintf(&b, "Partly checked: %s - %d of %d refusals (%s); the rest need a cloud.\n",
			partial.Layer, len(partial.Refusals), partial.Total,
			strings.Join(partial.Refusals, "; "))
	}
	if len(artifact.Unchecked) > 0 {
		b.WriteString("The unchecked stages each need a cloud. ")
	}
	b.WriteString("Nothing above says a corpus entry applies cleanly.\n")

	return b.String()
}

func layerNames(layers []check.Layer) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		out = append(out, string(layer))
	}
	return out
}

// schemaAcquirer acquires provider resource-type schemas, memoized by
// (provider, version constraint) across the whole corpus run (#211): many
// entries share the same provider requirement, and the caching unit is that
// requirement, not the entry - 250-odd entries collapse to however many
// distinct (provider, constraint) pairs the corpus actually contains.
type schemaAcquirer struct {
	initBin  string
	disabled bool

	// pinned and pinnedVersion name the one provider given an exact-version
	// override (-provider-source/-provider-version), matched structurally
	// against whatever [providerNeed.Provider] a caller asks for - not by
	// comparing any name in control flow. See the package doc comment for
	// why AWS keeps this by default.
	pinned        addrs.Provider
	pinnedVersion string

	// pins is the checked-in lock table (#222, live/corpus-provider-pins.json)
	// loaded once before the run starts. acquire consults it, by
	// (provider, constraint) rather than by any name in control flow, for
	// every requirement the flag pin above does not already cover; it is
	// never mutated during the run; see [schemaAcquirer.mergedPins] for how
	// a first-seen requirement gets added to the file afterward.
	pins providerPins

	logOut *os.File

	cache map[string]acquireResult
}

// acquireResult is one (provider, constraint) pair's outcome, cached by
// [schemaAcquirer.key].
type acquireResult struct {
	Provider   string
	Constraint string
	Version    string
	Types      int
	Pinned     bool
	Locked     bool
	Available  bool
	Error      string
	Schemas    map[string]providers.Schema
}

func newSchemaAcquirer(initBin string, disabled bool, pinned addrs.Provider, pinnedVersion string, pins providerPins, logOut *os.File) *schemaAcquirer {
	if pins == nil {
		pins = providerPins{}
	}
	return &schemaAcquirer{
		initBin:       initBin,
		disabled:      disabled,
		pinned:        pinned,
		pinnedVersion: pinnedVersion,
		pins:          pins,
		logOut:        logOut,
		cache:         map[string]acquireResult{},
	}
}

// schemasFor returns the merged resource-type schema map covering every
// available provider in needs, plus one status row per provider - the
// per-entry honesty #211 asks for, since a caller folding the merged map
// alone back into [check.Report] loses which specific provider (if any)
// came up short.
func (a *schemaAcquirer) schemasFor(needs []providerNeed) (map[string]providers.Schema, []check.EntryProviderSchema) {
	if a.disabled || a.initBin == "" || len(needs) == 0 {
		return nil, nil
	}

	merged := map[string]providers.Schema{}
	rows := make([]check.EntryProviderSchema, 0, len(needs))
	for _, need := range needs {
		res := a.acquire(need)
		rows = append(rows, check.EntryProviderSchema{
			Provider: res.Provider,
			Present:  res.Available,
			Error:    res.Error,
		})
		if res.Available {
			for typeName, schema := range res.Schemas {
				merged[typeName] = schema
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })
	if len(merged) == 0 {
		merged = nil
	}
	return merged, rows
}

// acquire runs (or replays from cache) one provider's schema fetch.
//
// The pinned provider (-provider-source/-provider-version) is canonicalized
// to one cache slot regardless of what version constraint the requiring
// entry itself declared, before the cache key is computed: two
// terraform-aws-modules examples with different ">= x.y" floors on the same
// hashicorp/aws still both get the identical pinned release, so they must
// not fragment into separate acquisitions (and separate "tofu init" runs)
// just because their own constraint text differs. Every other provider
// keeps its own entry's constraint as part of the key, since those really
// can resolve to different releases - unless [schemaAcquirer.pins] (#222)
// already recorded what that constraint resolved to on a previous run, in
// which case that recorded version is requested exactly, the same way the
// flag pin forces one, so a floating or unconstrained requirement stops
// floating the moment it has been seen once.
func (a *schemaAcquirer) acquire(need providerNeed) acquireResult {
	pinned := a.pinnedVersion != "" && need.Provider == a.pinned
	if pinned {
		need.Constraint = ""
	}

	key := need.key()
	if res, ok := a.cache[key]; ok {
		return res
	}

	res := acquireResult{
		Provider:   need.Provider.ForDisplay(),
		Constraint: need.Constraint,
		Pinned:     pinned,
	}

	req := pluginschema.Request{
		InitBin:    a.initBin,
		Source:     need.Provider.ForDisplay(),
		Constraint: need.Constraint,
		Provider:   need.Provider,
		Log:        a.logOut,
	}
	switch {
	case pinned:
		req.Version = a.pinnedVersion
		req.Constraint = ""
	default:
		if lock, ok := a.pins[pinKey(res.Provider, res.Constraint)]; ok {
			res.Locked = true
			if lock.Available && lock.Version != "" {
				req.Version = lock.Version
				req.Constraint = ""
			}
			// lock.Available == false: no version was ever successfully
			// resolved to lock to. Retry at the original constraint and
			// expect the same deterministic failure (see the doc comment
			// on [providerPin]) - the drift test compares the error text,
			// not a version, for this case.
		}
	}

	workdir, err := os.MkdirTemp("", "corpus-gen-schemas")
	if err != nil {
		res.Error = err.Error()
		a.cache[key] = res
		return res
	}
	defer os.RemoveAll(workdir)
	req.WorkDir = workdir

	schemas, err := pluginschema.ResourceTypes(context.Background(), req)
	if err != nil {
		res.Error = err.Error()
		a.cache[key] = res
		return res
	}

	res.Available = true
	res.Schemas = schemas
	res.Types = len(schemas)
	res.Version = req.Version
	if res.Version == "" {
		if v, ok := pluginschema.InstalledVersion(workdir, need.Provider); ok {
			res.Version = v
		}
	}

	a.cache[key] = res
	return res
}

// note builds the artifact-level [SchemaNote] from everything this
// acquirer tried over the whole run.
func (a *schemaAcquirer) note(noSchemas bool, initBin string) SchemaNote {
	if noSchemas || initBin == "" {
		return SchemaNote{
			Caveat: "Run without provider schemas. Resource types were judged from the generated admission table alone, so types a provider's own identity schema would have admitted are counted as refused here and the unadmitted-type rules are overstated.",
		}
	}

	note := SchemaNote{Present: true}
	for _, res := range a.cache {
		note.Providers = append(note.Providers, ProviderSchemaResult{
			Provider:   res.Provider,
			Constraint: res.Constraint,
			Version:    res.Version,
			Types:      res.Types,
			Pinned:     res.Pinned,
			Locked:     res.Locked,
			Available:  res.Available,
			Error:      res.Error,
		})
	}
	sort.Slice(note.Providers, func(i, j int) bool {
		a, b := note.Providers[i], note.Providers[j]
		if a.Available != b.Available {
			return a.Available
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Constraint < b.Constraint
	})
	if len(note.Providers) == 0 {
		note.Caveat = "No corpus entry declared or implied any provider, so nothing was acquired."
	}
	return note
}

// mergedPins returns the pins file this run should write: base plus one
// new entry for every (provider, constraint) pair this run acquired that
// base did not already have (#222). An existing entry is never overwritten
// - if this run's result for an already-locked key disagrees with base
// (the locked version stopped resolving, a previously broken platform
// build now exists), that disagreement is left for the drift test to
// catch, not silently absorbed here.
func (a *schemaAcquirer) mergedPins(base providerPins) providerPins {
	merged := make(providerPins, len(base)+len(a.cache))
	for k, v := range base {
		merged[k] = v
	}
	for _, res := range a.cache {
		key := pinKey(res.Provider, res.Constraint)
		if _, exists := merged[key]; exists {
			continue
		}
		merged[key] = providerPin{
			Provider:   res.Provider,
			Constraint: res.Constraint,
			Available:  res.Available,
			Version:    res.Version,
			Error:      res.Error,
		}
	}
	return merged
}
