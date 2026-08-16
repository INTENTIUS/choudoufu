// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the evidence source for [identity.TypeIdentity.RecordBacked],
// the one field in the identity table that no AWS source can speak to.
//
// Every other row in that table is derived from AWS evidence - the
// CloudFormation registry's primaryIdentifier, the provider's import grammar,
// the provider's identity schema. The record-backed rows are not AWS
// resources at all: they are the effects set issue #73 keeps in the record
// store (null_resource, terraform_data, the random_* and time_* families).
// They were therefore carried as ten hand-written table entries with a ruling
// apiece in annotations.json, and every one of those rulings named the same
// exit: "Retire by deriving the RecordBacked rows ... inside -emit instead of
// carrying them as unreproduced table rows." This file is that derivation's
// evidence half.
//
// The evidence is the providers' own GetProviderSchema response, which every
// one of them serves offline from the plugin cache. Two facts come out of it,
// per attribute of every block of every managed resource type: whether the
// provider marks it Sensitive, and whether it marks it Deprecated. That is
// enough to settle the whole set, because "can a persisted record hold this
// type's whole value" reduces to "does the type handle secret material" once
// the provider is one whose resources exist only inside the record (see
// [logicalProviderSource.StoreOnly]).
//
// -logical-schemas is the acquisition mode and needs a working init binary;
// -emit reads the artifact it writes and needs nothing but the checkout, the
// same split tools/survey-gen keeps between -all and -render.

// logicalSchemasJSONRel is the artifact this mode writes and -emit reads.
const logicalSchemasJSONRel = "live/logical-schemas.json"

// logicalProviderSource is one provider whose entire managed-resource set
// this generator measures.
//
// The set is small and closed on purpose: these are the providers whose
// resource types internal/live/lint's logicalFamilyPrefixes already names as
// "provider-local type prefixes whose resources exist only inside the state
// file", plus OpenTofu's own built-in provider, which serves terraform_data
// and is compiled into this binary rather than downloaded. Naming a provider
// is naming an input - the same thing tools/survey-gen does with
// hashicorp/aws - not naming a resource type: nothing downstream of this list
// mentions a type, and a new release of any of these providers is picked up
// by re-running the mode.
type logicalProviderSource struct {
	// Type is the provider's local name, and the type prefix its resources
	// carry (random_, tls_, time_, null_, local_). Empty for the built-in
	// provider, whose one resource type shares no prefix with anything.
	Type string

	// Source is the registry source address, and Version the exact release.
	// Both empty for the built-in provider.
	Source  string
	Version string

	// Builtin reads the schema from OpenTofu's own compiled-in provider
	// (internal/builtin/providers/tf) rather than launching a plugin. It is
	// the only way terraform_data's schema can be read at all: no registry
	// publishes it.
	Builtin bool

	// StoreOnly records whether this provider's resources exist only inside
	// OpenTofu's own record of them - which is what makes a persisted
	// micro-state record able to stand in for the state file entry, and
	// therefore what makes a non-secret type of this provider RecordBacked.
	//
	// It is a per-provider fact, stated once here with its evidence, because
	// nothing in a GetProviderSchema response distinguishes the two cases:
	// measured against all five providers, not one of the twenty-one types
	// serves a resource identity schema, so the "does this have an address in
	// some system" signal that would settle it mechanically is absent from
	// every one of them.
	StoreOnly bool

	// StoreOnlyEvidence is why, for either value of StoreOnly.
	StoreOnlyEvidence string
}

