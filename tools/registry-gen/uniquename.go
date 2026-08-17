// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/uniquename"
)

// maxUniqueNameDepth bounds the walk below. The deepest resource-level Name
// in the 1683 schemas of the pinned bundle sits one object in
// (CachePolicyConfig/Name); three is slack, and the bound also guarantees
// termination on a schema whose definitions refer to each other in a cycle,
// which several do.
const maxUniqueNameDepth = 3

// extractUniqueName finds the property that carries this resource type's own
// client-supplied name, when the schema's description of that property states
// the name is unique across an account and region - the registry half of the
// two-source evidence GitHub issue #272 requires.
//
// It returns the property's path from the resource root, "/"-joined the same
// way [stripPointerPath] leaves a nested pointer legible:
// "CachePolicyConfig/Name" for AWS::CloudFront::CachePolicy, "Name" for
// AWS::Route53::CidrCollection. Empty means no such property, which is the
// answer for all but a few dozen types.
//
// The path is not decoration. A caller binding a live object by its name has
// to read that name back off a Cloud Control ResourceDescription's Properties
// map, and the path is where in that map to look.
//
// # Why the walk refuses to cross an array
//
// The word "unique" appears on a Name property in 56 of the pinned schemas,
// and a third of those are the name of an element of a LIST inside the
// resource, not the resource's own name: AWS::GameLift::Fleet's
// ScalingPolicy.Name, AWS::SageMaker::ModelPackage's
// AdditionalInferenceSpecificationDefinition.Name, AWS::ResilienceHub::App's
// EventSubscription.Name, AWS::MediaConnect::Gateway's GatewayNetwork.Name.
// Each of those is unique among its siblings inside one resource and says so
// plainly; none of them identifies the resource. Reading one as the
// resource's name would bind a live object by a value many live objects share
// - the exact wrong bind this whole mechanism is fenced against.
//
// The distinction is structural, not lexical, so it needs no list of types:
// the resource's own name is reachable from the root through object-valued
// properties alone.
//
// The mechanism that achieves that is the walk NOT FOLLOWING "items". A CFN
// list property carries its element schema under items, usually as a $ref
// into definitions, and [property] has no items field at all, so the walk
// simply cannot descend into one. The explicit "type": "array" checks below
// are belt and braces on top of that; measured against the pinned bundle,
// removing both leaves the artifact byte-identical, because items was already
// unreachable. They are kept as the statement of intent, and
// TestExtractUniqueNameIgnoresListElements is what actually holds the line.
//
// A read-only Name is refused for the same reason in the other direction: it
// is a value the service mints, so no configuration states it and there would
// be nothing to match against.
func extractUniqueName(raw []byte) (string, error) {
	var doc struct {
		Properties         map[string]json.RawMessage `json:"properties"`
		Definitions        map[string]json.RawMessage `json:"definitions"`
		ReadOnlyProperties []string                   `json:"readOnlyProperties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("reading properties: %w", err)
	}
	readOnly := make(map[string]bool, len(doc.ReadOnlyProperties))
	for _, p := range doc.ReadOnlyProperties {
		readOnly[stripPointerPath(p)] = true
	}
	w := uniqueNameWalk{defs: doc.Definitions, readOnly: readOnly}
	return w.search(doc.Properties, nil, 0), nil
}

type uniqueNameWalk struct {
	defs     map[string]json.RawMessage
	readOnly map[string]bool
}

// property is the slice of a JSON Schema property node this walk reads.
type property struct {
	Type        string                     `json:"type"`
	Description string                     `json:"description"`
	Ref         string                     `json:"$ref"`
	Properties  map[string]json.RawMessage `json:"properties"`
}

// search returns the first path under props whose final segment is a Name
// asserting account-scoped uniqueness, in the ordering described below.
//
// Sorted iteration is not decoration either: Go map order is random, a schema
// can hold more than one candidate, and a generator whose artifact changes
// between runs on the same input is a generator whose diff cannot be read.
// Shallower paths win over deeper ones, and lexical order breaks the
// remaining ties, so the resource's own top-level Name is preferred to one
// nested inside a config object.
func (w uniqueNameWalk) search(props map[string]json.RawMessage, prefix []string, depth int) string {
	if depth > maxUniqueNameDepth || len(props) == 0 {
		return ""
	}
	// Shallowest first: settle every candidate at this level before
	// descending, so depth alone decides between a top-level Name and a
	// nested one.
	for _, key := range sortedKeys(props) {
		if key != "Name" {
			continue
		}
		p, ok := w.decode(props[key])
		if !ok {
			continue
		}
		path := strings.Join(append(append([]string(nil), prefix...), key), "/")
		if w.readOnly[path] {
			continue
		}
		if uniquename.Asserted(p.Description) {
			return path
		}
	}
	for _, key := range sortedKeys(props) {
		p, ok := w.decode(props[key])
		if !ok {
			continue
		}
		// An array's elements are not the resource. See the function's own
		// comment on extractUniqueName for the six schemas this rules out.
		if p.Type == "array" {
			continue
		}
		nested := p.Properties
		if p.Ref != "" {
			d, ok := w.definition(p.Ref)
			if !ok {
				continue
			}
			if d.Type == "array" {
				continue
			}
			nested = d.Properties
		}
		if got := w.search(nested, append(append([]string(nil), prefix...), key), depth+1); got != "" {
			return got
		}
	}
	return ""
}

func (w uniqueNameWalk) decode(raw json.RawMessage) (property, bool) {
	var p property
	if json.Unmarshal(raw, &p) != nil {
		return property{}, false
	}
	return p, true
}

// definition resolves a local "#/definitions/X" reference. A reference this
// bundle does not carry locally - none do today - resolves to nothing rather
// than to an empty node, so a future remote $ref cannot read as "an object
// with no Name in it".
func (w uniqueNameWalk) definition(ref string) (property, bool) {
	const prefix = "#/definitions/"
	if !strings.HasPrefix(ref, prefix) {
		return property{}, false
	}
	raw, ok := w.defs[strings.TrimPrefix(ref, prefix)]
	if !ok {
		return property{}, false
	}
	return w.decode(raw)
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
