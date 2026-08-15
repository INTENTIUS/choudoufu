// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"sort"
)

// This file is issue #151: CloudFormation resource schemas carry
// relationshipRef, AWS's own machine-readable declaration that one property
// references another type's property. It is exactly the foreign-key fact
// internal/live/identity infers from resource-type names, and this tool used
// to discard it.
//
// Coverage is thin - 26 of 1,683 schemas in the zip this was written
// against - so it is not the rule for anything on its own. It is the
// ratchet: where AWS declares a relationship, a derivation must agree with
// it, and a disagreement is a defect in the derivation rather than a row to
// except. The same discipline tools/row-gen/identityattr.go applies, where a
// rule is checked against the provider's wire schema rather than against
// itself.
//
// It also grows on its own. AWS has been populating relationshipRef since it
// was introduced, so a consumer written now gets denser evidence on every
// registry refresh at no further cost.

// Relationship is one declared foreign key: a property of this type whose
// value is a property of another type.
type Relationship struct {
	// Property is the schema property carrying the reference, as the
	// nearest enclosing "properties" or "definitions" key names it. Every
	// annotation in the roster this was written against attributes; an
	// empty one would mean a shape the walk has not learned, and the
	// artifact's relationships_unattributed count is what says so rather
	// than the row going quiet - see Pointer.
	Property string `json:"property,omitempty"`

	// Pointer is the JSON path the reference was found at, recorded so an
	// unattributed one is inspectable rather than lost.
	Pointer string `json:"pointer,omitempty"`

	// TypeName is the referenced CloudFormation type, e.g. AWS::EC2::VPC.
	TypeName string `json:"type_name"`

	// PropertyPath is the referenced property, stripped of its
	// /properties/ prefix the same way every other path in this artifact is.
	PropertyPath string `json:"property_path"`
}

// wireRelationshipRef is the schema's own shape for the annotation.
type wireRelationshipRef struct {
	TypeName     string `json:"typeName"`
	PropertyPath string `json:"propertyPath"`
}

// extractRelationships walks a raw schema for every relationshipRef,
// wherever it sits. Four shapes appear in the roster this was measured
// against: directly on a property (AWS::Redshift::EndpointAccess's
// VpcEndpointId), inside an anyOf alternative (AWS::DynamoDB::Table, which
// offers a KMS key by Arn or by KeyId), under items for an array-valued
// property (AWS::Redshift::ClusterSubnetGroup's subnet list), and hanging
// off a named definition with no properties map above it (RTBFabric's
// gateways, SSM::Association's S3BucketName).
//
// A generic walk rather than a struct field list, deliberately: the shapes
// above were found by looking, and a fourth would be silently dropped by a
// parser that only knew the first three. What the walk cannot attribute to a
// property it still records, with the pointer it was found at, so the count
// in the artifact is the whole truth rather than the part this code
// recognised.
func extractRelationships(raw []byte) ([]Relationship, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}

	var out []Relationship
	var walk func(node any, property, pointer string)
	walk = func(node any, property, pointer string) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["relationshipRef"]; ok {
				if rel, ok := decodeRelationshipRef(ref); ok {
					rel.Property = property
					rel.Pointer = pointer + "/relationshipRef"
					out = append(out, rel)
				}
			}
			for k, v := range n {
				// Descending through a "properties" map renames the current
				// property: its keys ARE property names. Everywhere else the
				// enclosing property name carries through, which is what
				// attributes an anyOf or items reference to the property that
				// owns it.
				if k == "properties" || k == "definitions" {
					// Both are name-to-subschema maps, so their keys name the
					// thing a nested relationshipRef belongs to. "properties"
					// is the direct case; "definitions" covers the shape the
					// RTBFabric and SSM::Association schemas use, where the
					// reference hangs off a named definition
					// (/definitions/VpcId/relationshipRef) with no properties
					// map above it. A deeper "properties" key still wins,
					// which is what keeps AWS::ApiGateway::RestApi's
					// VpcEndpointIds attributed to the property rather than to
					// the EndpointConfiguration definition enclosing it.
					if named, ok := v.(map[string]any); ok {
						for name, pv := range named {
							walk(pv, name, pointer+"/"+k+"/"+name)
						}
						continue
					}
				}
				if k == "relationshipRef" {
					continue // already handled, and it has no children worth walking
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

	sort.Slice(out, func(i, j int) bool {
		if out[i].Property != out[j].Property {
			return out[i].Property < out[j].Property
		}
		if out[i].TypeName != out[j].TypeName {
			return out[i].TypeName < out[j].TypeName
		}
		if out[i].PropertyPath != out[j].PropertyPath {
			return out[i].PropertyPath < out[j].PropertyPath
		}
		return out[i].Pointer < out[j].Pointer
	})
	return out, nil
}

// decodeRelationshipRef reads one annotation, rejecting a shape that does not
// carry both halves - a reference naming no type, or no property of it, says
// nothing a consumer could act on.
func decodeRelationshipRef(node any) (Relationship, bool) {
	m, ok := node.(map[string]any)
	if !ok {
		return Relationship{}, false
	}
	var wire wireRelationshipRef
	if s, ok := m["typeName"].(string); ok {
		wire.TypeName = s
	}
	if s, ok := m["propertyPath"].(string); ok {
		wire.PropertyPath = s
	}
	if wire.TypeName == "" || wire.PropertyPath == "" {
		return Relationship{}, false
	}
	return Relationship{
		TypeName:     wire.TypeName,
		PropertyPath: stripPointerPath(wire.PropertyPath),
	}, true
}

// itoa is strconv.Itoa without the import, for array indices in a pointer.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
