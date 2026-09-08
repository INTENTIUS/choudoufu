// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
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
// One existing fixture is entirely the first kind (testdata/schema-fallback,
// the fixture #107's own test uses) and one is entirely the second
// (testdata/schema-precedence, three ratified rows the schemas reproduce),
// so the two halves are asserted separately. Both are asserted by RENDERED
// message rather than by count alone: this change narrows which types the
// warning fires for and must not touch a character of what it says.
//
// Neither fixture is new, deliberately: internal/live/check's identity
// golden sweeps every configuration directory under internal/live, so a
// fixture added here would move a golden this unit has no business moving.
func TestOrphanWarningOnlyForTypesTheTableDoesNotList(t *testing.T) {
	t.Run("in the table and served schema-first: no warning", func(t *testing.T) {
		schemas := schemaFirstRowSchemas()

		// The fixture only exercises #980 if these types really do take the
		// schema-first path: a schema that did not reproduce the row would
		// leave a nil memo and this test would pass without testing
		// anything.
		for _, typeName := range []string{"aws_iam_role", "aws_s3_bucket", "aws_iam_role_policy_attachment"} {
			row, hasRow := LookupType(typeName)
			if !hasRow {
				t.Fatalf("%s has no row in DefaultTable; the fixture no longer exercises the schema-first path", typeName)
			}
			synthesized, ok := SynthesizeTypeIdentity(typeName, schemas, nil)
			if !ok {
				t.Fatalf("SynthesizeTypeIdentity refused %s against this test's own schema; the fixture no longer exercises the schema-first path", typeName)
			}
			if _, ok := preferSynthesized(row, synthesized); !ok {
				t.Fatalf("the fake schema no longer reproduces %s's row, so lookupType would keep the row and never memoize; the fixture no longer exercises the schema-first path", typeName)
			}
		}

		cfg := loadConfig(t, "testdata/schema-precedence", nil)
		_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		assertNoErrors(t, diags)

		if warned := orphanWarnings(diags); len(warned) != 0 {
			t.Errorf("every type in this configuration has a row in the admission table and the sweep does list all three, but the run warns about %d of them:\n%s", len(warned), renderDiags(diags))
		}
	})

	t.Run("not in the table at all: the warning, unchanged", func(t *testing.T) {
		cfg := loadConfig(t, "testdata/schema-fallback", nil)
		_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: fallbackSchemas()})
		assertNoErrors(t, diags)

		for _, typeName := range []string{"aws_thing", "aws_named_thing", "aws_child_thing"} {
			if _, hasRow := LookupType(typeName); hasRow {
				t.Fatalf("%s now has a row in DefaultTable; the fixture no longer exercises #107's condition", typeName)
			}
		}

		warned := orphanWarnings(diags)
		if len(warned) != 3 {
			t.Fatalf("got %d no-orphan-recovery warnings, want one for each of the three unsweepable types:\n%s", len(warned), renderDiags(diags))
		}

		// The warning's own text, quoted whole, for each of the three, in
		// the type-name order warnUnsweepableTypes sorts into.
		want := []string{
			`aws_child_thing is admitted by the provider's own identity schema rather than by this fork's admission table, so it plans and applies normally - but the estate-wide sweep draws its type universe from that table and will not list it. Deleting the last aws_child_thing block from this configuration leaves the live resource in the account with no run proposing to remove it, and no warning at that point either. Remove it by hand when you remove the block. See live/LIMITATIONS.md, "Resource type has no orphan recovery".`,
			`aws_named_thing is admitted by the provider's own identity schema rather than by this fork's admission table, so it plans and applies normally - but the estate-wide sweep draws its type universe from that table and will not list it. Deleting the last aws_named_thing block from this configuration leaves the live resource in the account with no run proposing to remove it, and no warning at that point either. Remove it by hand when you remove the block. See live/LIMITATIONS.md, "Resource type has no orphan recovery".`,
			`aws_thing is admitted by the provider's own identity schema rather than by this fork's admission table, so it plans and applies normally - but the estate-wide sweep draws its type universe from that table and will not list it. Deleting the last aws_thing block from this configuration leaves the live resource in the account with no run proposing to remove it, and no warning at that point either. Remove it by hand when you remove the block. See live/LIMITATIONS.md, "Resource type has no orphan recovery".`,
		}
		for i, detail := range warned {
			if detail != want[i] {
				t.Errorf("the warning for a genuinely unsweepable type changed:\n got: %s\nwant: %s", detail, want[i])
			}
		}
	})
}

// orphanWarnings is every [SummaryNoOrphanRecovery] warning's detail, in the
// order the run raised them - which is type name order, since
// [resolver.warnUnsweepableTypes] sorts.
func orphanWarnings(diags tfdiags.Diagnostics) []string {
	var out []string
	for _, d := range diags {
		if d.Severity() != tfdiags.Warning {
			continue
		}
		if d.Description().Summary == SummaryNoOrphanRecovery {
			out = append(out, d.Description().Detail)
		}
	}
	return out
}

// schemaFirstRowSchemas is one fake schema per type in
// testdata/schema-precedence, each read straight off DefaultTable's own row
// for the type: aws_iam_role and aws_s3_bucket read one Required argument
// (name, bucket) and aws_iam_role_policy_attachment reads two (role,
// policy_arn). It is TestSchemaPrecedenceMatchesRowByValue's own fixture
// schema, kept in step with it by that test's own by-value assertion.
func schemaFirstRowSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_iam_role": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req"},
		},
		"aws_s3_bucket": {
			args:     map[string]string{"bucket": "req"},
			identity: map[string]string{"bucket": "req"},
		},
		"aws_iam_role_policy_attachment": {
			args:     map[string]string{"role": "req", "policy_arn": "req"},
			identity: map[string]string{"role": "req", "policy_arn": "req"},
		},
	})
}
