// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// testdataSchemasDir holds a small, hand-picked subset of real
// CloudFormation Registry schemas (issue #42), extracted verbatim from the
// real bundle so their bytes are exactly what extractSchemas would have
// produced from the zip itself: AWS::S3::Bucket and AWS::IAM::Role (a large
// and a mid-size well-known type), AWS::EC2::Route (a compound
// primaryIdentifier and handlers.list.handlerSchema.required - the
// list-with-required-input shape), AWS::EC2::VPC and AWS::Logs::LogGroup
// (both taggable, list-free), AWS::Cases::Field (additionalIdentifiers),
// AWS::WAFv2::RegexPatternSet (a 3-way primaryIdentifier), and
// AWS::Pinpoint::App (no handlers section at all).
const testdataSchemasDir = "testdata/schemas"

// loadTestdataSchemas reads every committed testdata schema into the same
// typeName -> raw JSON bytes shape extractSchemas produces from a zip.
func loadTestdataSchemas(t *testing.T) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(testdataSchemasDir)
	if err != nil {
		t.Fatalf("reading %s: %v", testdataSchemasDir, err)
	}
	schemas := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(testdataSchemasDir, e.Name())) //nolint:gosec // fixed testdata paths inside the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		var probe struct {
			TypeName string `json:"typeName"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			t.Fatalf("%s does not parse as JSON: %v", e.Name(), err)
		}
		if probe.TypeName == "" {
			t.Fatalf("%s carries no typeName", e.Name())
		}
		schemas[probe.TypeName] = data
	}
	if len(schemas) == 0 {
		t.Fatalf("%s has no testdata schemas", testdataSchemasDir)
	}
	return schemas
}
