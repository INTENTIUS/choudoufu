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
		initBin      = flag.String("init-bin", "", "binary used to install the provider for schema reading; empty runs without schemas")
		provSource   = flag.String("provider-source", "hashicorp/aws", "provider to read schemas from")
		provVersion  = flag.String("provider-version", pins.AWSProviderVersion, "exact provider version to pin (default: internal/live/pins.AWSProviderVersion, the same pin survey-gen builds the admission evidence from - #117)")
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

	schemas, schemaNote, err := acquireSchemas(*initBin, *provSource, *provVersion, *noSchemas, logOut)
	if err != nil {
		return err
	}

	ctx := context.Background()
	corpus := check.NewCorpus()
	origins := map[string]int{}

	for _, entry := range entries {
		logf(logOut, "corpus-gen: %s\n", entry.Name)
		report := check.Dir(ctx, entry.Dir, check.Context{Schemas: schemas})
		corpus.Add(entry.Name, entry.Origin, report)
		origins[entry.Origin]++
	}
	corpus.Finish()

	artifact := Artifact{
		Corpus:     corpus,
		SchemaNote: schemaNote,
		Origins:    originRows(origins),
	}

	out := underRoot(*root, *outPath)
	if err := writeArtifact(out, artifact); err != nil {
		return err
	}
	logf(logOut, "corpus-gen: wrote %s\n", out)

	fmt.Print(renderTable(artifact))
	return nil
}

// Artifact is the generated file. It carries the corpus fold plus the two
// facts a reader needs before trusting a number in it: whether provider
// schemas were available, and where the configurations came from.
type Artifact struct {
	*check.Corpus

	// SchemaNote says whether schemas were read and from what. Without
	// them, types the provider's identity schema would have admitted read
	// as refused, and "unadmitted-type" tops the ranking for a reason that
	// is an artifact of the run rather than a property of the corpus.
	SchemaNote SchemaNote `json:"schemas"`

	// Origins counts the corpus by where its configurations came from.
	// In-repo fixtures measure this project's own idea of what works; only
	// third-party configurations measure the product promise.
	Origins []OriginCount `json:"origins"`
}

// SchemaNote records the schema situation behind a run.
type SchemaNote struct {
	Present  bool   `json:"present"`
	Provider string `json:"provider,omitempty"`
	Version  string `json:"version,omitempty"`
	Types    int    `json:"resource_types,omitempty"`

	// Caveat is the plain-language consequence, carried in the artifact so
	// that a reader who opens the file without reading this program is
	// told what the numbers are worth.
	Caveat string `json:"caveat,omitempty"`
}

// OriginCount is one origin's share of the corpus.
type OriginCount struct {
	Origin  string `json:"origin"`
	Configs int    `json:"configs"`
}

// acquireSchemas reads the provider's resource type schemas, or explains why
// the run has none.
//
// Running without them is supported and is worth less, in one specific
// direction: every resource type absent from the generated admission table
// reads as refused, so "unadmitted-type" tops the ranking for a reason that
// belongs to the run rather than to the corpus. That is the single outcome
// #102 exists to prevent, so the artifact carries the caveat rather than
// leaving a reader to infer it.
func acquireSchemas(initBin, source, pinnedVersion string, disabled bool, logOut *os.File) (map[string]providers.Schema, SchemaNote, error) {
	note := SchemaNote{
		Caveat: "Run without provider schemas. Resource types were judged from the generated admission table alone, so types the provider's own identity schema would have admitted are counted as refused here and the unadmitted-type rules are overstated.",
	}

	if disabled || initBin == "" {
		return nil, note, nil
	}
	if pinnedVersion == "" {
		return nil, note, fmt.Errorf("-provider-version is required with -init-bin")
	}

	provider, diags := addrs.ParseProviderSourceString(source)
	if diags.HasErrors() {
		return nil, note, fmt.Errorf("parsing -provider-source %q: %w", source, diags.Err())
	}

	workdir, err := os.MkdirTemp("", "corpus-gen-schemas")
	if err != nil {
		return nil, note, err
	}
	defer os.RemoveAll(workdir)

	var logWriter *os.File
	if logOut != nil {
		logWriter = logOut
	}

	schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
		InitBin:  initBin,
		WorkDir:  workdir,
		Source:   source,
		Version:  pinnedVersion,
		Provider: provider,
		Log:      logWriter,
	})
	if err != nil {
		return nil, note, fmt.Errorf("reading provider schemas: %w", err)
	}

	return schemas, SchemaNote{
		Present:  true,
		Provider: source,
		Version:  pinnedVersion,
		Types:    len(schemas),
	}, nil
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
// corpus and the same provider release must produce a byte-identical file,
// the property every other generated artifact in live/ holds.
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

	b.WriteString("\nCorpus origins:\n")
	for _, origin := range artifact.Origins {
		fmt.Fprintf(&b, "    %-24s %d\n", origin.Origin, origin.Configs)
	}

	if !artifact.SchemaNote.Present {
		fmt.Fprintf(&b, "\n%s\n", artifact.SchemaNote.Caveat)
	}

	fmt.Fprintf(&b, "\nChecked: %s. Not checked: %s.\n",
		strings.Join(layerNames(artifact.Checked), ", "),
		strings.Join(layerNames(artifact.Unchecked), ", "))
	b.WriteString("The unchecked stages each need a cloud. Nothing above says a corpus entry applies cleanly.\n")

	return b.String()
}

// underRoot resolves a path given on the command line against -root, leaving
// an absolute path alone so that a caller can write the artifact anywhere.
func underRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func layerNames(layers []check.Layer) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		out = append(out, string(layer))
	}
	return out
}

func logf(out *os.File, format string, args ...any) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, format, args...)
}
