// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"
	"testing"
)

func TestParseLiveMv_valid(t *testing.T) {
	testCases := map[string]struct {
		args []string
		want LiveMv
	}{
		"two addresses": {
			[]string{"aws_vpc.old", "aws_vpc.new"},
			LiveMv{RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"estate": {
			[]string{"-estate=prod", "aws_vpc.old", "aws_vpc.new"},
			LiveMv{Estate: "prod", RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"estate space form": {
			[]string{"-estate", "prod", "aws_vpc.old", "aws_vpc.new"},
			LiveMv{Estate: "prod", RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"dry run": {
			[]string{"-dry-run", "aws_vpc.old", "aws_vpc.new"},
			LiveMv{DryRun: true, RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"allow missing config": {
			[]string{"-allow-missing-config", "aws_vpc.old", "aws_vpc.new"},
			LiveMv{AllowMissingConfig: true, RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"json": {
			[]string{"-json", "aws_vpc.old", "aws_vpc.new"},
			LiveMv{JSON: true, RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"from estate, same address": {
			[]string{"-from-estate=monolith", "aws_vpc.main", "aws_vpc.main"},
			LiveMv{FromEstate: "monolith", RawOldAddr: "aws_vpc.main", RawNewAddr: "aws_vpc.main"},
		},
		"input is accepted and ignored": {
			[]string{"-input=false", "aws_vpc.old", "aws_vpc.new"},
			LiveMv{RawOldAddr: "aws_vpc.old", RawNewAddr: "aws_vpc.new"},
		},
		"instance keys": {
			[]string{"aws_vpc.old[\"a\"]", "aws_vpc.new[\"a\"]"},
			LiveMv{RawOldAddr: "aws_vpc.old[\"a\"]", RawNewAddr: "aws_vpc.new[\"a\"]"},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got, diags := ParseLiveMv(tc.args)
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %v", diags.Err())
			}
			if *got != tc.want {
				t.Errorf("got %+v, want %+v", *got, tc.want)
			}
		})
	}
}

func TestParseLiveMv_invalid(t *testing.T) {
	testCases := map[string]struct {
		args        []string
		wantSummary string
	}{
		"no addresses":     {nil, "Two resource addresses are required"},
		"one address":      {[]string{"aws_vpc.old"}, "Two resource addresses are required"},
		"three addresses":  {[]string{"a.b", "c.d", "e.f"}, "Two resource addresses are required"},
		"unknown flag":     {[]string{"-nope", "aws_vpc.old", "aws_vpc.new"}, "Invalid option"},
		"estate no value":  {[]string{"aws_vpc.old", "aws_vpc.new", "-estate"}, "Two resource addresses are required"},
		"flag after addrs": {[]string{"aws_vpc.old", "aws_vpc.new", "-dry-run"}, "Two resource addresses are required"},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			_, diags := ParseLiveMv(tc.args)
			if !diags.HasErrors() {
				t.Fatal("no diagnostics")
			}
			if got := diags.Err().Error(); !strings.Contains(got, tc.wantSummary) {
				t.Errorf("wrong diagnostic %q, want it to name %q", got, tc.wantSummary)
			}
		})
	}
}

// TestParseLiveMv_endOfFlags: past "--" an argument is an operand, even when
// it is shaped like a flag. Two operands is what this command wants, so an
// address that begins with a dash is addressable this way.
func TestParseLiveMv_endOfFlags(t *testing.T) {
	got, diags := ParseLiveMv([]string{"-dry-run", "--", "aws_vpc.old", "aws_vpc.new"})
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags.Err())
	}
	if !got.DryRun {
		t.Error("-dry-run before -- was not parsed as a flag")
	}
	if got.RawOldAddr != "aws_vpc.old" || got.RawNewAddr != "aws_vpc.new" {
		t.Errorf("addresses %q and %q, want aws_vpc.old and aws_vpc.new", got.RawOldAddr, got.RawNewAddr)
	}
}
