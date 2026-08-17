// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestExtractUniqueNameFindsTheResourceName covers the two shapes the pinned
// bundle actually uses: a Name directly on the resource, and one inside the
// single config object CloudFront resources wrap their whole configuration in.
func TestExtractUniqueNameFindsTheResourceName(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name: "top-level name, AWS::Route53::CidrCollection shape",
			schema: `{
			  "typeName": "AWS::Route53::CidrCollection",
			  "properties": {
			    "Id": {"type": "string"},
			    "Name": {"type": "string", "description": "A unique name for the CIDR collection."}
			  },
			  "readOnlyProperties": ["/properties/Id"]
			}`,
			want: "Name",
		},
		{
			name: "name inside a config object, AWS::CloudFront::CachePolicy shape",
			schema: `{
			  "typeName": "AWS::CloudFront::CachePolicy",
			  "properties": {
			    "Id": {"type": "string"},
			    "CachePolicyConfig": {"$ref": "#/definitions/CachePolicyConfig"}
			  },
			  "definitions": {
			    "CachePolicyConfig": {
			      "type": "object",
			      "properties": {
			        "Name": {"type": "string", "description": "A unique name to identify the cache policy."}
			      }
			    }
			  },
			  "readOnlyProperties": ["/properties/Id"]
			}`,
			want: "CachePolicyConfig/Name",
		},
		{
			name: "no uniqueness claim, AWS::CloudFront::OriginAccessControl shape",
			schema: `{
			  "typeName": "AWS::CloudFront::OriginAccessControl",
			  "properties": {
			    "Id": {"type": "string"},
			    "OriginAccessControlConfig": {"$ref": "#/definitions/OriginAccessControlConfig"}
			  },
			  "definitions": {
			    "OriginAccessControlConfig": {
			      "type": "object",
			      "properties": {
			        "Name": {"type": "string", "description": "A name to identify the origin access control."}
			      }
			    }
			  }
			}`,
			want: "",
		},
		{
			name: "a server-minted Name is not a client-supplied one",
			schema: `{
			  "typeName": "Test::ReadOnly::Name",
			  "properties": {
			    "Name": {"type": "string", "description": "A unique name for the thing."}
			  },
			  "readOnlyProperties": ["/properties/Name"]
			}`,
			want: "",
		},
		{
			name: "a shallower Name wins over a deeper one",
			schema: `{
			  "typeName": "Test::Both::Name",
			  "properties": {
			    "Name": {"type": "string", "description": "A unique name for the resource."},
			    "Config": {"$ref": "#/definitions/Config"}
			  },
			  "definitions": {
			    "Config": {
			      "type": "object",
			      "properties": {
			        "Name": {"type": "string", "description": "A unique name for the config."}
			      }
			    }
			  }
			}`,
			want: "Name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractUniqueName([]byte(tc.schema))
			if err != nil {
				t.Fatalf("extractUniqueName: %v", err)
			}
			if got != tc.want {
				t.Errorf("extractUniqueName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExtractUniqueNameIgnoresListElements is the guard that no wording rule
// can supply. Each schema below is a real shape from the pinned bundle, cut
// down: a list of objects, each object carrying a Name that genuinely IS
// unique - among its siblings inside one resource.
//
// AWS::GameLift::Fleet's ScalingPolicy, AWS::SageMaker::ModelPackage's
// AdditionalInferenceSpecificationDefinition, AWS::ResilienceHub::App's
// EventSubscription and AWS::MediaConnect::Gateway's GatewayNetwork are all
// this shape, and a naive scan for a unique-worded Name finds all four.
// Binding a live resource by one would match every resource that happens to
// contain a sibling of that name - many objects, one identity, which is the
// wrong bind this whole mechanism is fenced against.
func TestExtractUniqueNameIgnoresListElements(t *testing.T) {
	cases := []struct{ name, schema string }{
		{
			name: "array with an inline item schema",
			schema: `{
			  "typeName": "Test::List::Inline",
			  "properties": {
			    "Policies": {
			      "type": "array",
			      "items": {
			        "type": "object",
			        "properties": {
			          "Name": {"type": "string", "description": "A unique name for the policy."}
			        }
			      }
			    }
			  }
			}`,
		},
		{
			name: "array whose items $ref a definition, AWS::GameLift::Fleet shape",
			schema: `{
			  "typeName": "Test::List::Ref",
			  "properties": {
			    "ScalingPolicies": {
			      "type": "array",
			      "items": {"$ref": "#/definitions/ScalingPolicy"}
			    }
			  },
			  "definitions": {
			    "ScalingPolicy": {
			      "type": "object",
			      "properties": {
			        "Name": {"type": "string", "description": "Policy names must be unique."}
			      }
			    }
			  }
			}`,
		},
		{
			name: "a property whose $ref resolves to an array definition",
			schema: `{
			  "typeName": "Test::List::RefArray",
			  "properties": {
			    "Specs": {"$ref": "#/definitions/SpecList"}
			  },
			  "definitions": {
			    "SpecList": {
			      "type": "array",
			      "properties": {
			        "Name": {"type": "string", "description": "A unique name for the spec."}
			      }
			    }
			  }
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractUniqueName([]byte(tc.schema))
			if err != nil {
				t.Fatalf("extractUniqueName: %v", err)
			}
			if got != "" {
				t.Errorf("extractUniqueName = %q, want \"\"; review this: a list element's name was read as the resource's own name, "+
					"which would bind one live object out of many that share it", got)
			}
		})
	}
}

// TestExtractUniqueNameTerminatesOnACyclicSchema: several bundle schemas have
// definitions that refer to each other, and a walk with no depth bound would
// not return.
func TestExtractUniqueNameTerminatesOnACyclicSchema(t *testing.T) {
	schema := `{
	  "typeName": "Test::Cycle",
	  "properties": {"A": {"$ref": "#/definitions/A"}},
	  "definitions": {
	    "A": {"type": "object", "properties": {"B": {"$ref": "#/definitions/B"}}},
	    "B": {"type": "object", "properties": {"A": {"$ref": "#/definitions/A"}}}
	  }
	}`
	got, err := extractUniqueName([]byte(schema))
	if err != nil {
		t.Fatalf("extractUniqueName: %v", err)
	}
	if got != "" {
		t.Errorf("extractUniqueName = %q, want \"\"", got)
	}
}
