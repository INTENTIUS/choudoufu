// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Per-type signal parsing (issue #42). chant's own CloudFormation Registry
// parser (lexicons/aws/src/spec/parse.ts) reads primaryIdentifier,
// readOnly/createOnly/writeOnlyProperties and tagging to generate code, and
// discards the handlers section entirely. This parser keeps handlers - the
// load-bearing novelty over chant's: handlers.list is what tells whether a
// live resource of a type can be enumerated at all, freely or only given a
// parent's input, which is exactly the admission vocabulary choudoufu's
// live markers need.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cfnSchema is the slice of the raw CloudFormation Registry JSON Schema
// this tool reads. The full schema also carries properties, definitions and
// permissions the resource model doesn't need (chant's parser handles the
// codegen-relevant rest); this type stops at the fields issue #42 asks for.
type cfnSchema struct {
	TypeName              string       `json:"typeName"`
	PrimaryIdentifier     []string     `json:"primaryIdentifier"`
	AdditionalIdentifiers [][]string   `json:"additionalIdentifiers"`
	ReadOnlyProperties    []string     `json:"readOnlyProperties"`
	CreateOnlyProperties  []string     `json:"createOnlyProperties"`
	WriteOnlyProperties   []string     `json:"writeOnlyProperties"`
	Tagging               *cfnTagging  `json:"tagging"`
	Handlers              *cfnHandlers `json:"handlers"`
}

type cfnTagging struct {
	Taggable     bool `json:"taggable"`
	TagOnCreate  bool `json:"tagOnCreate"`
	TagUpdatable bool `json:"tagUpdatable"`
}

// cfnHandlers is the five CloudFormation handler operations. Only their
// presence matters here, except list: its handlerSchema.required is the one
// nested field this parser reaches into, so a bare pointer marks presence
// for the rest.
type cfnHandlers struct {
	Create *cfnHandlerStub `json:"create"`
	Read   *cfnHandlerStub `json:"read"`
	Update *cfnHandlerStub `json:"update"`
	Delete *cfnHandlerStub `json:"delete"`
	List   *cfnListHandler `json:"list"`
}

// cfnHandlerStub is deliberately empty: create/read/update/delete are
// consulted only for whether the key exists at all (a non-nil pointer after
// unmarshaling), never for their permissions list.
type cfnHandlerStub struct{}

type cfnListHandler struct {
	HandlerSchema *cfnHandlerSchema `json:"handlerSchema"`
}

type cfnHandlerSchema struct {
	// Required is the load-bearing field: handlers.list.handlerSchema.required
	// names the parent-identifying input a List call needs, splitting list
	// handlers into freely listable (no entry, or an empty one) and
	// listable-given-a-parent (a non-empty one).
	Required []string `json:"required"`
}

// Tagging is an entry's tagging facet, always present (defaulting to
// untaggable) so its absence in a schema is distinguishable in the artifact
// from an explicit "not taggable".
type Tagging struct {
	Taggable     bool `json:"taggable"`
	TagOnCreate  bool `json:"tag_on_create"`
	TagUpdatable bool `json:"tag_updatable"`
}

// Enumerability classifies a type's list handler for the artifact's
// headline counts (issue #42's reference values): whether a live resource
// of the type can be listed at all, and if so, whether listing needs a
// parent's input.
type Enumerability string

const (
	// EnumerabilityFree: a list handler exists and needs no required input
	// - handlers.list is present with no handlerSchema.required entries.
	EnumerabilityFree Enumerability = "free"
	// EnumerabilityParentInput: a list handler exists but needs at least
	// one required input (handlers.list.handlerSchema.required is
	// non-empty) - typically a parent resource's identifier.
	EnumerabilityParentInput Enumerability = "parent-input"
	// EnumerabilityNone: no working list handler - either handlers.list is
	// absent, or the type has no handlers section at all.
	EnumerabilityNone Enumerability = "none"
)

