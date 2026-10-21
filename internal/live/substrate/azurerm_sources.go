// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"strings"
)

// GitHub issue #2138, the maintainer's ruling (option (a), 2026-10-20):
// the record rung for azurerm identity-schema types whose identity names a
// parent attribute the block supplies only through a parent's ARM id
// argument. tools/azurerm-id-gen records, per identity attribute, the
// source azurerm states for it twice (its identity schema's segment naming
// and its generated identity test), as the row's identity_sources; this
// file reads them and parses a parent's id the way azurerm's id parsers
// do. A type with no such sources carries identity_sources_problem, which
// is the refusal.

// IdentitySource is where one identity attribute's value comes from.
// Segment is the type's own id segment the attribute is; exactly one of the
// rest is set.
type IdentitySource struct {
	// Segment is the segment of the type's own ARM id template this
	// attribute is, under the parser's name ("storageAccountName").
	Segment string

	// Argument: the value is this argument's, read off the applied object.
	// It differs from the attribute's own name only where azurerm's
	// identity test maps the two explicitly (dns_zone_name is zone_name).
	Argument string

	// Within: the value is segment ParentSegment of this argument's value,
	// an ARM id of the shape ParentTemplate, parsed as azurerm parses it
	// ([ARMIDSegment]): ParentSegments is that template segment by segment
	// with each one's kind, and ParentInsensitive is whether azurerm's
	// validator for the argument parses it insensitively (GitHub issue
	// #2140).
	Within            string
	ParentTemplate    string
	ParentSegment     string
	ParentSegments    []ARMIDPart
	ParentInsensitive bool

	// Provider: the provider configuration's value, as a cloud value
	// string ("subscription-id").
	Provider string

	// Constant: the same value for every object of the type (a DNS
	// record type).
	Constant string
}

type azurermIdentitySource struct {
	Segment        string      `json:"segment"`
	Argument       string      `json:"argument"`
	Within         string      `json:"within"`
	ParentIDType   string      `json:"parent_id_type"`
	ParentTemplate string      `json:"parent_template"`
	ParentSegment  string      `json:"parent_segment"`
	ParentSegments []ARMIDPart `json:"parent_segments"`
	ParentParse    string      `json:"parent_parse"`
	Provider       string      `json:"provider"`
	Constant       string      `json:"constant"`
}

// ARMIDPart is one path segment of an ARM id as go-azure-helpers'
// resourceids declares it, recorded by tools/azurerm-id-gen from the id
// type's Segments(): Kind "fixed" (a static or resource-provider segment,
// whose text is Value), "constant" (a constant segment, one of Values) or
// "value" (a subscription, resource group or user-specified segment, any
// non-empty text, under Name).
type ARMIDPart struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name,omitempty"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

// The kinds of [ARMIDPart].
const (
	ARMIDFixed    = "fixed"
	ARMIDConstant = "constant"
	ARMIDValue    = "value"
)

// identitySources is the row's sources in this package's terms, nil when it
// has none.
func (t azurermIDType) identitySources() map[string]IdentitySource {
	if len(t.IdentitySources) == 0 {
		return nil
	}
	out := make(map[string]IdentitySource, len(t.IdentitySources))
	for attr, s := range t.IdentitySources {
		out[attr] = IdentitySource{
			Segment:           s.Segment,
			Argument:          s.Argument,
			Within:            s.Within,
			ParentTemplate:    s.ParentTemplate,
			ParentSegment:     s.ParentSegment,
			ParentSegments:    s.ParentSegments,
			ParentInsensitive: s.ParentParse == "insensitive",
			Provider:          s.Provider,
			Constant:          s.Constant,
		}
	}
	return out
}

// ARMIDSegment parses id as an ARM id of the segments parts and returns the
// value of the segment named segment, the way go-azure-helpers'
// resourceids.Parser.Parse does for an id type with no scope segment
// (parse.go at the version azurerm 5.10.0 vendors): the id splits on "/"
// into exactly as many path segments as parts; a fixed part equals its
// text, a constant part equals one of its possible values, and a value part
// is any non-empty text. With insensitive false, as ParseXxxID and every
// ValidateXxxID parse, fixed and constant parts compare case-sensitively;
// with insensitive true, as ParseXxxIDInsensitively parses, they compare
// without regard to case. Nothing is matched by search: a segment is its
// position, so an id of any other shape, a wrong constant, or parts naming
// no such segment answer false.
//
// One trailing "/" is accepted, as Parse accepts it: Parse trims each
// segment and the "/" after it off the input as it goes, and an input with
// nothing left over is whole.
func ARMIDSegment(parts []ARMIDPart, insensitive bool, id, segment string) (string, bool) {
	if len(parts) == 0 || !strings.HasPrefix(id, "/") {
		return "", false
	}
	ip := strings.Split(strings.TrimSuffix(strings.TrimPrefix(id, "/"), "/"), "/")
	if len(ip) != len(parts) {
		return "", false
	}
	same := func(want, got string) bool {
		if insensitive {
			return strings.EqualFold(want, got)
		}
		return want == got
	}
	value, found := "", false
	for i, p := range parts {
		v := ip[i]
		if v == "" {
			return "", false
		}
		switch p.Kind {
		case ARMIDFixed:
			if !same(p.Value, v) {
				return "", false
			}
		case ARMIDConstant:
			ok := false
			for _, c := range p.Values {
				if same(c, v) {
					// Parse answers the declared spelling.
					v, ok = c, true
					break
				}
			}
			if !ok {
				return "", false
			}
		case ARMIDValue:
		default:
			return "", false
		}
		if p.Kind != ARMIDFixed && p.Name == segment {
			if found {
				return "", false
			}
			value, found = v, true
		}
	}
	return value, found
}

// azurermRecordImportIDFromIdentity rebuilds row's ARM id from a record
// keyed by identity attribute, through the row's identity sources: each
// {segment} of the type's own template is the value of the identity
// attribute that segment is. ok is false for a row with no sources, or a
// record missing any of the attributes: a hole is refused, never filled.
func azurermRecordImportIDFromIdentity(row azurermIDType, values map[string]string) (string, bool) {
	if len(row.IdentitySources) == 0 || row.Template == "" {
		return "", false
	}
	bySegment := make(map[string]string, len(row.IdentitySources))
	for attr, s := range row.IdentitySources {
		v := values[attr]
		if v == "" {
			return "", false
		}
		bySegment[s.Segment] = v
	}
	parts := strings.Split(row.Template, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			v, ok := bySegment[p[1:len(p)-1]]
			if !ok {
				return "", false
			}
			parts[i] = v
		}
	}
	return strings.Join(parts, "/"), true
}
