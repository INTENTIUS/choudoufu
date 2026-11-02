// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package modcompat builds live/module-compat.json, the module compatibility
// table in its two tiers (GitHub issue #2292).
//
// The static tier is one row per terraform-aws-modules example, measured by
// tools/refusal-probe in the onboarded form (-schemas -onboarded): the
// configuration a user runs after adding a live block, and the one every
// gauntlet script runs. Its rows come from a sweep file, so regenerating the
// static tier means running the probe again; see tools/modcompat-gen.
//
// The gauntlet tier is one row per terraform-popular estate in
// live/gauntlet.json whose last run is current: measured against the pinned
// emulator, AWS provider, stock oracle and engine base, with a script that has
// not changed since (the board's script_stale). A row that fails any of those
// is left out, so the array lists recorded current runs only. It is a pure
// function of committed files, and `go run ./tools/gauntlet render` refreshes
// it.
//
// The two tiers share no figure. Nothing in the file adds a static row to a
// gauntlet row or reports a combined total, by the issue's ruling: a static
// clear says live-check plans the example offline, and a gauntlet pass says
// a recorded run applied it against the emulator. Those are different claims.
package modcompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/docsref"
)

// Paths, relative to the repository root.
const (
	Rel           = "live/module-compat.json"
	ManifestRel   = "live/corpus-manifest.json"
	ArtifactRel   = "live/gauntlet.json"
	BoardRel      = "live/gauntlet-board.json"
	SchemaVersion = 1
)

// The tiers, as each row spells its own.
const (
	TierStatic   = "static"
	TierGauntlet = "gauntlet"
)

// Origin is the manifest origin the static tier draws from, and Lane the
// gauntlet lane the gauntlet tier draws from.
const (
	Origin = "terraform-aws-modules"
	Lane   = "terraform-popular"
)

// The static verdicts.
const (
	VerdictClear      = "clear"
	VerdictRefused    = "refused"
	VerdictUnmeasured = "unmeasured"
)

// DocsBase is where a refusal's documentation reference is linked to.
const DocsBase = "https://github.com/INTENTIUS/choudoufu/blob/main/"

// StaticCommand is the command that regenerates the static tier, and
// GauntletCommand the one that refreshes the gauntlet tier.
const (
	StaticCommand   = "refusal-probe -schemas -onboarded -out sweep.json, then go run ./tools/modcompat-gen -sweep sweep.json"
	GauntletCommand = "go run ./tools/gauntlet render"
)

// File is live/module-compat.json.
type File struct {
	Schema  int      `json:"schema"`
	Comment []string `json:"_comment"`

	// StaticMeasurement says what the static rows were measured with. It
	// is nil only before the first sweep.
	StaticMeasurement *StaticMeasurement `json:"static_measurement"`

	Static   []StaticRow   `json:"static"`
	Gauntlet []GauntletRow `json:"gauntlet"`
}

// StaticMeasurement is the provenance every static row shares.
type StaticMeasurement struct {
	Date       string `json:"date"`
	Commit     string `json:"commit"`
	Instrument string `json:"instrument"`
	Form       string `json:"form"`
	Schemas    bool   `json:"schemas"`
	SecondPass bool   `json:"second_pass"`

	// AWSProvider is the hashicorp/aws release the probe held every entry
	// at (its -provider-version).
	AWSProvider string `json:"aws_provider"`

	// Sources are the manifest's terraform-aws-modules sources at the time
	// of the sweep. live/module_compat_test.go holds them to the manifest,
	// so a re-pinned or added module makes the static tier stale loudly.
	Sources []Source `json:"sources"`
}

// Source is one module repository at one tag.
type Source struct {
	Module   string `json:"module"`
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Glob     string `json:"glob"`
	Examples int    `json:"examples"`
}

// StaticRow is one example in the onboarded form.
type StaticRow struct {
	Tier    string `json:"tier"`
	Module  string `json:"module"`
	Version string `json:"version"`
	Example string `json:"example"`

	Date        string `json:"date"`
	Commit      string `json:"commit"`
	AWSProvider string `json:"aws_provider"`

	Verdict string `json:"verdict"`

	// Reason is why a row is unmeasured. Empty otherwise.
	Reason string `json:"reason,omitempty"`

	// Reads counts the sites that resolve at plan time through an eligible
	// data-source read (check.Report.Reads). They never make a row
	// refused, and they never appear under Refusals (#2295).
	Reads int `json:"reads"`

	Refusals []Refusal `json:"refusals"`

	// MissingProviders names the providers whose schemas the probe could
	// not acquire for this example. A row with any is a partial-schema
	// measurement.
	MissingProviders []string `json:"missing_providers,omitempty"`
}

// Refusal is one refusal ID that fired on a row, with its site count and
// the document that explains it.
type Refusal struct {
	ID      string `json:"id"`
	Layer   string `json:"layer"`
	Sites   int    `json:"sites"`
	DocsRef string `json:"docs_ref,omitempty"`
	DocsURL string `json:"docs_url,omitempty"`
}

// GauntletRow is one current recorded gauntlet run of a module example.
type GauntletRow struct {
	Tier    string `json:"tier"`
	Module  string `json:"module"`
	Version string `json:"version"`
	Example string `json:"example"`
	Estate  string `json:"estate"`

	Date        string  `json:"date"`
	Commit      string  `json:"commit"`
	Emulator    string  `json:"emulator"`
	AWSProvider string  `json:"aws_provider"`
	Engine      string  `json:"engine"`
	Oracle      Oracle  `json:"oracle"`
	Clear       bool    `json:"clear"`
	Stages      []Stage `json:"stages"`
}

// Oracle is the stock binaries a gauntlet run compared against.
type Oracle struct {
	Terraform string `json:"terraform"`
	Tofu      string `json:"tofu"`
}

