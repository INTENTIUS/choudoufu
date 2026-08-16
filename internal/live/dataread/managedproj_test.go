// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// analyzeProjection is [analyzeFixture] with issue #193's managed-argument
// projection switched on.
func analyzeProjection(t *testing.T, name string) *Analysis {
	t.Helper()
	cfg := loadConfig(t, filepath.Join("testdata", name), nil)
	return Analyze(context.Background(), cfg, Options{ProjectManagedArguments: true})
}

// TestManagedProjectionAnswersASetArgument: with the option on, a data
// source whose argument reads a managed resource attribute the block itself
// sets classifies eligible - no cloud read, no state, the value is in the
// configuration.
func TestManagedProjectionAnswersASetArgument(t *testing.T) {
	off := analyzeFixture(t, "managed-projection")
	src, ok := off.SourceFor(addrs.RootModule, dataAddr("aws_subnet", "of_instance"))
	if !ok {
		t.Fatalf("data.aws_subnet.of_instance was not classified with the option off")
	}
	if src.Eligible {
		t.Fatalf("the option is off; the managed reference must still refuse")
	}

	on := analyzeProjection(t, "managed-projection")
	src, ok = on.SourceFor(addrs.RootModule, dataAddr("aws_subnet", "of_instance"))
	if !ok {
		t.Fatalf("data.aws_subnet.of_instance was not classified with the option on")
	}
	if !src.Eligible {
		t.Fatalf("aws_instance.web.subnet_id is set in the configuration, so the read is eligible; refused: %s", src.ReasonDetail)
	}
}

// TestManagedProjectionRefusesAProviderAssignedAttribute: the other
// direction, and the one that matters. private_dns is not in the
// configuration at all, so the projection must not answer it - and it must
// refuse in the same words it refuses in with the option off, not with a
// raw HCL "this object does not have an attribute named" error leaking out
// of the evaluator.
func TestManagedProjectionRefusesAProviderAssignedAttribute(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    *Analysis
	}{
		{"option off", analyzeFixture(t, "managed-projection")},
		{"option on", analyzeProjection(t, "managed-projection")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, ok := tc.a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "of_instance"))
			if !ok {
				t.Fatalf("data.aws_route53_zone.of_instance was not classified at all")
			}
			if src.Eligible {
				t.Fatalf("aws_instance.web.private_dns is assigned by the provider; it must not be projected")
			}
			if src.ReasonSummary != SummaryNotReadable {
				t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
			}
			for _, part := range []string{"managed resource", "cannot be read before the plan"} {
				if !strings.Contains(src.ReasonDetail, part) {
					t.Errorf("the wording lacks %q: %s", part, src.ReasonDetail)
				}
			}
			if strings.Contains(src.ReasonDetail, "does not have an attribute named") {
				t.Errorf("an HCL attribute error leaked into the refusal: %s", src.ReasonDetail)
			}
		})
	}
}
