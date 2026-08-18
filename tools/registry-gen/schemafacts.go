// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// This file is issue #155. The CloudFormation schemas carry considerably
// more than live/registry.json's six fields, and three of the discarded ones
// are load-bearing for work already filed:
//
//   - handlers[].permissions, on 1,522 of 1,683 types, is the IAM actions
//     each CRUDL operation needs. #143 asks what actions a run calls, and
//     the first plan for it was to read the code and write a table by hand.
//   - root required, on 1,434 types, is a second and independent source for
//     the requiredness the AWS provider under-declares in its own wire
//     schema, which is why tools/estate-gen carries hand overrides (#136).
//   - property enum is the declared legal members of an enum-valued
//     property. estate-gen hardcodes "Enabled" and "AES256" today with a
//     Reasons string saying the value cannot be inferred; the schema
//     declares both.
//
// Extraction only, as the issue asks. Nothing consumes this yet, and each
// consumer can then say which field it reads.
//
// # Why a sibling artifact rather than more columns
//
// live/registry.json is 1.1MB and has an embedded copy compiled into the
// binary (internal/live/registry/registry.json, held in step by
// TestEmbeddedArtifactsMatchLive). These three fields are 28,356 action
// strings and 13,170 enum members, which roughly doubles it - and the
// runtime reads none of them. They are generator input, not resolver input.
//
// So the split is by consumer rather than by subject: what the binary needs
// stays in the embedded artifact, what generators need lives here and is not
// embedded. Measured before choosing, because the issue's own instruction
// was to size it deliberately and to split rather than sample: a partial
// extraction is the failure mode this repository's registry scanners exist
// to prevent.

// schemaFactsRel is where the sibling artifact is committed.
const schemaFactsRel = "live/registry-schema-facts.json"

// SchemaFacts is the artifact.
type SchemaFacts struct {
	GeneratedBy string           `json:"generated_by"`
	Pin         SpecPin          `json:"pin"`
	Counts      SchemaFactCounts `json:"counts"`
	Types       []SchemaFact     `json:"types"`
}

// SchemaFactCounts is the coverage claim. The numbers in issue #155 are one
// zip's snapshot; these are the artifact's own.
type SchemaFactCounts struct {
	Types int `json:"types"`

	// WithHandlerPermissions is how many types declare permissions on at
	// least one handler, and HandlerPermissionActions the total number of
	// action strings across all of them.
	WithHandlerPermissions int `json:"with_handler_permissions"`
	HandlerPermissionActs  int `json:"handler_permission_actions"`

	// WithRequired is how many types declare a root required list.
	WithRequired int `json:"with_required"`

	// WithEnums is how many types declare at least one enum anywhere the
	// walk reaches, and EnumMembers the total member count.
	WithEnums   int `json:"with_enums"`
	EnumMembers int `json:"enum_members"`

	// EnumsUnattributed is how many enums the walk found but could not
	// attribute to a named property or definition. A counted gap, never a
	// silent omission - the same rule relationships.go holds itself to.
	EnumsUnattributed int `json:"enums_unattributed"`

	// WithUniqueNameProperty is how many types carry a resource-owned
	// "Name" property with a non-empty description (see
	// [UniqueNameProperty] and [findUniqueNameProperty]), and
	// UniqueNamePropertyDeclaredUnique the subset whose description states
	// the value must be unique - issue #272's CFN-registry evidence
	// source. Measured at v6.59.0: 374 and 40 respectively.
	WithUniqueNameProperty           int `json:"with_unique_name_property"`
	UniqueNamePropertyDeclaredUnique int `json:"unique_name_property_declared_unique"`
}

// SchemaFact is one type's extracted facts. Every field is omitempty: a type
// carrying none of them is still listed, so the roster is the full 1,683 and
// absence is visible rather than inferred from a missing row.
type SchemaFact struct {
	TypeName string `json:"type_name"`

	// HandlerPermissions is the IAM actions each handler declares, keyed by
	// operation ("create", "read", "update", "delete", "list").
	HandlerPermissions map[string][]string `json:"handler_permissions,omitempty"`

	// Required is the root-level required property list, stripped the same
	// way every other property path in these artifacts is.
	Required []string `json:"required,omitempty"`

	// Enums are the declared enum-valued properties.
	Enums []SchemaEnum `json:"enums,omitempty"`

	// UniqueNameProperty is this type's own client-supplied "Name"
	// property, when it has one - see [UniqueNameProperty].
	UniqueNameProperty *UniqueNameProperty `json:"unique_name_property,omitempty"`
}

