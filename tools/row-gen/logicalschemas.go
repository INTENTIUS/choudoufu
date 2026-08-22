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
	"strings"

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
// provider marks it Sensitive, and whether it marks it Deprecated. Together
// they settle whether the record holding a type's whole value would hold
// SECRET material, which is [identity.TypeIdentity.SecretMaterial]; whether
// the record can hold that value at all is [recordBackedTypes], and the
// answer there is yes for every measured type. What a run then does about
// the first flag is the strict block's secrets setting - GitHub issue #365
// slice 3, whose default keeps the value the way a stock OpenTofu state file
// keeps it.
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
	// micro-state record able to stand in for the state file entry outright,
	// and therefore what puts a non-secret type of this provider in lint's
	// ClassRecordAdmitted rather than its ClassExternalAdmitted.
	//
	// It does NOT decide RecordBacked. A non-store-only provider's type is
	// record-backed too, because there is no other way to bring its prior
	// state back: hashicorp/local 2.9.0 implements no ImportState at all.
	// See [recordBackedTypes].
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
// hashicorp/local is the one member with StoreOnly false. It was measured
// from the start so the artifact would be the complete picture of the families
// lint classifies rather than only the part that produced rows; since #314 it
// produces rows too, and StoreOnly picks the class instead of gating the row.
//
// One stale parenthetical is left standing in hashicorp/tls's evidence below,
// deliberately: "every one of its types is secret-bearing and so derives no
// row here regardless" was true until GitHub issue #365 slice 3, which made a
// secret-bearing type derive a row carrying
// [identity.TypeIdentity.SecretMaterial] rather than derive none. Correcting
// it means re-running -logical-schemas, which needs provider binaries, and
// the string is committed verbatim into live/logical-schemas.json - an
// artifact this repository regenerates rather than hand-edits. It is inert:
// StoreOnlyEvidence is rendered onto a row only for a provider whose
// StoreOnly is false, which hashicorp/tls's is not, so nothing downstream
// reads it. TestLogicalSchemasArtifactCoversEverySource is what holds the two
// copies together.
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
			"changing the file changes the resource. The file outlives any record, and two instances " +
			"at distinct addresses hold distinct records and still collide on one filename, so what " +
			"the resource affects is named by its filename argument rather than bounded by the " +
			"record",
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

// logicalClassRow is one internal/live/lint logicalTypes row, derived whole
// from this artifact: the same two facts that settle RecordBacked, rendered
// as the classification lint states to an operator.
//
// The two derivations are one rule read at two layers, which is the point.
// identity's RecordBacked answers "can resolution hold this type's whole
// value in a record"; lint's RECORD_ADMITTED answers "may a configuration
// naming this type get past lint under a record_store". They were separately
// maintained - a hand table in lint, a derivation here - and drifted the
// moment this derivation admitted four types the hand table predated:
// random_string, random_uuid, random_uuid4 and random_uuid7 resolved fine and
// lint refused them outright, so the record_store branch never ran. Deriving
// both from this one artifact is what makes that drift unrepresentable.
type logicalClassRow struct {
	// Type is the resource type name.
	Type string

	// Class is "RECORD_ADMITTED", "EXTERNAL_ADMITTED" or "SECRET_REFUSED",
	// spelled as lint's LogicalClass constants are. Every measured type is
	// one of the three and never lint's fourth: OTHER_REFUSED is the
	// no-row-found default, which is now reached only by a member of a
	// surveyed family released since the last -logical-schemas run.
	//
	// Two facts choose between them, and they are independent. A live
	// sensitive attribute settles SECRET_REFUSED whatever the provider is.
	// Failing that, the provider's own StoreOnly settles which admitted
	// class: true means the record is the whole of the resource
	// (RECORD_ADMITTED), false means the record holds the resource's prior
	// state while an argument of its own names something outside it
	// (EXTERNAL_ADMITTED).
	Class string

	// StoredClass is the class this type carries under GitHub issue #365's
	// `strict { secrets = "store" }`, which is the default: the answer
	// StoreOnly gives on its own, before sensitivity is consulted.
	//
	// Set only for a SECRET_REFUSED row, and empty for every other, where
	// [Class] already is that answer. A row carrying both is one rule read
	// under two settings - the same shape the [Class] field's own doc
	// comment describes for the two facts that choose it, one setting
	// further out.
	StoredClass string

	// Prefix is the family prefix, empty for the built-in provider.
	Prefix string

	// Evidence is the measured, operator-facing reason, built by
	// [logicalEvidence] from this artifact rather than written by hand.
	Evidence string

	// External is the provider's StoreOnlyEvidence, carried onto the row for
	// EXTERNAL_ADMITTED only - the measured reason the record does not bound
	// what this type affects, which is the half of that class's operator
	// message [logicalEvidence] cannot supply because it is a per-provider
	// fact rather than a per-type one. Empty for every other class.
	External string
}