// Handlers is which of the five CloudFormation handler operations a type
// implements, plus the list handler's required input when it declares one.
type Handlers struct {
	Create bool `json:"create"`
	Read   bool `json:"read"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
	List   bool `json:"list"`

	// ListRequiredInput is handlers.list.handlerSchema.required, verbatim
	// (property names, not JSON-pointer paths - the handler schema names
	// them directly). Empty when List is false, or when it's true but
	// declares no required input.
	ListRequiredInput []string `json:"list_required_input,omitempty"`
}

// Enumerability derives the type's Enumerability from its handlers.
func (h Handlers) Enumerability() Enumerability {
	switch {
	case !h.List:
		return EnumerabilityNone
	case len(h.ListRequiredInput) > 0:
		return EnumerabilityParentInput
	default:
		return EnumerabilityFree
	}
}

// HasAny reports whether the type has a handlers section at all - false is
// the "168 with no handlers at all" cohort in issue #42's reference values,
// a subset of Enumerability() == EnumerabilityNone.
func (h Handlers) HasAny() bool {
	return h.Create || h.Read || h.Update || h.Delete || h.List
}

// Entry is one resource type's parsed registry signals.
type Entry struct {
	TypeName string `json:"type_name"`

	// PrimaryIdentifier is the property (or properties, for a compound
	// key) that identifies a live resource of this type - the arity issue
	// #42's reference values report per type (single/double/3+).
	PrimaryIdentifier []string `json:"primary_identifier,omitempty"`

	// AdditionalIdentifiers are alternative compound keys that also
	// identify a live resource, each stripped the same way
	// PrimaryIdentifier is.
	AdditionalIdentifiers [][]string `json:"additional_identifiers,omitempty"`

	ReadOnlyProperties   []string `json:"read_only_properties,omitempty"`
	CreateOnlyProperties []string `json:"create_only_properties,omitempty"`
	WriteOnlyProperties  []string `json:"write_only_properties,omitempty"`

	Tagging  Tagging  `json:"tagging"`
	Handlers Handlers `json:"handlers"`

	// Relationships are the schema's own relationshipRef annotations: each
	// a declared foreign key from one of this type's properties to another
	// type's. Thin coverage by design of the upstream data, not of this
	// parser - see relationships.go.
	Relationships []Relationship `json:"relationships,omitempty"`
}

// parseType parses one raw schema (as extractSchemas keys it, by typeName)
// into an Entry.
func parseType(raw []byte) (Entry, error) {
	var schema cfnSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return Entry{}, fmt.Errorf("parsing schema: %w", err)
	}
	if schema.TypeName == "" {
		return Entry{}, fmt.Errorf("schema carries no typeName")
	}

	e := Entry{
		TypeName:             schema.TypeName,
		PrimaryIdentifier:    stripPointerPaths(schema.PrimaryIdentifier),
		ReadOnlyProperties:   stripPointerPaths(schema.ReadOnlyProperties),
		CreateOnlyProperties: stripPointerPaths(schema.CreateOnlyProperties),
		WriteOnlyProperties:  stripPointerPaths(schema.WriteOnlyProperties),
	}
	for _, ids := range schema.AdditionalIdentifiers {
		e.AdditionalIdentifiers = append(e.AdditionalIdentifiers, stripPointerPaths(ids))
	}
	if schema.Tagging != nil {
		e.Tagging = Tagging{
			Taggable:     schema.Tagging.Taggable,
			TagOnCreate:  schema.Tagging.TagOnCreate,
			TagUpdatable: schema.Tagging.TagUpdatable,
		}
	}
	if schema.Handlers != nil {
		h := schema.Handlers
		e.Handlers = Handlers{
			Create: h.Create != nil,
			Read:   h.Read != nil,
			Update: h.Update != nil,
			Delete: h.Delete != nil,
			List:   h.List != nil,
		}
		if h.List != nil && h.List.HandlerSchema != nil && len(h.List.HandlerSchema.Required) > 0 {
			e.Handlers.ListRequiredInput = append([]string(nil), h.List.HandlerSchema.Required...)
		}
	}

	rels, err := extractRelationships(raw)
	if err != nil {
		return Entry{}, fmt.Errorf("%s: reading relationshipRef annotations: %w", schema.TypeName, err)
	}
	e.Relationships = rels

	return e, nil
}

// stripPointerPath strips a leading "/properties/" from a CloudFormation
// Registry JSON-pointer path, the same minimal transform chant's
// stripPointerPath applies: a deeper path like
// "/properties/Endpoint/Address" becomes "Endpoint/Address", not something
// further reshaped - the pointer nesting stays legible.
func stripPointerPath(path string) string {
	return strings.TrimPrefix(path, "/properties/")
}

func stripPointerPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = stripPointerPath(p)
	}
	return out
}
