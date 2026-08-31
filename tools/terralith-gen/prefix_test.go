// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// zoneNamePattern matches the name attribute of a rendered aws_route53_zone
// block. It is anchored on the resource header rather than on `name = ` alone
// because dns.tf's OTHER name attribute is the record's
// `"${each.key}.${aws_route53_zone.main.name}"`, which is an interpolation and
// carries no case of its own.
var zoneNamePattern = regexp.MustCompile(`(?s)resource\s+"aws_route53_zone"\s+"[^"]+"\s*\{.*?\bname\s*=\s*"([^"]*)"`)

// renderedZoneNames returns every hosted-zone name literal in the rendered
// dns.tf given. Callers assert this is non-empty before drawing any conclusion
// from zoneNamesNotLowercase - a scanner that matched nothing would otherwise
// report a clean estate because it is blind.
func renderedZoneNames(dnsTF string) []string {
	var names []string
	for _, m := range zoneNamePattern.FindAllStringSubmatch(dnsTF, -1) {
		names = append(names, m[1])
	}
	return names
}

// zoneNamesNotLowercase returns the rendered hosted-zone names that are not
// already lowercase - the rendered-output form of issue #635's defect.
func zoneNamesNotLowercase(dnsTF string) []string {
	var bad []string
	for _, n := range renderedZoneNames(dnsTF) {
		if n != strings.ToLower(n) {
			bad = append(bad, n)
		}
	}
	return bad
}

func renderDNSTF(t *testing.T, scale int, prefix string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "terralith")
	if err := buildEstate(scale, prefix).write(out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "dns.tf")) //nolint:gosec // fixed test-generated path
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestGeneratedZoneNameIsLowercase is the rendered-output half of issue #635:
// whatever the flag guard does, the hosted-zone name this generator actually
// writes must be a lowercase DNS label, because that is the thing the AWS
// provider compares against the record name it has already lowercased.
func TestGeneratedZoneNameIsLowercase(t *testing.T) {
	for _, scale := range []int{1, 4} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			dnsTF := renderDNSTF(t, scale, "tl")
			if names := renderedZoneNames(dnsTF); len(names) == 0 {
				t.Fatalf("no aws_route53_zone name literal matched in the rendered dns.tf - the scan would pass vacuously; rendered dns.tf:\n%s", dnsTF)
			}
			for _, n := range zoneNamesNotLowercase(dnsTF) {
				t.Errorf("hosted-zone name %q is not lowercase; the provider lowercases the record name but not this, so every record is created under one name and read back under another (issue #635)", n)
			}
		})
	}
}

// TestZoneNameScanHasTeeth drives the scan with the generator's OWN rendering
// at a mixed-case prefix - what shipped before this fix, reached by calling
// buildEstate directly so the flag guard cannot mask it - and requires a
// finding. Without this control TestGeneratedZoneNameIsLowercase is a check
// that has never been made to fail.
func TestZoneNameScanHasTeeth(t *testing.T) {
	dnsTF := renderDNSTF(t, 1, "rtA")
	bad := zoneNamesNotLowercase(dnsTF)
	if len(bad) != 1 {
		t.Fatalf("the pre-#635 rendering (-prefix rtA) produced %d findings, want exactly 1 (the hosted zone): %v\nrendered dns.tf:\n%s", len(bad), bad, dnsTF)
	}
	if bad[0] != "rtA.terralith.test" {
		t.Errorf("finding names %q, want %q", bad[0], "rtA.terralith.test")
	}
}

// TestMixedCasePrefixIsRefused is issue #635's acceptance bullet: a mixed-case
// prefix is refused before anything is written, with a message that says why.
// It asserts on run() rather than validatePrefix() alone so the refusal is
// proved at the point a caller reaches it, and it checks the output directory
// was never created - a partial estate on disk would be the failure mode this
// guard exists to remove.
func TestMixedCasePrefixIsRefused(t *testing.T) {
	for _, prefix := range []string{"rtA", "RT", "Tl", "ticket-ABC-42", "tlX"} {
		t.Run(prefix, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			err := run(1, out, prefix, "")
			if err == nil {
				t.Fatalf("run with -prefix %q returned nil, want a refusal", prefix)
			}
			msg := err.Error()
			for _, want := range []string{prefix, strings.ToLower(prefix), "lowercase", "aws_route53_record", "#635"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal message does not mention %q, so it does not name the reason:\n%s", want, msg)
				}
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Errorf("run refused but %s exists (stat err %v) - the refusal must happen before anything is written", out, statErr)
			}
		})
	}
}

// TestLowercasePrefixIsAccepted is the other side of the guard: it must refuse
// nothing that works today. Every prefix here is one this repository actually
// passes to the generator - the "tl" default (main.go), "tls<pid>"
// (live/e2e/terralith-scale/run.sh), "sca"/"scb"/"scc"
// (internal/live/statefulcost), and "lc<epoch><pid>"
// (live/live-cert/terralith-scale.sh) - plus a hyphenated ticket-shaped one,
// since a hyphen is a legal DNS label character and the refusal is about case
// only.
func TestLowercasePrefixIsAccepted(t *testing.T) {
	for _, prefix := range []string{"tl", "tls12345", "sca", "scb", "scc", "lc175678912345", "ticket-4271"} {
		if err := validatePrefix(prefix); err != nil {
			t.Errorf("validatePrefix(%q) = %v, want nil - this prefix is one the repository already passes", prefix, err)
		}
	}
}
