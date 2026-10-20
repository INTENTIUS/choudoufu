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
	// ([ARMIDSegment]).
	Within         string
	ParentTemplate string
	ParentSegment  string

	// Provider: the provider configuration's value, as a cloud value
	// string ("subscription-id").
	Provider string

	// Constant: the same value for every object of the type (a DNS
	// record type).
	Constant string
}

type azurermIdentitySource struct {
	Segment        string `json:"segment"`
	Argument       string `json:"argument"`
	Within         string `json:"within"`
	ParentIDType   string `json:"parent_id_type"`
	ParentTemplate string `json:"parent_template"`
	ParentSegment  string `json:"parent_segment"`
	Provider       string `json:"provider"`
	Constant       string `json:"constant"`
}

// identitySources is the row's sources in this package's terms, nil when it
// has none.
func (t azurermIDType) identitySources() map[string]IdentitySource {
	if len(t.IdentitySources) == 0 {
		return nil
	}
	out := make(map[string]IdentitySource, len(t.IdentitySources))
	for attr, s := range t.IdentitySources {
		out[attr] = IdentitySource{
			Segment:        s.Segment,
			Argument:       s.Argument,
			Within:         s.Within,
			ParentTemplate: s.ParentTemplate,
			ParentSegment:  s.ParentSegment,
			Provider:       s.Provider,
			Constant:       s.Constant,
		}
	}
	return out
}

// ARMIDSegment parses id as an ARM id of the shape template and returns the
// value of its segment {segment}, the way a go-azure-sdk resourceids parser
// does: the two split on "/" into the same number of path segments, each
// fixed segment of the template equal to id's at the same position without
// regard to case (ARM's comparison), and each {value} segment a non-empty
// one. Nothing is matched by name or by search: a segment is its position,
// so an id of any other shape, or a template naming no such segment,
// answers false.
//
// It reads a template with one value per path segment, which is every
// parent template tools/azurerm-id-gen records (a scope segment, which
// spans several, is never one: [identitySources]' generator refuses it).
func ARMIDSegment(template, id, segment string) (string, bool) {
	if !strings.HasPrefix(template, "/") || !strings.HasPrefix(id, "/") {
		return "", false
	}
	tp := strings.Split(strings.TrimPrefix(template, "/"), "/")
	ip := strings.Split(strings.TrimPrefix(strings.TrimSuffix(id, "/"), "/"), "/")
	if len(tp) != len(ip) {
		return "", false
	}
	value, found := "", false
	for i, t := range tp {
		v := ip[i]
		if v == "" {
			return "", false
		}
		if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
			if t[1:len(t)-1] == segment {
				if found {
					return "", false
				}
				value, found = v, true
			}
			continue
		}
		if !strings.EqualFold(t, v) {
			return "", false
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