// UniqueNameProperty is one type's own client-supplied "Name" property -
// the resource's own top-level Name, or the Name nested one level inside
// the single object a top-level property wraps (the CloudFront cache/
// origin-request policy shape: a top-level CachePolicyConfig property that
// is a plain $ref to definitions.CachePolicyConfig, itself carrying Name) -
// together with whether its own description states the value must be
// unique. See [findUniqueNameProperty] for the structural rule and issue
// #272 for what this feeds: tools/row-gen's two-source admission rule
// cross-checks DeclaredUnique here against the provider's own Argument
// Reference (tools/importdocs-gen's ArgumentRefEntry.DeclaredUnique), and
// only where both agree may a live object's property stand in for an
// ownership marker.
//
// It is deliberately narrower than "any property named Name anywhere in
// the schema". A "Name" nested inside an array-valued (repeated) sub-block
// - AWS::ResilienceHub::App's EventSubscriptions, AWS::MediaConnect::
// Gateway's Networks, AWS::SageMaker::ModelPackage's
// AdditionalInferenceSpecifications - names one member of a list, not the
// resource's own identity, and is never a candidate: [findUniqueNameProperty]
// skips any top-level property that carries "items" for exactly this
// reason.
//
// Measured at v6.59.0: this file's structural rule (resource-owned, either
// top level or a single wrapped config object) finds a Name property with
// a non-empty description on 374 types
// ([SchemaFactCounts.WithUniqueNameProperty]); of those, 40 have
// [declaredUniqueText]'s negation-aware test on their description
// ([SchemaFactCounts.UniqueNamePropertyDeclaredUnique]) - see
// tools/row-gen for the cross-check that reads that count against the
// provider's own docs. An unstructured scan for comparison (any "Name"
// property anywhere in a schema, no structural gate, matched with a bare
// substring test for "unique" and no negation-awareness) finds 58 types,
// not the same 40: the issue's own list of "12+ other resource types"
// names four of the difference (aws-sagemaker-modelpackage,
// aws-iotsitewise-assetmodel, aws-mediaconnect-gateway,
// aws-resiliencehub-app), and the structural gate excludes all four -
// each one's own top-level Name, where it has one at all, says nothing
// about uniqueness, even though a list member's Name does.
type UniqueNameProperty struct {
	// Path is the property's location, in Cloud Control's own nesting: a
	// one-element path for a top-level Name, a two-element path
	// ["CachePolicyConfig", "Name"] for one wrapped in the resource's
	// single mutable-config object.
	Path []string `json:"path"`

	// DeclaredUnique is whether the property's own "description" text
	// states the value must be unique - see declaredUniqueText, this
	// package's own copy of tools/importdocs-gen's negation-aware test,
	// applied to the registry's prose instead of the provider's.
	DeclaredUnique bool `json:"declared_unique"`
}

// SchemaEnum is one enum-valued property and its legal members.
type SchemaEnum struct {
	// Property is the enclosing "properties" or "definitions" key, as
	// relationships.go attributes the same way. Empty means the walk
	// reached an enum with no named ancestor, which the counts record.
	Property string `json:"property,omitempty"`

	// Pointer is where it was found, so an unattributed one is
	// inspectable rather than lost.
	Pointer string `json:"pointer,omitempty"`

	// Members are the declared values, in schema order. Order is kept
	// rather than sorted because sorting would silently change which value
	// "the first member" names, and a consumer picking deterministically is
	// picking from this ordering.
	//
	// It is not a meaningful ordering, and a consumer must not treat it as
	// one. AWS::S3::Bucket alone spells the same two-member Status enum both
	// ways: ["Disabled", "Enabled"] under ReplicationRule and
	// IntelligentTieringConfiguration, ["Enabled", "Disabled"] under Rule
	// and ReplicaModifications. So "the first member" is Enabled in some
	// places and Disabled in others, and a generator that picks it will
	// disable a feature in half the cases and enable it in the rest.
	// Picking by value ("the member that is not Disabled") is the rule that
	// survives; the schema's order only guarantees reproducibility.
	Members []string `json:"members"`
}

// buildSchemaFacts parses every schema for the three fields.
func buildSchemaFacts(pin SpecPin, schemas map[string][]byte) (SchemaFacts, error) {
	art := SchemaFacts{
		GeneratedBy: "tools/registry-gen",
		Pin:         pin,
	}

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fact, unattributed, err := parseSchemaFacts(schemas[name])
		if err != nil {
			return SchemaFacts{}, fmt.Errorf("%s: %w", name, err)
		}
		art.Counts.Types++
		art.Counts.EnumsUnattributed += unattributed

		if len(fact.HandlerPermissions) > 0 {
			art.Counts.WithHandlerPermissions++
			for _, acts := range fact.HandlerPermissions {
				art.Counts.HandlerPermissionActs += len(acts)
			}
		}
		if len(fact.Required) > 0 {
			art.Counts.WithRequired++
		}
		if len(fact.Enums) > 0 {
			art.Counts.WithEnums++
			for _, e := range fact.Enums {
				art.Counts.EnumMembers += len(e.Members)
			}
		}
		if fact.UniqueNameProperty != nil {
			art.Counts.WithUniqueNameProperty++
			if fact.UniqueNameProperty.DeclaredUnique {
				art.Counts.UniqueNamePropertyDeclaredUnique++
			}
		}
		art.Types = append(art.Types, fact)
	}
	return art, nil
}

