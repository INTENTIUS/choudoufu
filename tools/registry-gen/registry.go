// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// RegistryArtifact is the committed artifact: live/registry.json.
type RegistryArtifact struct {
	// Pin is the accepted spec this artifact was generated under (see
	// pinned_spec.go), not necessarily the digest of whatever bundle
	// produced this particular run - AssertPinned is what decides whether a
	// run may proceed at all, this field just records which acceptance it
	// proceeded under.
	Pin SpecPin `json:"pin"`

	// GeneratedBy names the tool so a reader of the JSON alone knows where
	// rows come from and how to refresh them.
	GeneratedBy string `json:"generated_by"`

	// Counts are the roster-wide headline totals (issue #42's reference
	// values).
	Counts RegistryCounts `json:"counts"`

	// Types has one row per extracted resource type, sorted by type name.
	Types []Entry `json:"types"`
}

// RegistryCounts are the roster-wide totals.
type RegistryCounts struct {
	// Types is the total schema count.
	Types int `json:"types"`

	// WithPrimaryIdentifier is how many types carry a primaryIdentifier.
	WithPrimaryIdentifier int `json:"with_primary_identifier"`
	// PrimaryIdentifierArity buckets WithPrimaryIdentifier by key arity.
	PrimaryIdentifierArity PrimaryIdentifierArity `json:"primary_identifier_arity"`

	// Taggable is how many types have tagging.taggable set.
	Taggable int `json:"taggable"`

	// ListFree, ListWithRequiredInput and NoListHandler partition Types by
	// Enumerability; NoHandlersAtAll is the subset of NoListHandler with no
	// handlers section at all (Handlers.HasAny() false).
	ListFree              int `json:"list_free"`
	ListWithRequiredInput int `json:"list_with_required_input"`
	NoListHandler         int `json:"no_list_handler"`
	NoHandlersAtAll       int `json:"no_handlers_at_all"`

	// WithRead is how many types implement the read handler.
	WithRead int `json:"with_read"`

	// WithRelationships is how many types carry at least one
	// relationshipRef annotation, and Relationships the total number of
	// annotations across the roster. Both are reported because the second
	// is what a consumer joins on and the first is what says how thin the
	// coverage is (issue #151).
	WithRelationships int `json:"with_relationships"`
	Relationships     int `json:"relationships"`

	// RelationshipsUnattributed is the subset of Relationships the walk
	// could not tie to an enclosing schema property. Counted rather than
	// dropped: a zero here is the claim that every annotation found has a
	// property name, and a non-zero one is a shape relationships.go has
	// not learned to attribute yet.
	RelationshipsUnattributed int `json:"relationships_unattributed"`
}

// PrimaryIdentifierArity buckets primaryIdentifier key arity: a single
// property, a compound key of exactly two, or three or more.
type PrimaryIdentifierArity struct {
	Single      int `json:"single"`
	Double      int `json:"double"`
	ThreeOrMore int `json:"three_or_more"`
}

// buildRegistry parses every extracted schema and assembles the artifact,
// sorted by type name.
func buildRegistry(pin SpecPin, schemas map[string][]byte) (RegistryArtifact, error) {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	art := RegistryArtifact{
		Pin:         pin,
		GeneratedBy: "tools/registry-gen (go run ./tools/registry-gen)",
	}
	for _, name := range names {
		entry, err := parseType(schemas[name])
		if err != nil {
			return RegistryArtifact{}, fmt.Errorf("%s: %w", name, err)
		}
		tallyEntry(&art.Counts, entry)
		art.Types = append(art.Types, entry)
	}
	return art, nil
}

// tallyEntry folds one parsed Entry into the running counts.
func tallyEntry(c *RegistryCounts, e Entry) {
	c.Types++

	if len(e.PrimaryIdentifier) > 0 {
		c.WithPrimaryIdentifier++
		switch len(e.PrimaryIdentifier) {
		case 1:
			c.PrimaryIdentifierArity.Single++
		case 2:
			c.PrimaryIdentifierArity.Double++
		default:
			c.PrimaryIdentifierArity.ThreeOrMore++
		}
	}

	if e.Tagging.Taggable {
		c.Taggable++
	}

	switch e.Handlers.Enumerability() {
	case EnumerabilityFree:
		c.ListFree++
	case EnumerabilityParentInput:
		c.ListWithRequiredInput++
	case EnumerabilityNone:
		c.NoListHandler++
		if !e.Handlers.HasAny() {
			c.NoHandlersAtAll++
		}
	}

	if e.Handlers.Read {
		c.WithRead++
	}

	if len(e.Relationships) > 0 {
		c.WithRelationships++
		c.Relationships += len(e.Relationships)
		for _, r := range e.Relationships {
			if r.Property == "" {
				c.RelationshipsUnattributed++
			}
		}
	}
}

// marshal renders the artifact deterministically: sorted rows (buildRegistry
// already sorts them), two-space indent, trailing newline, no HTML
// escaping - the same shape survey.json's marshal uses.
func (a RegistryArtifact) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