// Stage is one gauntlet stage's verdict.
type Stage struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Verdict string `json:"verdict"`
}

// Comment is the file's _comment, written on every generation.
var Comment = []string{
	"Generated, never hand-edited (GitHub issue #2292). Two tiers, which share no figure:",
	"",
	"static: one row per terraform-aws-modules example in live/corpus-manifest.json,",
	"measured by tools/refusal-probe in the onboarded form (-schemas -onboarded), the",
	"configuration a user runs after adding a live block. clear means live-check plans",
	"it offline; reads counts eligible data-source reads, which never refuse. Regenerate",
	"with: " + StaticCommand + ".",
	"",
	"gauntlet: one row per terraform-popular estate in live/gauntlet.json whose last run",
	"is current against every pin and whose script has not changed since. A row that is",
	"not current is left out. Refresh with: " + GauntletCommand + ".",
}

// Read loads the committed file at root. A missing file reads as an empty
// one, so the first generation can start from nothing.
func Read(root string) (*File, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Rel)))
	if os.IsNotExist(err) {
		return &File{Schema: SchemaVersion, Static: []StaticRow{}, Gauntlet: []GauntletRow{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", Rel, err)
	}
	return &f, nil
}

// Encode renders f the way it is committed.
func Encode(f *File) ([]byte, error) {
	f.Schema = SchemaVersion
	f.Comment = Comment
	if f.Static == nil {
		f.Static = []StaticRow{}
	}
	if f.Gauntlet == nil {
		f.Gauntlet = []GauntletRow{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write writes f to root and reports whether the bytes changed.
func Write(root string, f *File) (bool, error) {
	b, err := Encode(f)
	if err != nil {
		return false, err
	}
	p := filepath.Join(root, filepath.FromSlash(Rel))
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, b) {
		return false, nil
	}
	return true, os.WriteFile(p, b, 0o644)
}

// RefreshGauntlet rebuilds the gauntlet tier from the committed artifact and
// board, keeps the static tier as committed, and writes the file. It is what
// `tools/gauntlet render` calls.
func RefreshGauntlet(root string) (bool, error) {
	f, err := Read(root)
	if err != nil {
		return false, err
	}
	rows, err := GauntletRows(root)
	if err != nil {
		return false, err
	}
	f.Gauntlet = rows
	return Write(root, f)
}

// ModuleFromRepo turns a terraform-aws-modules clone URL into its registry
// address: https://github.com/terraform-aws-modules/terraform-aws-vpc.git is
// terraform-aws-modules/vpc/aws.
func ModuleFromRepo(repo string) (string, bool) {
	m := moduleRepo.FindStringSubmatch(repo)
	if m == nil {
		return "", false
	}
	return "terraform-aws-modules/" + m[1] + "/aws", true
}

var moduleRepo = regexp.MustCompile(`^https://github\.com/terraform-aws-modules/terraform-aws-([a-z0-9-]+?)(?:\.git)?/?$`)

// ManifestSources returns the manifest's terraform-aws-modules sources at
// root, in manifest order, with Examples left zero. A source of that origin
// with no fetch block, no tag or an unrecognized repository is an error: the
// static tier cannot name a module and version for it.
func ManifestSources(root string) ([]Source, error) {
	m, err := check.ReadManifest(filepath.Join(root, filepath.FromSlash(ManifestRel)))
	if err != nil {
		return nil, err
	}
	var out []Source
	for _, s := range m.Sources {
		if s.Origin != Origin {
			continue
		}
		if s.Fetch == nil || s.Fetch.Tag == "" || s.Fetch.Commit == "" {
			return nil, fmt.Errorf("%s: source %s has origin %q and no fetch tag and commit; the static tier cannot name its version", ManifestRel, s.Glob, Origin)
		}
		mod, ok := ModuleFromRepo(s.Fetch.Repo)
		if !ok {
			return nil, fmt.Errorf("%s: source %s has origin %q but repository %s is not a terraform-aws-modules module repository", ManifestRel, s.Glob, Origin, s.Fetch.Repo)
		}
		out = append(out, Source{
			Module:  mod,
			Repo:    strings.TrimSuffix(s.Fetch.Repo, ".git"),
			Version: s.Fetch.Tag,
			Commit:  s.Fetch.Commit,
			Glob:    s.Glob,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s has no %q source", ManifestRel, Origin)
	}
	return out, nil
}

// DocsURL links a documentation reference (docsref form) to its first
// heading on GitHub. It returns "" for a reference that does not parse.
func DocsURL(ref string) string {
	r, err := docsref.Parse(ref)
	if err != nil {
		return ""
	}
	u := DocsBase + r.Doc
	if len(r.Headings) > 0 {
		u += "#" + Anchor(r.Headings[0])
	}
	return u
}

// Anchor is GitHub's heading anchor: lower case, every character that is not
// a letter, digit, space, hyphen or underscore dropped, spaces made hyphens.
func Anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// refusalDocs maps (layer, id) to the refusal's documentation reference,
// from the same registries live/LIMITATIONS.md is generated from.
func refusalDocs() map[[2]string]string {
	out := map[[2]string]string{}
	for _, r := range check.AllRefusals() {
		out[[2]string{string(r.Layer), r.ID}] = r.DocsRef
	}
	return out
}

// isEligibleRead reports whether a refusal ID under a layer is the
// data-read pass's eligible read, the one finding that never blocks
// (check.Finding.EligibleRead).
func isEligibleRead(layer, id string) bool {
	return layer == string(check.LayerDataread) && id == dataread.SummaryEligibleRead
}

func sortRefusals(rs []Refusal) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Sites != rs[j].Sites {
			return rs[i].Sites > rs[j].Sites
		}
		return rs[i].ID < rs[j].ID
	})
}
