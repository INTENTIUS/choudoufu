// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"

	"github.com/opentofu/opentofu/internal/providers"
)

// Admission says what admits a type to the identity table with no
// hand-written row: the provider's schemas on their own, or the schemas
// plus a configuration that answers the one question they leave open.
type Admission string

const (
	// AdmitSchema: the strict rule settles it. Every attribute the provider
	// requires for import is a *required* argument of the type, so no
	// configuration can decline to name the object. See [Derivable].
	AdmitSchema Admission = "schema"

	// AdmitConfigSignal: the schemas leave it open and a configuration
	// closes it. Every attribute required for import is a settable argument
	// of the type, and every instance in the configuration sets every one
	// of them. See [ConfigSignal].
	AdmitConfigSignal Admission = "config-signal"

	// AdmitNeedsConfigSignal: the schemas leave it open and no
	// configuration here answers. The type is a candidate, and the
	// candidate's terms are known: a configuration that sets these
	// arguments admits it, one that does not leaves it to discovery.
	AdmitNeedsConfigSignal Admission = "needs-config-signal"

	// AdmitConfigDeclined: the schemas leave it open and the configuration
	// says no. Some instance sets none of the identity arguments, or only
	// some of them, so the cloud names these objects and marker discovery
	// is what finds them.
	AdmitConfigDeclined Admission = "config-declined"
)

// DerivableWith is [Derivable] with the config-side naming signal folded
// in: the types the schemas admit on their own, plus the types the schemas
// leave ambiguous and this configuration resolves.
//
// The ambiguity it closes is the one the package documentation calls the
// Optional+Computed cohort, and it is where the interesting types live. A
// legacy-SDK schema marks aws_s3_bucket's bucket argument Optional+Computed
// because bucket_prefix is the alternative, and marks aws_vpc's id
// attribute Optional+Computed because that is what the SDK does with id.
// Read from the schemas the two are one shape; [Derivable] therefore
// refuses both, which is right about aws_vpc and needlessly wrong about
// aws_s3_bucket.
//
// A configuration separates them without a shred of ambiguity. A block that
// writes bucket = "…" has named the object it owns. A block that writes no
// id = "…" - which is every aws_vpc block ever written - has not. The
// signal is per instance because the question is per instance: the same
// type can be client-named in one block and prefix-generated in the next,
// and a verdict that flattened them would import the wrong object for one
// of the two. A type is admitted here only when every instance of it in the
// configuration sets every attribute the provider requires for import.
//
// A nil signal makes this exactly [Derivable]: no configuration, nothing to
// say about the cohort.
func DerivableWith(resourceTypes map[string]providers.Schema, signal *ConfigSignal) []DerivableType {
	out := Derivable(resourceTypes)
	for typeName, schema := range resourceTypes {
		d, ok := derivableFromConfig(typeName, schema, signal)
		if !ok {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// DerivableNewWith is [DerivableWith] restricted to the types
// [DefaultTable] does not already cover: the set a wiring batch would grow
// the table with, now including the ones only a configuration could settle.
func DerivableNewWith(resourceTypes map[string]providers.Schema, signal *ConfigSignal) []DerivableType {
	all := DerivableWith(resourceTypes, signal)
	out := make([]DerivableType, 0, len(all))
	for _, d := range all {
		if !d.InTable {
			out = append(out, d)
		}
	}
	return out
}

func derivableFromConfig(typeName string, schema providers.Schema, signal *ConfigSignal) (DerivableType, bool) {
	required, ok := cohortAttrs(schema)
	if !ok {
		return DerivableType{}, false
	}
	verdict, insts := signal.NamingOfType(typeName, required)
	if verdict != NamingClientNamed {
		return DerivableType{}, false
	}

	_, context := identityAttrs(schema.IdentitySchema)
	_, inTable := DefaultTable[typeName]
	return DerivableType{
		Type:          typeName,
		IdentityAttrs: required,
		Context:       context,
		InTable:       inTable,
		Admits:        AdmitConfigSignal,
		Naming:        insts,
	}, true
}

// cohortAttrs reports the attributes that leave a type undecided by the
// schemas alone, and whether the type is in that cohort at all.
//
// The cohort is narrow on purpose. Every attribute the provider requires
// for import has to be an argument of the type that a configuration is
// allowed to set, because the whole rule is "did the configuration set it",
// and an attribute configuration cannot set can never be answered yes. At
// least one of them has to be optional, because a type all of whose
// identity attributes are required arguments is already settled - by
// [Derivable], with no configuration needed - and reporting it twice would
// let a weaker piece of evidence stand in for a stronger one.
func cohortAttrs(schema providers.Schema) ([]string, bool) {
	if schema.IdentitySchema == nil || schema.Block == nil {
		return nil, false
	}
	required, _ := identityAttrs(schema.IdentitySchema)
	if len(required) == 0 {
		return nil, false
	}

	optional := false
	for _, name := range required {
		arg, ok := schema.Block.Attributes[name]
		switch {
		case !ok || arg == nil:
			// The provider computes it; no argument by that name exists.
			return nil, false
		case arg.Required:
			// Settled either way; it is the other attributes that decide.
		case arg.Optional:
			optional = true
		default:
			// Computed-only: configuration cannot name it, whatever it says.
			return nil, false
		}
	}
	if !optional {
		return nil, false
	}
	return required, true
}
