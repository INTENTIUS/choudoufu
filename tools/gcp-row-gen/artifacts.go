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

	// OtherDefaults names the arguments the id format names whose schema
	// field declares a Default that is not a string literal (`Default:
	// 443`), which Defaults cannot carry. Either kind rules out rendering
	// the argument as an empty segment when it is unset.
	OtherDefaults []string `json:"other_defaults,omitempty"`

	// AtLeastOneOf is the schema's AtLeastOneOf list on the field of an
	// argument the id format names, keyed by that argument:
	// google_compute_global_network_endpoint's fqdn declares
	// []string{"fqdn", "ip_address"}.
	AtLeastOneOf map[string][]string `json:"at_least_one_of,omitempty"`

	// Derived maps a computed attribute the id format names to the
	// template the provider's own CustomizeDiff computes it from:
	// `diff.SetNew("email", fmt.Sprintf("%s@%s.iam.gserviceaccount.com",
	// aid, proj))` over `aid := diff.Get("account_id")` and `proj :=
	// diff.Get("project")` is "{{account_id}}@{{project}}.iam.gserviceaccount.com".
	Derived map[string]string `json:"derived,omitempty"`

	// IAMParent is an IAM type's parent resource id, as its IdParseFunc
	// reads it: the first GetImportIdQualifiers regex turned into a
	// template, or "{{attr}}" where the function sets attr to the whole id.
	// A hand-written IdParseFunc gives it as its first ParseImportId regex,
	// or as the method its d.SetId calls on a parse helper's result over
	// the arguments it d.Sets from that result. A regex group named for a
	// values-map key, `(?P<serviceId>`, is respelled as the argument the
	// producer copies into that key, service_id (#2145).
	IAMParent string `json:"iam_parent,omitempty"`

	// IAMQualified is the argument the IAM updater producer parses with
	// GetImportIdQualifiers, `d.Get("secret_id").(string)`: written as a
	// long form (projects/{p}/secrets/{s}) or a short one ({s}), it is
	// rebuilt into the canonical parent name rather than substituted as
	// written (#2144).
	IAMQualified string `json:"iam_qualified,omitempty"`

	// IAMQualifiers are the regexes that GetImportIdQualifiers call
	// matches IAMQualified against, in order, unquoted, with each group
	// respelled as IAMParent's are.
	IAMQualifiers []string `json:"iam_qualifiers,omitempty"`

	// IAMComputedPattern and IAMComputedFormat are set instead for a
	// hand-written updater that passes IAMQualified through a
	// computed-name helper (pubsub's GetComputedSubscriptionName): a
	// value matching the pattern is the parent as written, any other is
	// placed into the format, "projects/{{project}}/subscriptions/{{subscription}}",
	// with the provider's project.
	IAMComputedPattern string `json:"iam_computed_pattern,omitempty"`
	IAMComputedFormat  string `json:"iam_computed_format,omitempty"`

	// IAMResourceID is the parent id the IAM updater's GetResourceId
	// returns, the id the framework's create sets before the binding, as
	// a template over the arguments its constructor reads:
	// fmt.Sprintf("projects/%s/datasets/%s", u.project, u.datasetId) over
	// `datasetId: d.Get("dataset_id").(string)` is
	// "projects/{{project}}/datasets/{{dataset_id}}" (#2145).
	// IAMResourceIDUnread is set instead when a piece of it is no argument
	// read as written, and says which: "u.resourceId is set from
	// keyRingId.KeyRingId()".
	IAMResourceID       string `json:"iam_resource_id,omitempty"`
	IAMResourceIDUnread string `json:"iam_resource_id_unread,omitempty"`

	// IAMResourceIDPrefixes are the arguments of IAMResourceID that the
	// updater passes through a helper adding a fixed prefix the value
	// lacks, by argument: {"folder": "folders/"} for CanonicalFolderId,
	// so a folder of "123" is the parent "folders/123".
	IAMResourceIDPrefixes map[string]string `json:"iam_resource_id_prefixes,omitempty"`

	// IAMParseUnset are the values-map keys the IdParseFunc's updater
	// struct reads that nothing in that function sets: "" after every
	// import (parseUnsetKeys, #2145 item 7).
	IAMParseUnset []string `json:"iam_parse_unset,omitempty"`

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

	// The Create* and Importer* fields are read for a resource whose file
	// states no id format, by reading its Create and importer at code
	// level (createread.go, #2145 item 1).
	//
	// CreateID is the id every non-empty d.SetId in the Create builds, as
	// a template; CreateIDAt is where each of those calls is. CreateUnread
	// is set instead when a piece of the id is not read from
	// configuration, and says where and what; CreateMinted when that
	// piece is what the API returned.
	CreateID     string   `json:"create_id,omitempty"`
	CreateIDAt   []string `json:"create_id_at,omitempty"`
	CreateUnread string   `json:"create_unread,omitempty"`
	CreateMinted bool     `json:"create_minted,omitempty"`

	// CreateRaw are the project, region and zone placeholders the Create
	// reads with d.Get, as written, with no provider default.
	// CreateShort are those it keeps only the last path segment of, and
	// CreateTrim maps one it trims a literal prefix off to the prefix.
	// CreateLocated are those read by tpgresource.GetLocation.
	CreateRaw     []string          `json:"create_raw,omitempty"`
	CreateShort   []string          `json:"create_short,omitempty"`
	CreateTrim    map[string]string `json:"create_trim,omitempty"`
	CreateLocated []string          `json:"create_located,omitempty"`

	// Importer is "passthrough" (schema.ImportStatePassthrough), "none",
	// "func" (code of the type's own, at ImporterAt) or "unread".
	// ImporterHash fingerprints that code (importerHash), ImporterFormats
	// are its ParseImportId regexes, and ImporterID the id it sets, empty
	// when it keeps the import string.
	Importer        string   `json:"importer,omitempty"`
	ImporterAt      string   `json:"importer_at,omitempty"`
	ImporterHash    string   `json:"importer_hash,omitempty"`
	ImporterFormats []string `json:"importer_formats,omitempty"`
	ImporterID      string   `json:"importer_id,omitempty"`
	ImporterUnread  string   `json:"importer_unread,omitempty"`

	// IAMGrammar is the IAM framework's import grammar for Kind, as the
	// framework's own error message spells it ("resource_name role member
	// [condition_title]").
	IAMGrammar string `json:"iam_grammar,omitempty"`

	// IdentityImport is how the type's importer reads an identity object
	// (identityimport.go). Nil when the type declares no identity schema.
	IdentityImport *IdentityImport `json:"identity_import,omitempty"`

	// ImportWhole, ImportNameSet and ImportPassthrough are importer shapes
	// the generated importRe does not read (fullname.go, #2145 item 5).
	// ImportWhole is the attribute a custom importer's sole ParseImportId
	// regex, `(?P<name>.+)`, fills with the whole import ID.
	ImportWhole string `json:"import_whole,omitempty"`

	// ImportIDSet is the attribute an importer that does nothing else
	// sets to the whole import ID: `d.Set("name", d.Id())` (#2145 item 3).
	ImportIDSet string `json:"import_id_set,omitempty"`

	// ImportNameSet is set when the importer, after ParseImportId, sets an
	// attribute to a template and gives the instance that attribute's
	// value as its id: `d.Set("name", "projects/{{project}}/...")` then
	// `id, err := tpgresource.ReplaceVars(d, config, d.Get("name").(string))`.
	ImportNameSet *ImportNameSet `json:"import_name_set,omitempty"`

	// ImportPassthrough is set when the importer is
	// schema.ImportStatePassthrough, which sets the import ID as the id and
	// nothing else.
	ImportPassthrough bool `json:"import_passthrough,omitempty"`

	// CreateCollection, CreateNameParam and CreateNameArg are read off a
	// generated Create that sends the new object's id itself
	// (createname.go, #2145 item 3): the create URL's path after the base
	// URL and any API version, "projects/{{project}}/locations/{{location}}/generators";
	// the id field naming that collection's resource, "generatorId", sent
	// as a query parameter or by the encoder in the request body; and the
	// argument it is sent from, "generator_id".
	CreateCollection string `json:"create_collection,omitempty"`
	CreateNameParam  string `json:"create_name_param,omitempty"`
	CreateNameArg    string `json:"create_name_arg,omitempty"`

	// NameFlatten says, for a placeholder of the id format, how the
	// file's flatten function keeps the API's value: "whole" (return v)
	// or "last-segment" (GetResourceNameFromSelfLink). Read only beside
	// CreateNameArg.
	NameFlatten map[string]string `json:"name_flatten,omitempty"`

	// NameCollection is, for a generated resource whose id format is one
	// placeholder whole ("{{name}}"), the path of its Create's URL after
	// the base URL and any API version, with the query dropped:
	// "projects/{{project}}/locations/{{location}}/jobs". The API names
	// the object it creates there under that collection, so it is the
	// shape the placeholder holds, which neither the id format nor an
	// import form "{{name}}" says (createname.go).
	NameCollection string `json:"name_collection,omitempty"`

	// LocationSet says how a generated Create whose id format names
	// {{location}} sets it before building the id (location.go, #2145
	// item 7): "none", "provider-region" (an encoder's
	// tpgresource.GetRegion default), "create-elsewhere" (the id is not
	// built from IDFormat in the Create), or the statement that sets it.
	// LocationSetLine is that statement's line in File.
	LocationSet     string `json:"location_set,omitempty"`
	LocationSetLine int    `json:"location_set_line,omitempty"`

	// ConfigPlaceholders are the id format's placeholders ReplaceVars
	// would fill from transport_tpg.Config's field of that name (or a
	// BasePath) when the resource has no value for them. Any other
	// placeholder the schema lacks renders empty (location.go).
	ConfigPlaceholders []string `json:"config_placeholders,omitempty"`

	// The CreateWrite* fields are what the type's Create sends, read for
	// the ruling of 2026-10-20 (createwrite.go): whether a create at an
	// identity something already holds collides with it.
	//
	// CreateFunc is the function the type's schema.Resource registers as
	// its Create, when this file defines it. CreateMethods are the HTTP
	// methods its SendRequest calls name, in order; CreateCustom the
	// custom method the create URL ends in ("setAddons" for
	// ".../organizations/{{org}}:setAddons"). CreateSetCalls and
	// CreateNewCalls are the API calls it makes whose names begin with a
	// set verb (Set..., set...) or a create verb (Insert, Create, Add),
	// other than on d and tpgresource. CreateAdopts is set when the
	// Create, before any write, returns success from a branch that sets
	// the id and calls nothing but d.Set, d.SetId, logging and
	// tpgresource.SetResourceIdentityAttributes: it takes an object it
	// found instead of creating one.
	CreateFunc     string   `json:"create_func,omitempty"`
	CreateMethods  []string `json:"create_methods,omitempty"`
	CreateCustom   string   `json:"create_custom,omitempty"`
	CreateSetCalls []string `json:"create_set_calls,omitempty"`
	CreateNewCalls []string `json:"create_new_calls,omitempty"`
	CreateAdopts   bool     `json:"create_adopts,omitempty"`

	// CreateRefusesExisting is the first error message in the file that
	// says an object is already there ("Unable to create RouterNat,
	// existing object already found: %+v"): a create sent as a PATCH of
	// a parent that still refuses a name the parent already holds.
	CreateRefusesExisting string `json:"create_refuses_existing,omitempty"`
}

// ImportNameSet is one importer's `d.Set(Attr, Template)` after
// ParseImportId over Regexes.
type ImportNameSet struct {
	Attr     string   `json:"attr"`
	Template string   `json:"template"`
	Regexes  []string `json:"regexes"`
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
