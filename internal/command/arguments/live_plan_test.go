// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParseLivePlan_estate covers the one option live-plan adds to the plan
// flag set: the forms it accepts, and that its absence is not an error.
func TestParseLivePlan_estate(t *testing.T) {
	testCases := map[string]struct {
		args      []string
		want      string
		wantError bool
	}{
		"absent":       {nil, "", false},
		"equals form":  {[]string{"-estate=prod"}, "prod", false},
		"space form":   {[]string{"-estate", "prod"}, "prod", false},
		"double dash":  {[]string{"--estate=prod"}, "prod", false},
		"beside other": {[]string{"-target=aws_vpc.main", "-estate=prod", "-input=false"}, "prod", false},
		"empty value":  {[]string{"-estate="}, "", false},
		"last wins":    {[]string{"-estate=a", "-estate=b"}, "b", false},

		// Not our flag, and not any flag: the plan flag set rejects it rather
		// than the pre-pass this replaced leaving it for someone else.
		"lookalike": {[]string{"-estate-ish=1"}, "", true},

		// No value at the end of the arguments. The flag package reports
		// "flag needs an argument", which is the same answer every other
		// valued option gives.
		"no value": {[]string{"-estate"}, "", true},

		// The behavior change this parser makes on purpose. The old hand
		// rolled pre-pass trimmed every leading dash before comparing, so
		// any number of them parsed as -estate; a flag set knows one dash
		// from two and nothing else.
		"many dashes": {[]string{"-----estate=prod"}, "", true},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got, closer, diags := ParseLivePlan(tc.args)
			defer closer()
			if got.Plan == nil {
				t.Fatal("the embedded Plan is nil")
			}
			if diags.HasErrors() != tc.wantError {
				t.Fatalf("diagnostics %v, want error == %v", diags.Err(), tc.wantError)
			}
			if tc.wantError {
				return
			}
			if got.Estate != tc.want {
				t.Errorf("Estate %q, want %q", got.Estate, tc.want)
			}
		})
	}
}

// TestParseLivePlan_endOfFlags: everything after "--" is an operand, and the
// plan command takes no operands. The pre-pass this replaced scanned the whole
// argument list and would have pulled an -estate out from behind the
// end-of-flags marker.
func TestParseLivePlan_endOfFlags(t *testing.T) {
	got, closer, diags := ParseLivePlan([]string{"--", "-estate=prod"})
	defer closer()
	if !diags.HasErrors() {
		t.Fatalf("no error for an operand after --; Estate parsed as %q", got.Estate)
	}
	if got.Estate != "" {
		t.Errorf("Estate %q, want empty: an argument after -- is not a flag", got.Estate)
	}
}

// TestParseLivePlan_planFlags: the stock plan options still parse the way
// they do for the plan command itself, since live-plan shares its flag set.
func TestParseLivePlan_planFlags(t *testing.T) {
	got, closer, diags := ParseLivePlan([]string{
		"-estate=prod", "-target=aws_vpc.main", "-detailed-exitcode", "-parallelism=5", "-input=false",
	})
	defer closer()
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags.Err())
	}
	if got.Estate != "prod" {
		t.Errorf("Estate %q, want prod", got.Estate)
	}
	if !got.DetailedExitCode {
		t.Error("-detailed-exitcode did not reach the embedded Plan")
	}
	if got.Operation.Parallelism != 5 {
		t.Errorf("parallelism %d, want 5", got.Operation.Parallelism)
	}
	if got.ViewOptions.InputEnabled {
		t.Error("-input=false did not reach the embedded Plan")
	}
	wantTargets := []string{"aws_vpc.main"}
	gotTargets := make([]string, 0, len(got.Operation.Targets))
	for _, target := range got.Operation.Targets {
		gotTargets = append(gotTargets, target.String())
	}
	if diff := cmp.Diff(wantTargets, gotTargets); diff != "" {
		t.Errorf("wrong targets\n%s", diff)
	}
}

// TestParsePlan_noEstate: adding -estate to live-plan must not add it to the
// plan command, whose flag set is the one this shares.
func TestParsePlan_noEstate(t *testing.T) {
	_, closer, diags := ParsePlan([]string{"-estate=prod"})
	defer closer()
	if !diags.HasErrors() {
		t.Fatal("\"choudoufu plan\" accepted -estate; it is live-plan's option only")
	}
}
