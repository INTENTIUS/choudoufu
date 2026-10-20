// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// RMTagsPath is the committed azurerm tag facts, relative to this
// package's directory.
const RMTagsPath = "azurerm_tags_generated.json"

//go:embed azurerm_tags_generated.json
var committedRMTags []byte

// CreateClass says when an azurerm resource's Create writes its tags.
type CreateClass string

const (
	// CreateInCreate: the create call's payload carries the tags.
	CreateInCreate CreateClass = "in-create"
	// CreateAfterCreate: the create call does not carry them and a later
	// call in the same Create does (Call names it). A stamped marker is
	// absent between the two, which is what
	// substrate's PostCreateNeeded answers for.
	CreateAfterCreate CreateClass = "after-create"
	// CreateUnknown: the analysis could not decide (Reason says why).
	CreateUnknown CreateClass = "unknown"
)

// TagRules is what azurerm's own validation of a resource's `tags`
// argument enforces, read from the schema's ValidateFunc. A zero limit
// means azurerm does not check it, which leaves ARM's own limit (see
// TagLimit, MaxKeyLength, MaxValueLength).
type TagRules struct {
	// Validator is the schema expression the rules were read from.
	Validator     string   `json:"validator"`
	MaxTags       int      `json:"max_tags,omitempty"`
	MaxKeyLen     int      `json:"max_key_length,omitempty"`
	MaxValueLen   int      `json:"max_value_length,omitempty"`
	LowercaseKeys bool     `json:"lowercase_keys,omitempty"`
	ForbiddenKeys []string `json:"forbidden_keys,omitempty"`
}

