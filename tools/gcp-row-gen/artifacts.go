// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Evidence is live/gcp-identity-evidence.json: what the pinned provider
// source says about each resource type's identity, per provider.
type Evidence struct {
	GeneratedBy string `json:"generated_by"`

	// Pins names the source release each provider's evidence was read from.
	Pins map[string]SourcePin `json:"pins"`

	// Types maps a resource type to what each provider serving it says.
	Types map[string]map[string]Source `json:"types"`
}

// SourcePin is one provider repository at the release this tool reads.
type SourcePin struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
}

// Source is one resource type's identity as one provider's source states it.
// Every field is a string or regex copied out of the code, never inferred.
type Source struct {
	// File is the Go file, relative to the provider repository, that
	// registers the type.
	File string `json:"file"`

	// Generation is the generation_type the type's *_meta.yaml records:
	// "mmv1" for a Magic Modules type, "handwritten" otherwise, empty for
	// the IAM types, which carry no meta file.
	Generation string `json:"generation,omitempty"`

	// Kind is "resource" or one of the IAM framework's four: "iam_member",
	// "iam_binding", "iam_policy", "iam_audit_config".
	Kind string `json:"kind"`

	// IDFormat is the first `id, err := tpgresource.ReplaceVars(d, config,
	// "...")` template in the file, the one the generated Create sets as
	// the resource's id.
	IDFormat string `json:"id_format,omitempty"`

	// IDShortened is set when that call is tpgresource.ReplaceVarsForId,
	// which substitutes only the last path segment of each argument's
	// value (GetResourceNameFromSelfLink) and trims "projects/",
	// "regions/" and "zones/" off the scope values. A long-form value
	// written into such an argument names a different id than a row that
	// substitutes the value whole (#2144).
	IDShortened bool `json:"id_shortened,omitempty"`

	// ImportIDFormat is the template the importer passes to ReplaceVars
	// after ParseImportId, the id an imported instance ends up with.
	ImportIDFormat string `json:"import_id_format,omitempty"`

	// ImportFormats are the importer's ParseImportId regexes, in order.
	ImportFormats []string `json:"import_formats,omitempty"`

	// IDFromResponse names the field of an API response the file passes
	// to d.SetId: `if val, ok := gr["gcpResourcename"].(string); ok {
	// d.SetId(val)`. A file that does this names the created object the
	// way the API answers, whatever template its importer rebuilds.
	IDFromResponse string `json:"id_from_response,omitempty"`

	// Defaults are the schema Default values the file declares for the
	// arguments its id format names, keyed by argument.
	Defaults map[string]string `json:"defaults,omitempty"`

	// Derived maps a computed attribute the id format names to the
	// template the provider's own CustomizeDiff computes it from:
	// `diff.SetNew("email", fmt.Sprintf("%s@%s.iam.gserviceaccount.com",
	// aid, proj))` over `aid := diff.Get("account_id")` and `proj :=
	// diff.Get("project")` is "{{account_id}}@{{project}}.iam.gserviceaccount.com".
	Derived map[string]string `json:"derived,omitempty"`

	// IAMParent is an IAM type's parent resource id, as its IdParseFunc
	// reads it: the first GetImportIdQualifiers regex turned into a
	// template, or "{{attr}}" where the function sets attr to the whole id.
	IAMParent string `json:"iam_parent,omitempty"`

	// IAMQualified is the argument the IAM updater producer parses with
	// GetImportIdQualifiers, `d.Get("secret_id").(string)`: written as a
	// long form (projects/{p}/secrets/{s}) or a short one ({s}), it is
	// rebuilt into the canonical parent name rather than substituted as
	// written (#2144).
	IAMQualified string `json:"iam_qualified,omitempty"`

	// IAMQualifiers are the regexes that GetImportIdQualifiers call
	// matches IAMQualified against, in order, unquoted.
	IAMQualifiers []string `json:"iam_qualifiers,omitempty"`

	// IAMComputedPattern and IAMComputedFormat are set instead for a
	// hand-written updater that passes IAMQualified through a
	// computed-name helper (pubsub's GetComputedSubscriptionName): a
	// value matching the pattern is the parent as written, any other is
	// placed into the format, "projects/{{project}}/subscriptions/{{subscription}}",
	// with the provider's project.
	IAMComputedPattern string `json:"iam_computed_pattern,omitempty"`
	IAMComputedFormat  string `json:"iam_computed_format,omitempty"`

	// IAMDefaults are the arguments an IAM type's file fills in from the
	// provider when the resource leaves them unset, read off its calls to
	// tpgresource.GetProject, GetRegion, GetZone and GetLocation.
	IAMDefaults []string `json:"iam_defaults,omitempty"`

	// APIService is the api_service_name the type's *_meta.yaml records:
	// "compute.googleapis.com".
	APIService string `json:"api_service,omitempty"`

	// CAINameFormats are the Cloud Asset Inventory name formats the
	// type's *_meta.yaml records, "//cloudsql.googleapis.com/projects/{{project}}/instances/{{name}}".
	// Magic Modules writes them only where they differ from
	// "//<api_service_name>/<id format>" (its GetCaiAssetNameTemplate).
	CAINameFormats []string `json:"cai_name_formats,omitempty"`

	// DocImportFormats are the formats the "## Import" section of the
	// type's page under website/docs/r lists, in order. For a type whose
	// importer is custom code rather than ParseImportId, they are the only
	// statement of what it accepts.
	DocImportFormats []string `json:"doc_import_formats,omitempty"`

	// IAMGrammar is the IAM framework's import grammar for Kind, as the
	// framework's own error message spells it ("resource_name role member
	// [condition_title]").
	IAMGrammar string `json:"iam_grammar,omitempty"`
}

