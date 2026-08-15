// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// One attribute of a parent, rather than the parent's whole import identity.
//
// [Resolution.attrParts] is what answers a reference like
// aws_cloudwatch_event_target.rule = aws_cloudwatch_event_rule.x.name, from
// the split the parent's own resolution already carries. The tests here pin
// the two things that split has to be: the same value the parent's identity
// uses, and never the joined string when the attribute is one piece of it.

// TestParentAttributeResolvesFromTheParentsOwnSplit is the ordinary
// EventBridge idiom, and the shape eight published deployments in the corpus
// are blocked on. The rule's identity is EVENT_BUS_NAME/NAME; the target reads
// the NAME half.
func TestParentAttributeResolvesFromTheParentsOwnSplit(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "parent-attr"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	rule := resolutionAt(t, result, `aws_cloudwatch_event_rule.nightly`)
	if rule.ImportID != "ops-bus/nightly-sweep" {
		t.Fatalf("the rule resolved to %q, want ops-bus/nightly-sweep", rule.ImportID)
	}

	// The provider documents an event target's import as
	// EVENT_BUS_NAME/RULE/TARGET_ID, so the middle segment has to be the
	// rule's name and not the rule's whole import ID.
	target := resolutionAt(t, result, `aws_cloudwatch_event_target.nightly`)
	if target.Class != ClassConcrete {
		t.Fatalf("the target resolved %s; every value it needs is in configuration", target.Class)
	}
	if want := "ops-bus/nightly-sweep/sweep"; target.ImportID != want {
		t.Errorf("the target resolved to %q, want %q", target.ImportID, want)
	}
	if got := target.IdentityValues["rule"]; got != "nightly-sweep" {
		t.Errorf("the target's rule attribute is %q, want the rule's name nightly-sweep", got)
	}
}

// TestParentAttributeIsNotTheJoinedImportID is the regression the change
// exists for. aws_volume_attachment's entry lists device_name, instance_id and
// volume_id in IdentityAttrs while its components say each supplies one third
// of DEVICE_NAME:VOLUME_ID:INSTANCE_ID, so a child reading one of them used to
// receive all three joined - a marker naming a cloud object that does not
// exist, with no diagnostic anywhere.
func TestParentAttributeIsNotTheJoinedImportID(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "parent-attr-composite"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	attachment := resolutionAt(t, result, `aws_volume_attachment.data`)
	joined := "/dev/sdh:vol-0123456789abcdef0:i-0123456789abcdef0"
	if attachment.ImportID != joined {
		t.Fatalf("the attachment resolved to %q, want %q", attachment.ImportID, joined)
	}

	child := resolutionAt(t, result, `aws_s3_bucket_policy.data`)
	if child.ImportID == joined {
		t.Fatalf("reading .volume_id yielded the attachment's whole import ID %q", child.ImportID)
	}
	if want := "vol-0123456789abcdef0"; child.ImportID != want {
		t.Errorf("reading .volume_id yielded %q, want %q", child.ImportID, want)
	}
}

// TestParentAttributeAnswersOnlyWhatTheParentCarries is the other half: the
// change must not turn a refusal into a guess. A server-assigned parent's
// resolution carries no per-attribute split at all, so nothing here can answer
// for it and the refusal stands.
func TestParentAttributeAnswersOnlyWhatTheParentCarries(t *testing.T) {
	tests := []struct {
		name string
		res  Resolution
		attr string
		want string // "" means no answer
	}{
		{
			name: "concrete, attribute in the split",
			res: Resolution{
				Class:          ClassConcrete,
				ImportID:       "ops-bus/nightly",
				IdentityValues: map[string]string{"event_bus_name": "ops-bus", "name": "nightly"},
			},
			attr: "name",
			want: "nightly",
		},
		{
			name: "concrete, attribute not in the split",
			res: Resolution{
				Class:          ClassConcrete,
				ImportID:       "ops-bus/nightly",
				IdentityValues: map[string]string{"event_bus_name": "ops-bus", "name": "nightly"},
			},
			attr: "arn",
		},
		{
			name: "concrete with no split at all",
			res:  Resolution{Class: ClassConcrete, ImportID: "subnet-1/rtb-1"},
			attr: "subnet_id",
		},
		{
			// A [TypeIdentity.IdentityObjectOnly] parent: the split is the
			// whole answer and the import ID is deliberately empty, so the
			// empty string is what the old path handed a child.
			name: "concrete, import by identity object only",
			res: Resolution{
				Class:          ClassConcrete,
				ImportID:       "",
				IdentityValues: map[string]string{"cluster": "prod", "name": "svc"},
			},
			attr: "name",
			want: "svc",
		},
		{
			name: "needs discovery",
			res:  Resolution{Class: ClassNeedsDiscovery, Reason: "EC2 assigns it"},
			attr: "id",
		},
		{
			name: "record backed",
			res:  Resolution{Class: ClassRecordBacked},
			attr: "id",
		},
		{
			name: "parent derived, attribute in the split",
			res: Resolution{
				Class: ClassParentDerived,
				Formula: &Formula{
					Attrs: []AttrFormula{{Name: "device_name", Parts: []Part{{Literal: "/dev/sdh"}}}},
				},
			},
			attr: "device_name",
			want: "/dev/sdh",
		},
		{
			name: "parent derived with no split",
			res:  Resolution{Class: ClassParentDerived, Formula: &Formula{}},
			attr: "subnet_id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.res.attrParts(tc.attr)
			if tc.want == "" {
				if ok {
					t.Fatalf("answered %v for an attribute the resolution does not carry", got)
				}
				return
			}
			if !ok {
				t.Fatalf("no answer for %q, which the resolution carries", tc.attr)
			}
			var b strings.Builder
			for _, p := range got {
				b.WriteString(p.Literal)
			}
			if b.String() != tc.want {
				t.Errorf("answered %q, want %q", b.String(), tc.want)
			}
		})
	}
}

// TestIdentityAttrsAndComponentsAgreeOrTheComponentsWin walks the whole table
// for the disagreement the change makes inert, so the count is a measurement
// rather than a claim. An entry that lists an attribute in IdentityAttrs while
// its own components say that attribute supplies only part of the import ID is
// two contradictory statements about the same name; parentPart now reads the
// components, and this records which rows the reading changes.
//
// It is a floor, not a freeze: a new row that repeats the contradiction is
// caught, and a row repaired in the generated table only lowers the number.
func TestIdentityAttrsAndComponentsAgreeOrTheComponentsWin(t *testing.T) {
	const known = 4

	var found []string
	for name, entry := range DefaultTable {
		if entry.ServerAssigned || entry.RecordBacked {
			continue
		}
		// How many value-bearing components each declared attribute covers,
		// and how many there are in total.
		valued := 0
		cover := map[string]int{}
		for _, c := range entry.Components {
			if len(c.Attrs) == 0 && c.Cloud == CloudNone {
				continue
			}
			valued++
			switch c.IdentityAttr {
			case "":
			case SameNameIdentity:
				for _, a := range c.Attrs {
					cover[a]++
				}
			default:
				cover[c.IdentityAttr]++
			}
		}
		for _, listed := range entry.IdentityAttrs {
			if n, declared := cover[listed]; declared && n < valued {
				found = append(found, name+"."+listed)
			}
		}
	}

	if len(found) > known {
		t.Errorf("%d rows list an attribute in IdentityAttrs that their own components say is only part of the import ID, up from %d:\n  %s",
			len(found), known, strings.Join(found, "\n  "))
	}
}
