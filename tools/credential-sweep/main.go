// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command credential-sweep is issue #431's measurement: a provider-wide
// sweep for identity.CredentialMaterial's shape, so the credential exclusion
// class stops being a set of precedents someone happened to notice and
// becomes a number recomputed from the pinned schema.
//
// [identity.CredentialMaterial] (internal/live/identity/located.go) is the
// rule: any attribute the provider marks Sensitive and does not also mark
// Deprecated, anywhere in a resource type's schema - nested blocks and
// nested attribute objects included. It already gates two live decisions
// (LocatedType's record-located route, and internal/live/projection's
// residue classifier), each over its own narrow population. This sweeps
// every resource type hashicorp/aws 6.59.0 ships, so a type neither route
// has looked at yet is not silently outside the count.
//
// For every hit it records what is already known about the type, read from
// the artifacts and tables this fork already maintains - never
// re-classified here, since a second implementation of "is this type
// admitted" would drift from the first:
//
//   - admitted: a row in internal/live/identity.DefaultTable, and, when so,
//     whether the ratified identity's own components ever name the flagged
//     attribute (identity_uses_sensitive_attr) - the same question ruling 5
//     (issue #365 population 2, commit 361e0da9ab) asked of the markerless
//     population, asked here of the whole roster: a type whose recorded
//     identity never touches the sensitive attribute is not excluded by
//     admitting it, whatever the schema sweep alone would suggest.
//   - rejected: a key in tools/row-gen/rejected.json, and its reason's
//     first 200 characters - the veto set the schema-first table route
//     already consults, whatever the veto's actual ground is.
//   - markerless: a member of internal/live/identity.MarkerlessTypes, the
//     record-located route's own population.
//   - taggable / importable: live/survey-full.json's own signals, so a
//     type with an ownership marker or with no route to admission at all
//     reads as what it already is without a second schema read.
//
// Disposing each hit - genuinely credential material, or a false positive
// the rule catches for an unrelated reason - is prose, not data, and lives
// in the issue and the PR that measured it, not in this tool or its
// artifact. This writes the measurement; it rules on nothing.
//
// Usage, from anywhere in the checkout:
//
//	go run ./tools/credential-sweep
//
// Needs network for the provider download (or a warm TF_PLUGIN_CACHE_DIR)
// and a terraform binary on PATH (-init-bin overrides), the same
// requirement tools/survey-gen states for the identical acquisition.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
)

const (
	// outRel is where the full hit list is committed.
	outRel = "live/credential-sweep.json"

	// rejectedRel and surveyFullRel are the two existing artifacts this
	// sweep cross-references rather than re-derives.
	rejectedRel   = "tools/row-gen/rejected.json"
	surveyFullRel = "live/survey-full.json"

	providerSource  = "hashicorp/aws"
	providerVersion = "6.59.0"

	defaultInitBin = "terraform"
)

// repoRoot resolves the checkout's root from this file's own location, the
// same trick tools/survey-gen's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Sweep is the committed artifact: live/credential-sweep.json.
type Sweep struct {
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
	GeneratedBy     string `json:"generated_by"`

	// Rule states the predicate this sweep applied, so the artifact reads
	// on its own without a source dive.
	Rule string `json:"rule"`

	Counts Counts `json:"counts"`
	Hits   []Hit  `json:"hits"`
}

// Counts are the roster-wide totals this sweep measured.
type Counts struct {
	ProviderTypes int `json:"provider_types"`
	Hits          int `json:"hits"`
}

// Hit is one resource type identity.CredentialMaterial flags, with every
// fact this fork already tracks about it - see the package doc comment for
// what each field means and where it is read from.
type Hit struct {
	Type string `json:"type"`

	// SensitiveAttrs is every live (Sensitive, not Deprecated) attribute
	// path that made this type a hit, dotted for a nested block or nested
	// attribute object, sorted.
	SensitiveAttrs []string `json:"sensitive_attrs"`

	Admitted bool `json:"admitted"`

	// IdentityUsesSensitiveAttr is set only when Admitted: whether the
	// ratified row's own IdentityAttrs names one of SensitiveAttrs' top-level
	// segments. False across an admitted type's whole population is the
	// measured false-positive signal ruling 5 established for the
	// markerless population and this sweep re-applies table-wide.
	IdentityUsesSensitiveAttr bool `json:"identity_uses_sensitive_attr,omitempty"`

	Rejected bool `json:"rejected"`
	// RejectedReason is the ledger entry's own Reason, truncated to 200
	// bytes so the artifact stays a census rather than a copy of the
	// ledger's prose.
	RejectedReason string `json:"rejected_reason,omitempty"`

	Markerless bool `json:"markerless"`
	Taggable   bool `json:"taggable"`
	Importable bool `json:"importable"`
}

func main() {
	initBin := flag.String("init-bin", defaultInitBin,
		"binary that downloads the pinned provider (terraform, tofu or choudoufu)")
	flag.Parse()

	if err := run(*initBin); err != nil {
		fmt.Fprintf(os.Stderr, "credential-sweep: %v\n", err)
		os.Exit(1)
	}
}

