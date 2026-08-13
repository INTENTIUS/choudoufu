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
	trueVal := true
	falseVal := false

	survey := map[string]surveyEntry{
		"aws_has_schema": {Type: "aws_has_schema", Identity: &struct {
			RequiredForImport []string `json:"required_for_import"`
		}{RequiredForImport: []string{"schema_name"}}},
	}
	importGrammar := map[string]importGrammarRow{
		// Present alongside a schema row too, to pin that the schema still
		// wins - checked below.
		"aws_has_schema":   {ComposedOfArguments: &trueVal, Arguments: []string{"grammar_name"}},
		"aws_grammar_only": {ComposedOfArguments: &trueVal, Arguments: []string{"grammar_name"}},
		// Present in both import-grammar and the carve seed; import-grammar
		// must win now that it outranks the carve seed.
		"aws_grammar_and_carve": {ComposedOfArguments: &trueVal, Arguments: []string{"grammar_name"}},
		// Not composed of arguments at all - falls through despite naming
		// exactly one argument.
		"aws_grammar_opaque": {ComposedOfArguments: &falseVal, Arguments: []string{"grammar_name"}},
		// Composed of arguments, but more than one - falls through rather
		// than guess which one the CFN property corresponds to.
		"aws_grammar_multi": {ComposedOfArguments: &trueVal, Arguments: []string{"a", "b"}},
	}
	carveSeed := map[string]string{
		"aws_has_schema":        "carve_name", // present in all three; schema must win
		"aws_carve_only":        "carve_name",
		"aws_grammar_and_carve": "carve_name", // import-grammar must win over this
		"aws_grammar_opaque":    "carve_name", // import-grammar falls through; carve seed answers
		"aws_grammar_multi":     "carve_name", // same
	}

	arg, src, confident := resolveArgName("aws_has_schema", "SchemaName", survey, importGrammar, carveSeed)
	if arg != "schema_name" || src != argSourceIdentitySchema || !confident {
		t.Errorf("schema case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_grammar_only", "GrammarName", survey, importGrammar, carveSeed)
	if arg != "grammar_name" || src != argSourceImportGrammar || !confident {
		t.Errorf("import-grammar case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_grammar_and_carve", "GrammarName", survey, importGrammar, carveSeed)
	if arg != "grammar_name" || src != argSourceImportGrammar || !confident {
		t.Errorf("import-grammar-over-carve-seed case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_grammar_opaque", "CarveName", survey, importGrammar, carveSeed)
	if arg != "carve_name" || src != argSourceCarveSeed || !confident {
		t.Errorf("opaque-import-grammar-falls-through case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_grammar_multi", "CarveName", survey, importGrammar, carveSeed)
	if arg != "carve_name" || src != argSourceCarveSeed || !confident {
		t.Errorf("multi-argument-import-grammar-falls-through case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_carve_only", "CarveName", survey, importGrammar, carveSeed)
	if arg != "carve_name" || src != argSourceCarveSeed || !confident {
		t.Errorf("carve seed case: got (%q, %q, %v)", arg, src, confident)
	}

	arg, src, confident = resolveArgName("aws_neither", "SomeProperty", survey, importGrammar, carveSeed)
	if arg != "some_property" || src != argSourceGuessed || confident {
		t.Errorf("guess case: got (%q, %q, %v)", arg, src, confident)
	}
}
