// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestOverlongAddressRule covers the three ways an instance address is
// measured against the 256-character marker cap (a plain resource, a
// for_each key, a count index), plus the two silences the fixture pins: an
// address inside the cap, and a long-labeled resource whose for_each is not
// statically evaluable, which the rule skips rather than guesses at.
func TestOverlongAddressRule(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/overlong-address")
	issues := CheckContext(t.Context(), cfg)

	assertIssues(t, issues, []wantIssue{
		{
			rule:      RuleOverlongAddress,
			construct: "aws_s3_bucket." + strings.Repeat("x", 250),
			file:      "testdata/overlong-address/main.tf",
			line:      13,
		},
		{
			rule:      RuleOverlongAddress,
			construct: `aws_subnet.wide["` + strings.Repeat("k", 250) + `"]`,
			file:      "testdata/overlong-address/main.tf",
			line:      21,
		},
		{
			rule:      RuleOverlongAddress,
			construct: "aws_vpc." + strings.Repeat("y", 250) + "[1]",
			file:      "testdata/overlong-address/main.tf",
			line:      29,
		},
	})
}

// TestOverlongAddressBudgetBreakdown pins the arithmetic behind the
// refusal's "budget math" sentence directly, independent of any fixture: a
// root-module instance attributes the whole escaped length to the resource
// itself, a nested instance splits it between the module path and the
// resource, and a module path alone past the cap is called out as such
// rather than reported with a negative remainder.
func TestOverlongAddressBudgetBreakdown(t *testing.T) {
	resource := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "aws_vpc",
		Name: strings.Repeat("x", 50),
	}
	inst := resource.Instance(addrs.NoKey)

	t.Run("root module", func(t *testing.T) {
		got := budgetBreakdown(inst, addrs.RootModuleInstance)
		want := "This instance declares no module path, so the resource type, label and instance key alone account for all " +
			strconv.Itoa(utf8RuneCount(inst.String())) + " characters."
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("nested module splits the budget", func(t *testing.T) {
		modInst := addrs.ModuleInstance{
			{Name: strings.Repeat("a", 40)},
			{Name: strings.Repeat("b", 40)},
			{Name: strings.Repeat("c", 40)},
		}
		got := budgetBreakdown(inst, modInst)

		resourceOnly := utf8RuneCount(markers.EscapeAddress(inst.String()))
		modulePath := utf8RuneCount(markers.EscapeAddress(inst.Absolute(modInst).String())) - resourceOnly
		remaining := markers.MaxTagValue - modulePath

		for _, want := range []string{
			modInst.String(),
			strconv.Itoa(modulePath) + " characters",
			"leaving " + strconv.Itoa(remaining),
		} {
			if !strings.Contains(got, want) {
				t.Errorf("breakdown %q does not contain %q", got, want)
			}
		}
		// The module path here ("module.aaa....module.ccc...", 3 levels of
		// 40-character names) is short enough that some budget remains -
		// this sub-test would be pinning the wrong branch of
		// budgetBreakdown if it were not, so assert that directly.
		if remaining <= 0 {
			t.Fatalf("test setup produced a module path (%d chars) that already exceeds the budget; adjust the fixture", modulePath)
		}
	})

	t.Run("module path alone exceeds the budget", func(t *testing.T) {
		modInst := addrs.ModuleInstance{
			{Name: strings.Repeat("m", 250)},
		}
		got := budgetBreakdown(inst, modInst)
		if !strings.Contains(got, "already past the 256-character budget") {
			t.Errorf("breakdown %q does not report the module path alone as over budget", got)
		}
	})
}

func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