// factsSchema is the slice of raw schema this extraction reads. Separate
// from cfnSchema deliberately: that type is the resolver's view and is
// mirrored into the embedded artifact, and widening it would widen the
// binary with fields the runtime never reads.
type factsSchema struct {
	TypeName string                  `json:"typeName"`
	Required []string                `json:"required"`
	Handlers map[string]factsHandler `json:"handlers"`
}

type factsHandler struct {
	Permissions []string `json:"permissions"`
}

// parseSchemaFacts extracts one schema's facts, returning how many enums it
// could not attribute to a name.
func parseSchemaFacts(raw []byte) (SchemaFact, int, error) {
	var s factsSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return SchemaFact{}, 0, fmt.Errorf("parsing schema: %w", err)
	}
	if s.TypeName == "" {
		return SchemaFact{}, 0, fmt.Errorf("schema carries no typeName")
	}

	fact := SchemaFact{
		TypeName: s.TypeName,
		Required: stripPointerPaths(s.Required),
	}

	for op, h := range s.Handlers {
		if len(h.Permissions) == 0 {
			continue
		}
		if fact.HandlerPermissions == nil {
			fact.HandlerPermissions = map[string][]string{}
		}
		acts := append([]string(nil), h.Permissions...)
		sort.Strings(acts)
		fact.HandlerPermissions[op] = acts
	}

	enums, unattributed := extractEnums(raw)
	fact.Enums = enums

	uniqueProp, err := findUniqueNameProperty(raw)
	if err != nil {
		return SchemaFact{}, 0, fmt.Errorf("finding the resource-owned Name property: %w", err)
	}
	fact.UniqueNameProperty = uniqueProp

	return fact, unattributed, nil
}

// uniqueNamePropSchema is the slice of raw schema [findUniqueNameProperty]
// reads: just enough of "properties" and "definitions" to find one
// resource-owned "Name" property and its own description, never the whole
// schema shape [factsSchema] and [cfnSchema] already type.
type uniqueNamePropSchema struct {
	Properties  map[string]uniqueNamePropNode `json:"properties"`
	Definitions map[string]uniqueNamePropNode `json:"definitions"`
}

type uniqueNamePropNode struct {
	Description string                        `json:"description"`
	Ref         string                        `json:"$ref"`
	Items       json.RawMessage               `json:"items"`
	Properties  map[string]uniqueNamePropNode `json:"properties"`
}

// definitionRefPrefix is the only $ref shape these schemas use for a
// same-document pointer - relative refs into another file do not appear in
// the CloudFormation Registry bundle.
const definitionRefPrefix = "#/definitions/"

// findUniqueNameProperty finds the resource's own client-supplied "Name"
// property, structurally rather than by name-matching anywhere in the
// tree - see [UniqueNameProperty]'s doc comment for why the distinction
// matters and what it measurably changes.
//
// Two shapes qualify, tried in that order:
//
//  1. A top-level "properties.Name" - the common case (AWS::EKS::Cluster,
//     AWS::Athena::DataCatalog, and 37 more at v6.59.0).
//  2. A top-level property that is a plain (non-array) $ref to a
//     "definitions" entry, when THAT entry itself has a "Name" property -
//     the CloudFront cache/origin-request/origin-access-control/response-
//     headers-policy shape, where CFN wraps the whole mutable config in one
//     object. A top-level property carrying "items" (an array) is skipped
//     outright: that is a repeated sub-block (AWS::ResilienceHub::App's
//     EventSubscriptions, AWS::MediaConnect::Gateway's Networks), and a
//     "Name" nested inside one names a member of the list, not the
//     resource itself.
//
// Iteration order over shape 2's candidate properties is the sorted
// property name, not Go's own randomized map order, so that a schema
// carrying more than one qualifying wrapped property (none does today)
// still gets a reproducible answer rather than one that varies by run.
//
// Returns nil, nil when neither shape is present - the honest "this type
// has no property here" answer, not an error.
func findUniqueNameProperty(raw []byte) (*UniqueNameProperty, error) {
	var doc uniqueNamePropSchema
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing schema: %w", err)
	}

	if top, ok := doc.Properties["Name"]; ok && top.Description != "" {
		return &UniqueNameProperty{
			Path:           []string{"Name"},
			DeclaredUnique: declaredUniqueText(top.Description),
		}, nil
	}

	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, pname := range names {
		pval := doc.Properties[pname]
		if len(pval.Items) > 0 {
			continue // an array-valued property wraps a repeated sub-block, not the resource's own singleton config
		}
		defName, ok := strings.CutPrefix(pval.Ref, definitionRefPrefix)
		if !ok {
			continue
		}
		def, ok := doc.Definitions[defName]
		if !ok {
			continue
		}
		nameProp, ok := def.Properties["Name"]
		if !ok || nameProp.Description == "" {
			continue
		}
		return &UniqueNameProperty{
			Path:           []string{pname, "Name"},
			DeclaredUnique: declaredUniqueText(nameProp.Description),
		}, nil
	}
	return nil, nil
}

