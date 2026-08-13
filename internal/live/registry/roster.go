// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// The mapping artifact's via column vocabulary (tools/mapping-gen). Only
// viaName and viaAlias name a CFN type this package will hand out; see the
// package doc's "What counts as mapped".
const (
	viaName  = "name"
	viaAlias = "alias"
)

// mappingArtifact mirrors live/mapping.json's shape (tools/mapping-gen's
// Mapping), keeping only the fields this package reads.
type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

type mappingRow struct {
	TFType  string  `json:"tf_type"`
	CFNType *string `json:"cfn_type"`
	Via     string  `json:"via"`
}

// registryArtifact mirrors live/registry.json's shape (tools/registry-gen's
// RegistryArtifact), keeping only the fields this package reads.
type registryArtifact struct {
	Types []registryEntry `json:"types"`
}

type registryEntry struct {
	TypeName          string   `json:"type_name"`
	PrimaryIdentifier []string `json:"primary_identifier,omitempty"`
	Tagging           struct {
		Taggable bool `json:"taggable"`
	} `json:"tagging"`
	Handlers struct {
		List              bool     `json:"list"`
		ListRequiredInput []string `json:"list_required_input,omitempty"`
	} `json:"handlers"`
}

// Roster is live/mapping.json joined against live/registry.json, held in
// memory after one parse of each. The zero value is not useful; build one
// with [Load] or [Parse].
type Roster struct {
	// cfnType is tf_type -> cfn_type, for rows whose via is "name" or
	// "alias" only (see the package doc).
	cfnType map[string]string

	// tfTypeFor is cfn_type -> every tf_type live/mapping.json's "name" or
	// "alias" rows map to it, [Roster.TFTypesForCFNType]'s index. Built from
	// the same rows as cfnType, in the other direction: the ARN join (issue
	// #51, internal/live/discovery/tagging.go) has a CFN type in hand and
	// needs the TF type, not the other way around. Almost always one entry;
	// more than one means the CFN type's TF mapping is not unique, which
	// that join treats as ambiguous rather than picking one.
	tfTypeFor map[string][]string

	// listable is cfn_type -> whether live/registry.json's handlers.list is
	// set with no required input (see the package doc's "What counts as
	// listable"). A CFN type absent from live/registry.json - the mapping
	// artifact named one that the registry roster does not carry, which
	// should not happen for two artifacts generated from the same CFN
	// vintage but is not this package's place to assume - is simply absent
	// here too, and Listable reports false for it.
	listable map[string]bool

	// taggable is cfn_type -> live/registry.json's tagging.taggable.
	taggable map[string]bool

	// arity is cfn_type -> len(primary_identifier), the number of "|"-joined
	// segments a Cloud Control identifier for the type carries.
	arity map[string]int
}

// Load reads and parses the two artifacts from disk.
func Load(mappingPath, registryPath string) (*Roster, error) {
	mappingJSON, err := os.ReadFile(mappingPath) //nolint:gosec // caller-supplied path to a known artifact
	if err != nil {
		return nil, fmt.Errorf("registry: reading %s: %w", mappingPath, err)
	}
	registryJSON, err := os.ReadFile(registryPath) //nolint:gosec // caller-supplied path to a known artifact
	if err != nil {
		return nil, fmt.Errorf("registry: reading %s: %w", registryPath, err)
	}
	return Parse(mappingJSON, registryJSON)
}