// RMTagRow is one azurerm resource type's tag facts.
type RMTagRow struct {
	Type   string      `json:"type"`
	Create CreateClass `json:"create"`
	// Call is the API call that carries the tags (the create call when
	// in-create, the later call when after-create), At its file:line in
	// the provider checkout.
	Call string `json:"call,omitempty"`
	At   string `json:"at,omitempty"`
	// CreateCall, on an after-create or unknown row, is the create call
	// that does not carry the tags.
	CreateCall string   `json:"create_call,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Rules      TagRules `json:"rules"`
}

// RMTags is the parsed or generated azurerm tag facts.
type RMTags struct {
	Version      string
	Commit       string
	SchemaSHA256 string
	// NotARMTags are the types whose `tags` argument is not ARM's tags
	// map (a list of labels kept in the service's own properties): never
	// a marker carrier, and so not rows.
	NotARMTags []string
	rows       map[string]RMTagRow
}

// RMTagCounts is the facts' summary.
type RMTagCounts struct {
	Tagged      int `json:"tagged"`
	InCreate    int `json:"in_create"`
	AfterCreate int `json:"after_create"`
	Unknown     int `json:"unknown"`
	// Limited counts types whose azurerm validation allows fewer than
	// DefaultTagLimit tags or stricter keys than ARM's.
	Limited int `json:"limited"`
}

func (t *RMTags) Counts() RMTagCounts {
	var c RMTagCounts
	for _, r := range t.rows {
		c.Tagged++
		switch r.Create {
		case CreateInCreate:
			c.InCreate++
		case CreateAfterCreate:
			c.AfterCreate++
		default:
			c.Unknown++
		}
		if (r.Rules.MaxTags > 0 && r.Rules.MaxTags < DefaultTagLimit) || (r.Rules.MaxKeyLen > 0 && r.Rules.MaxKeyLen < MaxKeyLength) ||
			r.Rules.LowercaseKeys || len(r.Rules.ForbiddenKeys) > 0 {
			c.Limited++
		}
	}
	return c
}

// Row returns one type's facts.
func (t *RMTags) Row(tfType string) (RMTagRow, bool) {
	r, ok := t.rows[tfType]
	return r, ok
}

// Rows returns every row, sorted by type.
func (t *RMTags) Rows() []RMTagRow {
	out := make([]RMTagRow, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

type rmTagsFile struct {
	GeneratedBy string      `json:"generated_by"`
	Source      rmTagsPin   `json:"source"`
	Counts      RMTagCounts `json:"counts"`
	NotARMTags  []string    `json:"tags_not_arm_tags"`
	Types       []RMTagRow  `json:"types"`
}

type rmTagsPin struct {
	Provider     string `json:"provider"`
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	SchemaSHA256 string `json:"schema_sha256"`
}

// Render writes the facts in their committed format.
func (t *RMTags) Render() []byte {
	doc := rmTagsFile{
		GeneratedBy: "tools/azurerm-tags-gen; DO NOT EDIT",
		Source:      rmTagsPin{Provider: "hashicorp/terraform-provider-azurerm", Version: t.Version, Commit: t.Commit, SchemaSHA256: t.SchemaSHA256},
		Counts:      t.Counts(),
		NotARMTags:  t.NotARMTags,
		Types:       t.Rows(),
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		panic(err) // only plain strings, ints and bools
	}
	return b.Bytes()
}

// ParseRMTags reads the committed format and checks the counts against
// the rows and each row's class against its fields.
func ParseRMTags(data []byte) (*RMTags, error) {
	var doc rmTagsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Source.Version == "" || len(doc.Source.Commit) != 40 || len(doc.Source.SchemaSHA256) != 64 {
		return nil, fmt.Errorf("source %+v does not name a version, a commit and a schema digest", doc.Source)
	}
	t := &RMTags{Version: doc.Source.Version, Commit: doc.Source.Commit, SchemaSHA256: doc.Source.SchemaSHA256, NotARMTags: doc.NotARMTags, rows: map[string]RMTagRow{}}
	for i, r := range doc.Types {
		if i > 0 && r.Type <= doc.Types[i-1].Type {
			return nil, fmt.Errorf("types are not sorted and distinct at %q", r.Type)
		}
		switch r.Create {
		case CreateInCreate:
			if r.Call == "" {
				return nil, fmt.Errorf("%s is in-create with no call", r.Type)
			}
		case CreateAfterCreate:
			if r.Call == "" || r.CreateCall == "" {
				return nil, fmt.Errorf("%s is after-create without both its create call and the call that writes the tags", r.Type)
			}
		case CreateUnknown:
			if r.Reason == "" {
				return nil, fmt.Errorf("%s is unknown with no reason", r.Type)
			}
		default:
			return nil, fmt.Errorf("%s has class %q", r.Type, r.Create)
		}
		t.rows[r.Type] = r
	}
	if got := t.Counts(); got != doc.Counts {
		return nil, fmt.Errorf("counts %+v disagree with the rows, which give %+v", doc.Counts, got)
	}
	return t, nil
}

var (
	rmOnce    sync.Once
	rmDefault *RMTags
)

// DefaultRMTags is the committed azurerm tag facts. It panics if the
// embedded file does not parse, which TestCommittedRMTagsParse turns into
// a test failure first.
func DefaultRMTags() *RMTags {
	rmOnce.Do(func() {
		t, err := ParseRMTags(committedRMTags)
		if err != nil {
			panic(fmt.Sprintf("armtypes: committed %s does not parse: %s", RMTagsPath, err))
		}
		rmDefault = t
	})
	return rmDefault
}

// PostCreateTags answers, for an azurerm resource type, whether its Create
// writes `tags` in a separate call after the create call returns. known is
// false for a type with no `tags` argument, a type the provider does not
// register, and a type the analysis classed unknown; a caller treats
// unknown as "may be after", the conservative answer. This is the Azure
// counterpart of live/registry.json's tag_on_create false, which
// substrate's AWS PostCreateNeeded reads.
func PostCreateTags(tfType string) (after bool, known bool) {
	r, ok := DefaultRMTags().rows[tfType]
	if !ok || r.Create == CreateUnknown {
		return false, false
	}
	return r.Create == CreateAfterCreate, true
}

// AzurermTagRules returns azurerm's own validation of a resource type's
// `tags`; ok is false for a type with no `tags` argument.
func AzurermTagRules(tfType string) (TagRules, bool) {
	r, ok := DefaultRMTags().rows[tfType]
	return r.Rules, ok
}
