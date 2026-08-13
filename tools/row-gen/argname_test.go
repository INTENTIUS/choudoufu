// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

func TestSnakeCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"BucketName", "bucket_name"},
		{"Name", "name"},
		{"ARN", "arn"},
		{"FunctionARN", "function_arn"},
		{"VPCId", "vpc_id"},
		{"Id", "id"},
	}
	for _, tt := range tests {
		if got := snakeCase(tt.in); got != tt.want {
			t.Errorf("snakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveArgName_PreferenceOrder(t *testing.T) {
	survey := map[string]surveyEntry{
		"aws_has_schema": {Type: "aws_has_schema", Identity: &struct {
			RequiredForImport []string `json:"required_for_import"`
		}{RequiredForImport: []string{"schema_name"}}},
	}
	carveSeed := map[string]string{
		"aws_has_schema": "carve_name", // present in both; schema must win
		"aws_carve_only": "carve_name",
	}

	arg, src, confident := resolveArgName("aws_has_schema", "SchemaName", survey, carveSeed)
	if arg != "schema_name" || src != argSourceIdentitySchema || !confident {
		t.Errorf("schema case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_carve_only", "CarveName", survey, carveSeed)
	if arg != "carve_name" || src != argSourceCarveSeed || !confident {
		t.Errorf("carve seed case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_neither", "SomeProperty", survey, carveSeed)
	if arg != "some_property" || src != argSourceGuessed || confident {
		t.Errorf("guess case: got (%q, %q, %v)", arg, src, confident)
	}
}