// logicalClassRecordAdmitted and logicalClassSecretRefused are the string
// values of internal/live/lint's LogicalClass constants. They are spelled
// here rather than imported because the generator renders source text; the
// rendered file's own compilation against the real constants is what checks
// them, and TestGeneratedLogicalClassNamesMatchLint pins the pair directly.
const (
	logicalClassRecordAdmitted   = "ClassRecordAdmitted"
	logicalClassExternalAdmitted = "ClassExternalAdmitted"
	logicalClassSecretRefused    = "ClassSecretRefused"
)

// logicalFamilyPrefix is the type prefix a provider's resources carry, taken
// from the last segment of its source address - the same string
// [logicalProviderSources] declares as Type and passes to
// addrs.NewDefaultProvider, so the artifact needs no separate field for it.
//
// The built-in provider has none, and that is not a missing value: giving
// terraform_data a "terraform_" prefix would claim a whole family this fork
// has never surveyed, on the strength of one built-in type. The second
// return is false for it, and it contributes no family prefix to lint.
func logicalFamilyPrefix(p logicalProviderSchemas) (string, bool) {
	if p.Source == builtinProviderSource {
		return "", false
	}
	name := p.Source
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name + "_", true
}

// logicalProviderLabel names a provider in operator-facing evidence.
func logicalProviderLabel(p logicalProviderSchemas) string {
	if p.Source == builtinProviderSource {
		return "OpenTofu's built-in terraform provider"
	}
	return p.Source + " " + p.Version
}

// joinAnd renders a list as English prose: "a", "a and b", "a, b and c".
func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// logicalEvidence builds one row's Evidence from the measurement, in the
// three shapes the data can take.
//
// The third shape - a type whose only sensitive attributes are deprecated -
// produces no row today, because the one attribute in the whole measurement
// that is both is local_file.sensitive_content and hashicorp/local is not
// store-only. It is built anyway, and tested, because the alternative is an
// evidence string that says "marks no attribute sensitive" about a type whose
// schema plainly marks one - a false claim in an operator-facing message,
// waiting on a provider release to surface it.
func logicalEvidence(p logicalProviderSchemas, t logicalTypeSchema) string {
	label := logicalProviderLabel(p)
	live := liveSensitiveAttrs(t)
	if len(live) > 0 {
		return fmt.Sprintf("%s marks %s sensitive", label, joinAnd(live))
	}
	if len(t.Sensitive) > 0 {
		var names, messages []string
		for _, a := range t.Sensitive {
			names = append(names, a.Name)
		}
		for _, a := range t.Deprecated {
			if a.DeprecationMessage != "" {
				messages = append(messages, fmt.Sprintf("%s: %q", a.Name, a.DeprecationMessage))
			}
		}
		return fmt.Sprintf(
			"%s marks %s sensitive, but deprecates every one of them (%s), so no attribute the provider "+
				"still tells you to use carries secret material",
			label, joinAnd(names), joinAnd(messages))
	}
	return fmt.Sprintf(
		"%s marks no attribute of %s sensitive, measured over every attribute of every block of its "+
			"schema, nested attribute types and nested blocks included",
		label, t.Type)
}