// Facts is live/gcp-schema-facts.json: the parts of each provider's schema
// the rows are checked against and the identity-schema route reads.
type Facts struct {
	GeneratedBy string                   `json:"generated_by"`
	Providers   map[string]ProviderFacts `json:"providers"`
}

// ProviderFacts is one provider's schema, narrowed.
type ProviderFacts struct {
	Source  string               `json:"source"`
	Version string               `json:"version"`
	Types   map[string]TypeFacts `json:"types"`
}

// TypeFacts is one resource type's schema, narrowed. Attribute flags are
// "R" (required), "O" (optional), "OC" (optional and computed) and "C"
// (computed only).
type TypeFacts struct {
	Attrs map[string]string `json:"a"`

	// Nested maps an attribute with a nested type to its leaf attributes.
	Nested map[string]map[string]string `json:"n,omitempty"`

	// Blocks maps a nested block to its leaf attributes and max_items.
	Blocks map[string]BlockFacts `json:"b,omitempty"`

	// Identity is the resource identity schema: "R" required for import,
	// "O" optional. Absent when the provider serves none for the type.
	Identity map[string]string `json:"i,omitempty"`

	// LabelsPropagate is true when the type's labels map is copied onto
	// what the type creates and never kept by the type itself, as its
	// description says ([markers.GoogleLabelsPropagate]): an instance
	// template's. The attribute flags cannot say it; the description is
	// the schema's only channel for it.
	LabelsPropagate bool `json:"lp,omitempty"`
}

// BlockFacts is one nested block's leaf attributes.
type BlockFacts struct {
	Attrs    map[string]string `json:"a"`
	MaxItems int               `json:"m,omitempty"`
}

func loadInputs(root string) (Evidence, Facts, error) {
	var ev Evidence
	if err := readJSON(root, EvidenceJSONRel, &ev); err != nil {
		return Evidence{}, Facts{}, err
	}
	var facts Facts
	if err := readJSON(root, FactsJSONRel, &facts); err != nil {
		return Evidence{}, Facts{}, err
	}
	if len(ev.Types) == 0 || len(facts.Providers) == 0 {
		return Evidence{}, Facts{}, fmt.Errorf("%s or %s decoded empty; the shape this tool reads has changed", EvidenceJSONRel, FactsJSONRel)
	}
	return ev, facts, nil
}

func readJSON(root, rel string, v any) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decoding %s: %w", rel, err)
	}
	return nil
}

// marshalEvidence renders the evidence one type per line, so a provider
// bump reads as a diff of the types that moved.
func marshalEvidence(ev Evidence) ([]byte, error) {
	var b bytes.Buffer
	gb, _ := json.Marshal(ev.GeneratedBy)
	pins, err := json.Marshal(ev.Pins)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "{\n\"generated_by\": %s,\n\"pins\": %s,\n\"types\": {\n", gb, pins)
	names := sortedKeys(ev.Types)
	for i, t := range names {
		line, err := json.Marshal(ev.Types[t])
		if err != nil {
			return nil, err
		}
		key, _ := json.Marshal(t)
		fmt.Fprintf(&b, "%s: %s", key, line)
		if i < len(names)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n}\n")
	return b.Bytes(), nil
}

// marshalFacts renders the facts one type per line, for the same reason.
func marshalFacts(f Facts) ([]byte, error) {
	var b bytes.Buffer
	gb, _ := json.Marshal(f.GeneratedBy)
	fmt.Fprintf(&b, "{\n\"generated_by\": %s,\n\"providers\": {\n", gb)
	provs := sortedKeys(f.Providers)
	for pi, p := range provs {
		pf := f.Providers[p]
		key, _ := json.Marshal(p)
		src, _ := json.Marshal(pf.Source)
		ver, _ := json.Marshal(pf.Version)
		fmt.Fprintf(&b, "%s: {\n\"source\": %s,\n\"version\": %s,\n\"types\": {\n", key, src, ver)
		names := sortedKeys(pf.Types)
		for i, t := range names {
			line, err := json.Marshal(pf.Types[t])
			if err != nil {
				return nil, err
			}
			tk, _ := json.Marshal(t)
			fmt.Fprintf(&b, "%s: %s", tk, line)
			if i < len(names)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("}\n}")
		if pi < len(provs)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n}\n")
	return b.Bytes(), nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
