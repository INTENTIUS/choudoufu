// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"sort"
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
	return fact, unattributed, nil
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