// Parse builds a Roster from the two artifacts' JSON bytes directly, for a
// caller that already has them (a test fixture, an embedded copy, bytes
// fetched some other way) without going through the filesystem.
func Parse(mappingJSON, registryJSON []byte) (*Roster, error) {
	var m mappingArtifact
	if err := json.Unmarshal(mappingJSON, &m); err != nil {
		return nil, fmt.Errorf("registry: parsing the mapping artifact: %w", err)
	}
	var reg registryArtifact
	if err := json.Unmarshal(registryJSON, &reg); err != nil {
		return nil, fmt.Errorf("registry: parsing the registry artifact: %w", err)
	}

	r := &Roster{
		cfnType:   make(map[string]string, len(m.Rows)),
		tfTypeFor: make(map[string][]string, len(m.Rows)),
		listable:  make(map[string]bool, len(reg.Types)),
		taggable:  make(map[string]bool, len(reg.Types)),
		arity:     make(map[string]int, len(reg.Types)),
	}
	for _, row := range m.Rows {
		if (row.Via != viaName && row.Via != viaAlias) || row.CFNType == nil || *row.CFNType == "" {
			continue
		}
		r.cfnType[row.TFType] = *row.CFNType
		r.tfTypeFor[*row.CFNType] = append(r.tfTypeFor[*row.CFNType], row.TFType)
	}
	for _, e := range reg.Types {
		r.listable[e.TypeName] = e.Handlers.List && len(e.Handlers.ListRequiredInput) == 0
		r.taggable[e.TypeName] = e.Tagging.Taggable
		r.arity[e.TypeName] = len(e.PrimaryIdentifier)
	}
	return r, nil
}

// CloudControlType returns the CFN type live/mapping.json joins tfType to,
// and whether it named one at all (a "name" or "alias" row - see the
// package doc). A "fold" or "none" row, or a tfType the mapping artifact
// never saw, reports ok=false.
func (r *Roster) CloudControlType(tfType string) (cfnType string, ok bool) {
	if r == nil {
		return "", false
	}
	cfnType, ok = r.cfnType[tfType]
	return cfnType, ok
}

// TFTypesForCFNType returns every TF type live/mapping.json's "name" or
// "alias" rows map to cfnType, sorted - the reverse of
// [Roster.CloudControlType], for a caller that has a CFN type in hand and
// wants the TF type it names (the ARN join, issue #51: an ARN's service and
// resource segments join to a CFN type first, and only then to a TF type).
// nil for a CFN type nothing maps to. A result with more than one entry
// means the CFN type's TF mapping is not unique in the committed artifact;
// today's live/mapping.json never produces one, but a caller doing a reverse
// join must still treat it as ambiguous rather than picking the first.
func (r *Roster) TFTypesForCFNType(cfnType string) []string {
	if r == nil || len(r.tfTypeFor[cfnType]) == 0 {
		return nil
	}
	out := append([]string(nil), r.tfTypeFor[cfnType]...)
	sort.Strings(out)
	return out
}

// Listable reports whether cfnType can be enumerated with Cloud Control's
// ListResources and no required input (see the package doc's "What counts
// as listable"). False for a type live/registry.json never saw.
func (r *Roster) Listable(cfnType string) bool {
	if r == nil {
		return false
	}
	return r.listable[cfnType]
}

// Taggable reports live/registry.json's tagging.taggable for cfnType. False
// for a type live/registry.json never saw.
func (r *Roster) Taggable(cfnType string) bool {
	if r == nil {
		return false
	}
	return r.taggable[cfnType]
}

// IdentifierArity is the number of "|"-joined segments a Cloud Control
// identifier for cfnType carries - live/registry.json's
// len(primary_identifier). Zero for a type live/registry.json never saw,
// which is indistinguishable here from a (nonexistent) zero-part identifier;
// callers that need to tell those apart should check [Roster.Listable]
// first.
func (r *Roster) IdentifierArity(cfnType string) int {
	if r == nil {
		return 0
	}
	return r.arity[cfnType]
}

// EnumerationSource is the combined join a caller actually wants: the CFN
// type Cloud Control should be asked to list tfType through, when the
// mapping names one and the registry roster says it is listable. ok is
// false for anything short of both - unmapped, folded, or mapped to a type
// that turns out to require input to list - which is exactly the "neither"
// leg of discovery's enumeration-source selection (issue #47): the caller
// falls back to its existing refusal.
func (r *Roster) EnumerationSource(tfType string) (cfnType string, ok bool) {
	cfnType, mapped := r.CloudControlType(tfType)
	if !mapped || !r.Listable(cfnType) {
		return "", false
	}
	return cfnType, true
}
