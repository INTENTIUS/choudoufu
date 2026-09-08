// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestOrphanWarningOnlyForTypesTheTableDoesNotList is GitHub issue #980.
//
// [resolver.lookupType] writes a non-nil memo into r.synth on two different
// paths, and only one of them is what issue #107's warning describes:
//
//   - the type has NO row in [DefaultTable] and resolves only because the
//     provider's identity schema settles it. internal/live/discovery draws
//     its sweep universe from [AdmittedTypes], which is the table's keys, so
//     no run will ever propose removing the live resource. #107's condition,
//     and the warning is true.
//   - the type HAS a row and the schema reproduces it, so the synthesized
//     entry is used in the row's place (ruling 2 of the foundation-order
//     ruling, #387, [preferSynthesized]). The type is still in the table,
//     the sweep still lists it, and the warning is false.
//
// The fixture carries one of each, and the assertion is by rendered message
// rather than by count alone: the exact detail the unsweepable type must
// still produce, unchanged, and nothing at all naming the reproduced one.
func TestOrphanWarningOnlyForTypesTheTableDoesNotList(t *testing.T) {
	schemas := orphanWarningScopeSchemas()

	// The fixture only exercises #980 if aws_iam_role really does take the
	// schema-first path: a schema that did not reproduce the row would
	// leave a nil memo, and this test would pass without testing anything.
	row, hasRow := LookupType("aws_iam_role")
	if !hasRow {
		t.Fatal("aws_iam_role has no row in DefaultTable; the fixture no longer exercises the schema-first path")
	}
	synthesized, ok := SynthesizeTypeIdentity("aws_iam_role", schemas, nil)
	if !ok {
		t.Fatal("SynthesizeTypeIdentity refused aws_iam_role against this test's own schema; the fixture no longer exercises the schema-first path")
	}
	if _, ok := preferSynthesized(row, synthesized); !ok {
		t.Fatal("the fake schema no longer reproduces aws_iam_role's row, so lookupType would keep the row and never memoize; the fixture no longer exercises the schema-first path")
	}
	if _, hasRow := LookupType("aws_thing"); hasRow {
		t.Fatal("aws_thing now has a row in DefaultTable; the fixture no longer exercises #107's condition")
	}

	cfg := loadConfig(t, "testdata/orphan-warning-scope", nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	var warned []string
	for _, d := range diags {
		if d.Severity() != tfdiags.Warning {
			continue
		}
		if d.Description().Summary == SummaryNoOrphanRecovery {
			warned = append(warned, d.Description().Detail)
		}
	}

	// The warning's own text, quoted whole: this change narrows which types
	// it fires for and must not touch what it says.
	wantThing := `aws_thing is admitted by the provider's own identity schema rather than by this fork's admission table, so it plans and applies normally - but the estate-wide sweep draws its type universe from that table and will not list it. Deleting the last aws_thing block from this configuration leaves the live resource in the account with no run proposing to remove it, and no warning at that point either. Remove it by hand when you remove the block. See live/LIMITATIONS.md, "Resource type has no orphan recovery".`

	for _, detail := range warned {
		if strings.Contains(detail, "aws_iam_role") {
			t.Errorf("aws_iam_role has a row in the admission table and the sweep does list it, but the run warns:\n  %s", detail)
		}
	}
	if len(warned) != 1 {
		t.Fatalf("got %d no-orphan-recovery warnings, want exactly 1 (aws_thing):\n%s", len(warned), renderDiags(diags))
	}
	if warned[0] != wantThing {
		t.Errorf("the warning for the genuinely unsweepable type changed:\n got: %s\nwant: %s", warned[0], wantThing)
	}
}

// orphanWarningScopeSchemas is [fallbackSchemas]' own cohort - which is
// where aws_thing, a type no table row covers, comes from - plus one schema
// for aws_iam_role read straight off DefaultTable's row for it: one Required
// argument, name, which is also the one required identity attribute, the
// same fixture shape TestSchemaPrecedenceMatchesRowByValue uses and for the
// same reason.
//
// The whole cohort is handed in rather than the two types alone because
// synthesis decides which identity attributes are cloud CONTEXT by looking
// across the types the provider serves (notContext), and aws_thing's own
// account_id/region are only recognised as context in company.
func orphanWarningScopeSchemas() map[string]providers.Schema {
	schemas := fallbackSchemas()
	for name, schema := range fakeProviderSchemas(map[string]fakeType{
		"aws_iam_role": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req"},
		},
	}) {
		schemas[name] = schema
	}
	return schemas
}