// logicalProviderSources is this generator's input: the five providers
// internal/live/lint's logicalFamilyPrefixes names, plus the built-in.
//
// hashicorp/local is the one member with StoreOnly false, and it is measured
// anyway rather than dropped, so the artifact is the complete picture of the
// families lint classifies rather than only the part that produces rows.
var logicalProviderSources = []logicalProviderSource{
	{
		Type: "random", Source: "hashicorp/random", Version: "3.9.0",
		StoreOnly: true,
		StoreOnlyEvidence: "every hashicorp/random resource generates its value at create time and " +
			"keeps it nowhere but OpenTofu's own record of the resource; there is no remote object " +
			"to read back, which is why the provider's own docs describe each type as generating a " +
			"value \"intended to be used as a unique identifier for other resources\" rather than " +
			"as managing one",
	},
	{
		Type: "tls", Source: "hashicorp/tls", Version: "4.3.0",
		StoreOnly: true,
		StoreOnlyEvidence: "hashicorp/tls computes keys and certificates locally and stores them in " +
			"the record; nothing is registered with any authority, so the record is the only place " +
			"the material exists. (Every one of its types is secret-bearing and so derives no row " +
			"here regardless; it is measured because lint classifies the family.)",
	},
	{
		Type: "time", Source: "hashicorp/time", Version: "0.14.1",
		StoreOnly: true,
		StoreOnlyEvidence: "each hashicorp/time resource's own description says it \"keeps a ... UTC " +
			"timestamp stored in the Terraform state\": the stored timestamp is the whole resource",
	},
	{
		Type: "null", Source: "hashicorp/null", Version: "3.3.1",
		StoreOnly: true,
		StoreOnlyEvidence: "null_resource's own description is \"implements the standard resource " +
			"lifecycle but takes no further action\" - there is nothing outside the record by " +
			"construction",
	},
	{
		Type: "local", Source: "hashicorp/local", Version: "2.9.0",
		StoreOnly: false,
		StoreOnlyEvidence: "hashicorp/local's resources write a file to the local filesystem and read " +
			"it back on refresh. Measured against 2.9.0: delete the file and the provider drops the " +
			"resource so the next plan proposes a create, and id is the SHA1 of the content, so " +
			"changing the file changes the resource. The file outlives any record and two instances " +
			"with distinct records still collide on one filename, so the identity is the filename " +
			"rather than the record - which is exactly what a RecordBacked row would claim it is " +
			"not. internal/live/lint's TestLocalFileKeepsItsCountIndexCheck pins the consequence " +
			"from the other side.",
	},
	{
		Type: "", Builtin: true,
		StoreOnly: true,
		StoreOnlyEvidence: "terraform_data is OpenTofu's own built-in replacement for null_resource; " +
			"its output is its input echoed back after apply, with no provider and no remote object " +
			"anywhere in the lifecycle",
	},
}

// logicalAttr is one attribute of one resource type, with the two flags the
// provider ships for it in its own binary.
type logicalAttr struct {
	// Name is the attribute's path within the resource's schema, with
	// nested attribute and nested block names joined by dots.
	Name string `json:"name"`

	// DeprecationMessage is the provider's own wording, kept because the
	// derivation below turns on it: a deprecated sensitive attribute is one
	// the provider tells you not to use, and classifying a type by it would
	// classify it by an attribute whose whole purpose is to no longer be
	// that type's.
	DeprecationMessage string `json:"deprecation_message,omitempty"`
}

// logicalTypeSchema is one managed resource type's measurement.
type logicalTypeSchema struct {
	Type string `json:"type"`

	// Description is the provider's own prose for the type, shipped in its
	// binary. It is the documentation evidence for a family this repo's
	// offline doc cache (tools/importdocs-gen, AWS-scoped) cannot reach.
	Description string `json:"description,omitempty"`

	// Sensitive is every attribute the provider marks Sensitive, sorted.
	Sensitive []logicalAttr `json:"sensitive,omitempty"`

	// Deprecated is every attribute the provider marks Deprecated, sorted -
	// the complete list, not only the ones that are also sensitive, so the
	// artifact is a measurement rather than a selection.
	Deprecated []logicalAttr `json:"deprecated,omitempty"`
}

// logicalProviderSchemas is one provider's whole measurement.
type logicalProviderSchemas struct {
	Source            string              `json:"source"`
	Version           string              `json:"version"`
	StoreOnly         bool                `json:"store_only"`
	StoreOnlyEvidence string              `json:"store_only_evidence"`
	Types             []logicalTypeSchema `json:"types"`
}