// uniqueRe and uniqueNegatedRe mirror tools/importdocs-gen's
// declaredUniqueRe and declaredUniqueNegatedRe exactly - the same
// negation-aware test, applied to the registry's prose instead of the
// provider's Argument Reference. Kept as a separate copy rather than a
// shared import: two ten-line regexes are cheaper to duplicate than a new
// internal package a single generator-only signal would justify, and the
// two are read from different generators (registry-gen, importdocs-gen)
// that already do not share code today.
var uniqueRe = regexp.MustCompile(`(?i)\bunique\b`)
var uniqueNegatedRe = regexp.MustCompile(`(?i)\b(?:not|n't)\b[^.]{0,40}\bunique\b`)

// declaredUniqueText reports whether a schema property's own description
// states that its value must be unique.
func declaredUniqueText(desc string) bool {
	return uniqueRe.MatchString(desc) && !uniqueNegatedRe.MatchString(desc)
}

// extractEnums walks a raw schema for every enum, wherever it sits.
//
// A generic walk for the same reason relationships.go uses one: enums nest
// under definitions, under items for an array-valued property, and inside
// anyOf/oneOf alternatives, and a parser that knows only the shapes someone
// happened to look at drops the rest silently. Attribution follows the same
// rule - a "properties" or "definitions" key names its subschemas, and a
// deeper one wins - so an enum on a nested property is attributed to that
// property rather than to the definition enclosing it.
//
// What it cannot attribute it still records, with the pointer, and the
// artifact counts those. That count is the honest claim about this walk's
// reach; a zero means the shapes were all recognised, not that nothing was
// missed by looking.
func extractEnums(raw []byte) ([]SchemaEnum, int) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, 0
	}

	var out []SchemaEnum
	unattributed := 0

	var walk func(node any, property, pointer string)
	walk = func(node any, property, pointer string) {
		switch n := node.(type) {
		case map[string]any:
			if rawEnum, ok := n["enum"]; ok {
				if members, ok := decodeEnumMembers(rawEnum); ok {
					if property == "" {
						unattributed++
					}
					out = append(out, SchemaEnum{
						Property: property,
						Pointer:  pointer + "/enum",
						Members:  members,
					})
				}
			}
			for k, v := range n {
				if k == "properties" || k == "definitions" {
					if named, ok := v.(map[string]any); ok {
						for name, pv := range named {
							walk(pv, name, pointer+"/"+k+"/"+name)
						}
						continue
					}
				}
				if k == "enum" {
					continue // handled, and its members have no subschemas
				}
				walk(v, property, pointer+"/"+k)
			}
		case []any:
			for i, v := range n {
				walk(v, property, pointer+"/"+itoa(i))
			}
		}
	}
	walk(doc, "", "")

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Property != out[j].Property {
			return out[i].Property < out[j].Property
		}
		return out[i].Pointer < out[j].Pointer
	})
	return out, unattributed
}

// decodeEnumMembers reads an enum's values, rejecting a shape that carries
// none. Members are rendered as strings whatever their JSON type: the
// schemas carry numeric and boolean enums as well as string ones, and a
// consumer choosing a value writes it into HCL as text either way.
func decodeEnumMembers(node any) ([]string, bool) {
	list, ok := node.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case bool:
			out = append(out, fmt.Sprintf("%t", t))
		case float64:
			out = append(out, trimFloat(t))
		default:
			return nil, false // an object or array member is not an enum this can express
		}
	}
	return out, true
}

// trimFloat renders a JSON number without a trailing ".0", since every
// numeric enum member in the schemas is an integer.
func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// marshal renders the artifact the same way the registry one does.
func (a SchemaFacts) marshal() ([]byte, error) {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