func run(initBin string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	rejected, err := loadRejected(filepath.Join(root, rejectedRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rejectedRel, err)
	}
	taggable, importable, err := loadSurveySignals(filepath.Join(root, surveyFullRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", surveyFullRel, err)
	}

	workdir, err := os.MkdirTemp("", "credential-sweep-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
		InitBin:  initBin,
		WorkDir:  workdir,
		Source:   providerSource,
		Version:  providerVersion,
		Provider: addrs.NewDefaultProvider("aws"),
		Log:      os.Stderr,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "credential-sweep: %d resource types\n", len(schemas))

	sweep := buildSweep(schemas, rejected, taggable, importable)
	fmt.Fprintf(os.Stderr, "credential-sweep: %d hits\n", len(sweep.Hits))

	data, err := json.MarshalIndent(sweep, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	out := filepath.Join(root, outRel)
	if err := os.WriteFile(out, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "credential-sweep: wrote %s\n", outRel)
	return nil
}

func buildSweep(schemas map[string]providers.Schema, rejected map[string]string, taggable, importable map[string]bool) Sweep {
	var hits []Hit
	for t, s := range schemas {
		if s.Block == nil {
			continue
		}
		attrs := sensitiveAttrs(s.Block)
		if len(attrs) == 0 {
			continue
		}
		_, markerless := identity.MarkerlessTypes[t]
		h := Hit{
			Type:           t,
			SensitiveAttrs: attrs,
			Taggable:       taggable[t],
			Importable:     importable[t],
			Markerless:     markerless,
		}
		if row, ok := identity.DefaultTable[t]; ok {
			h.Admitted = true
			h.IdentityUsesSensitiveAttr = identityUsesSensitiveAttr(row.IdentityAttrs, attrs)
		}
		if reason, ok := rejected[t]; ok {
			h.Rejected = true
			h.RejectedReason = truncate(reason, 200)
		}
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Type < hits[j].Type })

	return Sweep{
		Provider:        providerSource,
		ProviderVersion: providerVersion,
		GeneratedBy:     "tools/credential-sweep (go run ./tools/credential-sweep)",
		Rule: "identity.CredentialMaterial: any attribute the provider marks Sensitive and does not also mark " +
			"Deprecated, anywhere in the resource's schema - nested blocks and nested attribute objects included " +
			"(internal/live/identity/located.go).",
		Counts: Counts{ProviderTypes: len(schemas), Hits: len(hits)},
		Hits:   hits,
	}
}

// identityUsesSensitiveAttr reports whether identityAttrs - a ratified
// row's own [identity.TypeIdentity.IdentityAttrs] - names the top-level
// segment of any path in sensitiveAttrs. A sensitive path is dotted for a
// nested block or object ("credential.oauth2_credential.client_secret");
// an identity attribute is always a top-level argument name
// (internal/live/identity's own Component vocabulary never composes an
// identity from inside a nested block), so comparing top-level segments is
// exact, not an approximation.
func identityUsesSensitiveAttr(identityAttrs, sensitiveAttrs []string) bool {
	want := make(map[string]bool, len(identityAttrs))
	for _, a := range identityAttrs {
		want[a] = true
	}
	for _, s := range sensitiveAttrs {
		top := s
		if i := strings.Index(top, "."); i >= 0 {
			top = top[:i]
		}
		if want[top] {
			return true
		}
	}
	return false
}

// sensitiveAttrs is [identity.CredentialMaterial]'s predicate, walked to
// return the attribute paths that fired rather than a bare bool - the
// evidence the artifact carries per hit. Copied rather than imported
// because the predicate's own walk (walkSchemaAttrs, walkSchemaObjectAttrs)
// is unexported; kept to the same three cases that function visits -
// top-level attributes, nested attribute object types, and nested blocks,
// recursively - so the two answer the identical question.
func sensitiveAttrs(b *configschema.Block) []string {
	var out []string
	var walkBlock func(b *configschema.Block, prefix string)
	var walkObject func(o *configschema.Object, prefix string)

	walkObject = func(o *configschema.Object, prefix string) {
		if o == nil {
			return
		}
		for name, a := range o.Attributes {
			if a == nil {
				continue
			}
			if a.Sensitive && !a.Deprecated {
				out = append(out, prefix+name)
			}
			walkObject(a.NestedType, prefix+name+".")
		}
	}
	walkBlock = func(b *configschema.Block, prefix string) {
		if b == nil {
			return
		}
		for name, a := range b.Attributes {
			if a == nil {
				continue
			}
			if a.Sensitive && !a.Deprecated {
				out = append(out, prefix+name)
			}
			walkObject(a.NestedType, prefix+name+".")
		}
		for name, nb := range b.BlockTypes {
			if nb == nil {
				continue
			}
			walkBlock(&nb.Block, prefix+name+".")
		}
	}
	walkBlock(b, "")
	sort.Strings(out)
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type rejectedArtifact struct {
	Rejected map[string]struct {
		Reason string `json:"reason,omitempty"`
	} `json:"rejected"`
}

// loadRejected reads tools/row-gen/rejected.json's key set and reason text.
// Duplicated rather than imported because tools/row-gen is `package main`,
// the same reason tools/survey-gen and tools/estate-gen each carry their
// own copy of the schema-acquisition step this file also duplicates
// (internal/live/pluginschema's own doc comment).
func loadRejected(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var art rejectedArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(art.Rejected))
	for t, row := range art.Rejected {
		out[t] = row.Reason
	}
	return out, nil
}

type surveyFullArtifact struct {
	Types []struct {
		Type    string `json:"type"`
		Signals struct {
			Taggable   bool `json:"taggable"`
			Importable bool `json:"importable"`
		} `json:"signals"`
	} `json:"types"`
}

// loadSurveySignals reads live/survey-full.json's taggable and importable
// signals for every surveyed type - the same artifact tools/survey-gen -all
// writes, over the provider's entire resource-type roster rather than
// live/SURVEY.md's curated set.
func loadSurveySignals(path string) (taggable, importable map[string]bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, nil, err
	}
	var art surveyFullArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, nil, err
	}
	taggable = make(map[string]bool, len(art.Types))
	importable = make(map[string]bool, len(art.Types))
	for _, t := range art.Types {
		taggable[t.Type] = t.Signals.Taggable
		importable[t.Type] = t.Signals.Importable
	}
	return taggable, importable, nil
}