// logicalSchemas is live/logical-schemas.json.
type logicalSchemas struct {
	Providers []logicalProviderSchemas `json:"providers"`
}

// builtinProviderSource is the source address recorded for the compiled-in
// provider, matching the address OpenTofu itself resolves terraform_data
// under.
const builtinProviderSource = "terraform.io/builtin/terraform"

// runLogicalSchemas is -logical-schemas' entry point: read every provider in
// [logicalProviderSources] and write [logicalSchemasJSONRel].
func runLogicalSchemas(initBin string, out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	art, err := acquireLogicalSchemas(context.Background(), initBin, errOut)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, logicalSchemasJSONRel), data, 0o644); err != nil { //nolint:gosec // a fixed path inside the checkout
		return fmt.Errorf("writing %s: %w", logicalSchemasJSONRel, err)
	}
	fmt.Fprintf(out, "wrote %s\n", logicalSchemasJSONRel)

	backed := recordBackedTypes(art)
	types := 0
	for _, p := range art.Providers {
		types += len(p.Types)
	}
	fmt.Fprintf(errOut, "row-gen -logical-schemas: %d provider(s), %d resource type(s), %d derive RecordBacked\n",
		len(art.Providers), types, len(backed))
	return nil
}

// acquireLogicalSchemas reads every source's schemas and reduces each to the
// two flag sets the derivation needs.
func acquireLogicalSchemas(ctx context.Context, initBin string, log *os.File) (logicalSchemas, error) {
	var art logicalSchemas
	for _, src := range logicalProviderSources {
		resp, err := readOneProviderSchema(ctx, initBin, src, log)
		if err != nil {
			return logicalSchemas{}, err
		}
		entry := logicalProviderSchemas{
			Source:            src.Source,
			Version:           src.Version,
			StoreOnly:         src.StoreOnly,
			StoreOnlyEvidence: src.StoreOnlyEvidence,
		}
		if src.Builtin {
			entry.Source = builtinProviderSource
			entry.Version = choudoufuVersionPlaceholder
		}
		names := make([]string, 0, len(resp.ResourceTypes))
		for name := range resp.ResourceTypes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			entry.Types = append(entry.Types, reduceResourceSchema(name, resp.ResourceTypes[name]))
		}
		art.Providers = append(art.Providers, entry)
	}
	return art, nil
}

// choudoufuVersionPlaceholder is the version recorded for the built-in
// provider: it ships with this binary, so there is no release to pin.
const choudoufuVersionPlaceholder = "built-in"