// logicalClassRows derives lint's whole logicalTypes table: one row per
// measured resource type, classified by the same live-sensitive-attribute
// rule [recordBackedTypes] applies, sorted by type.
//
// StoreOnly used to gate whether a row was derived AT ALL, which left both of
// hashicorp/local's types on lint's OTHER_REFUSED default and cost this
// repository three issues (#237, #238, #314) arguing about whether that was a
// verdict or a gap. It was neither: it was one measurement doing two jobs.
// StoreOnly answers "is the record the whole of the resource", and the answer
// "no" is a reason to pick a different admitted class, not a reason to say
// nothing. So it now selects between RECORD_ADMITTED and EXTERNAL_ADMITTED,
// and the sensitivity rule still decides admission itself - which is what
// keeps local_sensitive_file refused on its own measured evidence rather than
// on the hand-written exception internal/live/lint used to carry for it.
//
// The distinction StoreOnly=false actually buys downstream is
// countIndexScopeForType's skip: an EXTERNAL_ADMITTED type does not get it,
// because an argument of its own names the object it writes, and
// internal/live/lint's TestLocalFileKeepsItsCountIndexCheck pins that from
// the other side.
func logicalClassRows(art logicalSchemas) ([]logicalClassRow, error) {
	var out []logicalClassRow
	for _, p := range art.Providers {
		prefix, hasPrefix := logicalFamilyPrefix(p)
		for _, t := range p.Types {
			if hasPrefix && !strings.HasPrefix(t.Type, prefix) {
				return nil, fmt.Errorf(
					"row-gen -emit: %s serves resource type %s, which does not start with the %q prefix its "+
						"source address derives - lint classifies unlisted types by prefix, so a provider whose "+
						"types do not share one cannot be reduced to a family",
					p.Source, t.Type, prefix)
			}
			// The class the provider's own StoreOnly settles, before
			// sensitivity is consulted at all. It is what the type IS: the
			// record is the whole of the resource, or the record holds its
			// prior state while an argument names something outside it.
			stored := logicalClassRecordAdmitted
			external := ""
			if !p.StoreOnly {
				stored = logicalClassExternalAdmitted
				external = p.StoreOnlyEvidence
			}
			// Sensitivity then decides which of the two columns that answer
			// goes in. A live sensitive attribute means the record would
			// hold secret material, which is refused unless the operator
			// asked for it - GitHub issue #365's secrets setting - so the
			// class becomes SECRET_REFUSED and the answer above is carried
			// alongside as the class that applies under `secrets = "store"`.
			//
			// Keeping BOTH is what lets one rule serve two settings without
			// being written down twice. StoredClass is empty for a type with
			// no live sensitive attribute, where Class already is the
			// answer.
			class, storedClass := stored, ""
			if len(liveSensitiveAttrs(t)) > 0 {
				class, storedClass = logicalClassSecretRefused, stored
			}
			out = append(out, logicalClassRow{
				Type:        t.Type,
				Class:       class,
				StoredClass: storedClass,
				Prefix:      prefix,
				Evidence:    logicalEvidence(p, t),
				External:    external,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out, nil
}

// logicalFamilyPrefixesOf is lint's logicalFamilyPrefixes: every input
// provider's family prefix, store-only or not, sorted.
//
// Membership here is what makes an unlisted family member classify with the
// logical-resource explanation instead of the generic unadmitted-type
// refusal, so hashicorp/local belongs in it even though it derives no row -
// a local_file gets told its whole family is out, which is the answer, rather
// than being told it is missing from a table it was never a candidate for.
func logicalFamilyPrefixesOf(art logicalSchemas) []string {
	var out []string
	for _, p := range art.Providers {
		if prefix, ok := logicalFamilyPrefix(p); ok {
			out = append(out, prefix)
		}
	}
	sort.Strings(out)
	return out
}

// recordBackedTypes is the derivation: every measured resource type, sorted.
//
// It used to subtract the types carrying a live sensitive attribute, and
// GitHub issue #365 slice 3 is why it no longer does. The subtraction
// answered the wrong question. [TypeIdentity.RecordBacked] asks whether a
// type's prior state comes out of the record store rather than out of a
// cloud read, and for a secret-generating logical type the answer was always
// yes - the record is the only place it could come from, exactly as for its
// non-secret siblings. What the sensitive attribute settles is whether that
// record may be WRITTEN, which is an operator's choice with a default rather
// than a fact about the mechanism, and it is now carried on the same row by
// [secretMaterialTypes] as [TypeIdentity.SecretMaterial].
//
// The visible consequence is that this function's output grew by the seven
// types the subtraction removed. None of them resolves any differently under
// `strict { secrets = "refuse" }` than it did before that setting existed.
//
// StoreOnly is deliberately not consulted here, and that is the one thing
// worth reading twice. [TypeIdentity.RecordBacked] answers "does this type's
// prior state come out of the record store rather than out of a cloud read",
// and for hashicorp/local the answer is yes for a reason StoreOnly does not
// touch: hashicorp/local 2.9.0 implements no ImportState for local_file at
// all (`tofu import local_file.f <path>` answers "Resource Import Not
// Implemented"), so there is no other way to bring its prior state back. What
// StoreOnly settles is the lint class - see [logicalClassRows] - and that
// governs the count.index walk, not the record.
//
// The rule is the one internal/live/lint's own classification already argues
// from, in its rows' Evidence strings, spelled mechanically: a live sensitive
// attribute anywhere in the schema means the record holding this type's whole
// value would hold secret material, and none means it would not. Which of the
// two flags a row carries is what that settles; whether the record is written
// is the operator's secrets setting. lint's tls_self_signed_cert row
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
		for _, t := range p.Types {
			out = append(out, t.Type)
		}
	}
	sort.Strings(out)
	return out
}

// secretMaterialTypes is the other half of the same measurement: every
// measured resource type that DOES carry a live sensitive attribute, sorted.
//
// It is a subset of [recordBackedTypes] by construction, which is the whole
// point of splitting the rule this way. Until GitHub issue #365 slice 3
// these types were simply absent from the record-backed set, which said the
// record COULD NOT hold their prior state - and that was never the finding.
// The finding was that the record would hold secret material, which is a
// question about what the operator asked for and not about what the
// mechanism can do: a stock state file holds random_password.result in
// clear, so refusing it made a configuration stock runs unrunnable here.
//
// So the derivation now emits both flags off one predicate, and
// [identity.TypeIdentity.SecretMaterial] is what internal/live/lint and
// internal/live/identity gate on. Nothing here decides the gate; this
// function only says which rows carry the fact.
//
// It names no resource type, for [recordBackedTypes]' reason.
func secretMaterialTypes(art logicalSchemas) []string {
	var out []string
	for _, p := range art.Providers {
		for _, t := range p.Types {
			if len(liveSensitiveAttrs(t)) == 0 {
				continue
			}
			out = append(out, t.Type)
		}
	}
	sort.Strings(out)
	return out
}
