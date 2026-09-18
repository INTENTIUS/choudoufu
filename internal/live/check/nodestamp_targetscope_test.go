// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// stampTargetScopeFixture holds two instances of the untaggable,
// server-assigned shape [stampUnmarkedApplyRecordBackedFixture] holds one
// of. It lives under internal/command/testdata for the reason that
// fixture's own doc comment gives: [TestIdentityGolden] sweeps every
// directory under internal/live and live, and a repro fixture must not add
// a pinned row there.
const stampTargetScopeFixture = "../../command/testdata/live-plan-target-scope-1203"

// resolveStampTargetScopeFixture loads and resolves [stampTargetScopeFixture]
// with no scope, which is what the resolution pass hands the online caller:
// [identity.Scope]'s own doc comment says an out-of-scope block KEEPS its
// resolution, because the resolution list is also the estate sweep's
// declared set. So both instances are present in NeedsDiscovery here even
// on a run that targets one of them, which is exactly why the narrowing has
// to happen in the pass that acts.
func resolveStampTargetScopeFixture(t *testing.T) (*configs.Config, *identity.Result) {
	t.Helper()

	schemas := stampUnmarkedApplyRecordBackedSchemas()
	report := Dir(t.Context(), stampTargetScopeFixture, Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	result, diags := identity.ResolveWith(t.Context(), report.Load.Config, identity.Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolving the fixture: %s", diags.Err())
	}
	if got := len(result.NeedsDiscovery()); got != 2 {
		t.Fatalf("want two needs-discovery instances, got %d: %v", got, result.NeedsDiscovery())
	}
	return report.Load.Config, result
}

// countSummary is how many diagnostics carry exactly this summary.
// [hasSummary] next door answers the boolean; this audit needs the count,
// because "the check still fires, but only for the block the run holds" is
// a statement about how MANY fired.
func countSummary(diags tfdiags.Diagnostics, summary string) int {
	n := 0
	for _, d := range diags {
		if d.Description().Summary == summary {
			n++
		}
	}
	return n
}

// diagsMention reports whether any diagnostic's detail names addr.
func diagsMention(diags tfdiags.Diagnostics, addr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Description().Detail, addr) {
			return true
		}
	}
	return false
}

// rendered is what a failure message prints, so it says what actually fired
// rather than only that the count was wrong.
func rendered(diags tfdiags.Diagnostics) string {
	if err := diags.Err(); err != nil {
		return "got: " + err.Error()
	}
	return "got: (no diagnostics)"
}

// TestNodeStampUnmarkedApplyHonoursTheTargetScope is GitHub issue #1203's
// own instance of #1176's shape: a live-path pass that reasons over the
// whole configuration while the run has been narrowed to a target set.
//
// [NodeStampUnmarkedApply] is the plan-time half of #950. It walks every
// needs-discovery block in the configuration and raises an ERROR for each
// one whose type has nowhere to write an ownership marker, which stops the
// run. On a -target run that refusal names a resource the plan graph does
// not hold and this run will therefore never create - so the apply it
// warns about cannot happen, and the operator cannot act on the diagnostic
// without abandoning the narrowing they asked for.
//
// The two halves matter equally, and the second is the one that makes this
// a fix rather than a hole: narrowing a run must not disable a check that
// protects something the run DOES touch. A scope that keeps one of the two
// blocks must still refuse that one.
func TestNodeStampUnmarkedApplyHonoursTheTargetScope(t *testing.T) {
	cfg, result := resolveStampTargetScopeFixture(t)
	schemas := stampUnmarkedApplyRecordBackedSchemas()

	only := func(name string) identity.Scope {
		return func(a addrs.ConfigResource) bool { return a.Resource.Name == name }
	}

	t.Run("no scope: both refuse, exactly as before", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, schemas, "target-scope-1203", nil, nil)
		if n := countSummary(diags, stamp.SummaryUnmarkedApply); n != 2 {
			t.Fatalf("want two %q on an untargeted run, got %d. %s",
				stamp.SummaryUnmarkedApply, n, rendered(diags))
		}
	})

	t.Run("scope keeps one: that one still refuses", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, schemas, "target-scope-1203", nil, only("targeted"))
		if n := countSummary(diags, stamp.SummaryUnmarkedApply); n != 1 {
			t.Fatalf("want exactly one %q, got %d. Narrowing a run must not disable the check for a block the run still holds. %s",
				stamp.SummaryUnmarkedApply, n, rendered(diags))
		}
		if !diagsMention(diags, "aws_vpc.targeted") {
			t.Errorf("the surviving refusal should name aws_vpc.targeted. %s", rendered(diags))
		}
		if diagsMention(diags, "aws_vpc.excluded") {
			t.Errorf("aws_vpc.excluded is outside the plan graph and must not be refused. %s", rendered(diags))
		}
	})

	t.Run("scope keeps neither: nothing refuses", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, schemas, "target-scope-1203", nil, only("neither"))
		if diags.HasErrors() {
			t.Fatalf("a run whose plan graph holds neither block must not be refused for either. %s",
				rendered(diags))
		}
	})
}