// readOneProviderSchema returns one source's GetProviderSchema response,
// either from the compiled-in provider or by launching the plugin.
func readOneProviderSchema(ctx context.Context, initBin string, src logicalProviderSource, log *os.File) (providers.GetProviderSchemaResponse, error) {
	if src.Builtin {
		return tf.NewProvider().GetProviderSchema(ctx), nil
	}
	dir, err := os.MkdirTemp("", "row-gen-logical-"+src.Type)
	if err != nil {
		return providers.GetProviderSchemaResponse{}, err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temp dir this function owns
	return pluginschema.Acquire(ctx, pluginschema.Request{
		InitBin:  initBin,
		WorkDir:  dir,
		Source:   src.Source,
		Version:  src.Version,
		Provider: addrs.NewDefaultProvider(src.Type),
		Log:      log,
	})
}

// reduceResourceSchema walks every attribute of every block of one resource
// type - nested attribute types and nested blocks included - and records the
// Sensitive and Deprecated ones.
func reduceResourceSchema(name string, schema providers.Schema) logicalTypeSchema {
	out := logicalTypeSchema{Type: name}
	if schema.Block == nil {
		return out
	}
	out.Description = schema.Block.Description
	walkBlockAttrs(schema.Block, "", func(path string, a *configschema.Attribute) {
		if a.Sensitive {
			out.Sensitive = append(out.Sensitive, logicalAttr{Name: path})
		}
		if a.Deprecated {
			out.Deprecated = append(out.Deprecated, logicalAttr{Name: path, DeprecationMessage: a.DeprecationMessage})
		}
	})
	sort.Slice(out.Sensitive, func(i, j int) bool { return out.Sensitive[i].Name < out.Sensitive[j].Name })
	sort.Slice(out.Deprecated, func(i, j int) bool { return out.Deprecated[i].Name < out.Deprecated[j].Name })
	return out
}

// walkBlockAttrs visits every attribute reachable from b, including those
// inside nested attribute object types and nested blocks, with a dotted path.
func walkBlockAttrs(b *configschema.Block, prefix string, visit func(string, *configschema.Attribute)) {
	for name, a := range b.Attributes {
		visit(prefix+name, a)
		if a.NestedType != nil {
			walkObjectAttrs(a.NestedType, prefix+name+".", visit)
		}
	}
	for name, nested := range b.BlockTypes {
		walkBlockAttrs(&nested.Block, prefix+name+".", visit)
	}
}

// walkObjectAttrs is walkBlockAttrs for a nested attribute's object type.
func walkObjectAttrs(o *configschema.Object, prefix string, visit func(string, *configschema.Attribute)) {
	for name, a := range o.Attributes {
		visit(prefix+name, a)
		if a.NestedType != nil {
			walkObjectAttrs(a.NestedType, prefix+name+".", visit)
		}
	}
}

// loadLogicalSchemas reads live/logical-schemas.json.
func loadLogicalSchemas(path string) (logicalSchemas, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return logicalSchemas{}, err
	}
	var art logicalSchemas
	if err := json.Unmarshal(data, &art); err != nil {
		return logicalSchemas{}, fmt.Errorf("decoding %s: %w", path, err)
	}
	if len(art.Providers) == 0 {
		return logicalSchemas{}, fmt.Errorf("%s carries no providers; the artifact shape has changed or the file is a stub", path)
	}
	return art, nil
}

// liveSensitiveAttrs is one type's sensitive attributes with its deprecated
// ones removed.
//
// The subtraction is part of the rule rather than an exception to it. A
// deprecated attribute is one the provider tells you not to use, and it
// cannot support the reading "this type handles secret material":
// hashicorp/local deprecated local_file.sensitive_content precisely in order
// to move the sensitive case out into local_sensitive_file, so counting it
// would classify local_file by an attribute whose whole purpose is to no
// longer be local_file's. Measured across all twenty-one types, exactly one
// attribute is both sensitive and deprecated, so the clause moves one type
// and no already-derived row.
func liveSensitiveAttrs(t logicalTypeSchema) []string {
	deprecated := make(map[string]bool, len(t.Deprecated))
	for _, a := range t.Deprecated {
		deprecated[a.Name] = true
	}
	var live []string
	for _, a := range t.Sensitive {
		if !deprecated[a.Name] {
			live = append(live, a.Name)
		}
	}
	return live
}

// recordBackedTypes is the derivation: every resource type of a store-only
// provider with no live sensitive attribute anywhere in its schema, sorted.
//
// The rule is the one internal/live/lint's own classification already argues
// from, in its rows' Evidence strings, spelled mechanically: a live sensitive
// attribute anywhere in the schema means the type handles material a record
// may not carry (live/RECEIPTS.md's no-secrets rule), and none means the
// record can hold the type's whole value. lint's tls_self_signed_cert row
// makes the "anywhere" reading explicit by refusing a type whose own cert_pem
// output is not secret, on the strength of a sensitive private_key_pem
// *argument*.
//
// It names no resource type. A new release of any input provider is picked up
// by re-running -logical-schemas: random_uuid4 and random_uuid7 shipped in
// hashicorp/random 3.9.0 after the hand table was written, and are the first
// two types this rule admitted that no human had written a row for.
func recordBackedTypes(art logicalSchemas) []string {
	var out []string
	for _, p := range art.Providers {
		if !p.StoreOnly {
			continue
		}
		for _, t := range p.Types {
			if len(liveSensitiveAttrs(t)) > 0 {
				continue
			}
			out = append(out, t.Type)
		}
	}
	sort.Strings(out)
	return out
}
