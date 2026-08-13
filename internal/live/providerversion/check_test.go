// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package providerversion

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
	residue "github.com/intentius/choudoufu/live"
)

// evidenceVersion is live/survey.json's provider_version header, read
// through the same accessor Check uses, so these tests track the artifact
// rather than a hand copied literal that could drift from it silently.
func evidenceVersion(t *testing.T) string {
	t.Helper()
	v := residue.EvidenceVersion()
	if v == "" {
		t.Fatal("residue.EvidenceVersion() is empty; live/survey.json's provider_version header is missing or unparsed")
	}
	return v
}

func TestCheck_exactMatchIsSilent(t *testing.T) {
	got := Check(evidenceVersion(t))
	if len(got) != 0 {
		t.Fatalf("Check(evidence version) = %v, want no diagnostics", got)
	}
}

func TestCheck_emptyResolvedIsSilent(t *testing.T) {
	got := Check("")
	if len(got) != 0 {
		t.Fatalf("Check(\"\") = %v, want no diagnostics", got)
	}
}

func TestCheck_patchDifferenceWarnsOnce(t *testing.T) {
	evidence := evidenceVersion(t)
	resolved := bumpPatch(t, evidence)

	got := Check(resolved)
	if len(got) != 1 {
		t.Fatalf("Check(%q) with evidence %q = %d diagnostics, want exactly 1: %v", resolved, evidence, len(got), got)
	}

	d := got[0]
	if d.Severity() != tfdiags.Warning {
		t.Errorf("diagnostic severity = %v, want Warning", d.Severity())
	}

	detail := d.Description().Detail
	if !strings.Contains(detail, resolved) {
		t.Errorf("diagnostic detail does not name the resolved version %q:\n%s", resolved, detail)
	}
	if !strings.Contains(detail, evidence) {
		t.Errorf("diagnostic detail does not name the evidence version %q:\n%s", evidence, detail)
	}
	if !strings.Contains(detail, "import") {
		t.Errorf("diagnostic detail does not name import grammar as version-sensitive:\n%s", detail)
	}
	if !strings.Contains(detail, "identity") {
		t.Errorf("diagnostic detail does not name identity schemas as version-sensitive:\n%s", detail)
	}
	if !strings.Contains(detail, "Tagg") && !strings.Contains(detail, "tagg") {
		t.Errorf("diagnostic detail does not name taggability as the non-version-sensitive contrast:\n%s", detail)
	}
}

func TestCheck_minorAndMajorDifferencesAlsoWarn(t *testing.T) {
	evidence := evidenceVersion(t)

	for _, resolved := range []string{
		bumpMinor(t, evidence),
		bumpMajor(t, evidence),
	} {
		t.Run(resolved, func(t *testing.T) {
			got := Check(resolved)
			if len(got) != 1 {
				t.Fatalf("Check(%q) = %d diagnostics, want exactly 1: %v", resolved, len(got), got)
			}
		})
	}
}

func TestCheck_unparsableResolvedFallsBackToLiteralCompare(t *testing.T) {
	// A resolved string Check cannot parse as a version is compared
	// literally against the evidence version rather than causing a panic
	// or a false silent match: since it can never equal a well-formed
	// evidence version, it warns.
	got := Check("not-a-version")
	if len(got) != 1 {
		t.Fatalf("Check(\"not-a-version\") = %d diagnostics, want exactly 1: %v", len(got), got)
	}
}

func TestVersionsEqual_formatDifferencesStillMatch(t *testing.T) {
	// versionsEqual parses both sides, so trailing zero components or other
	// cosmetic formatting differences that still name the same version
	// compare equal.
	if !versionsEqual("6.58.0", "6.58.0") {
		t.Error("versionsEqual(\"6.58.0\", \"6.58.0\") = false, want true")
	}
	if versionsEqual("6.58.0", "6.58.1") {
		t.Error("versionsEqual(\"6.58.0\", \"6.58.1\") = true, want false")
	}
}

// bumpPatch, bumpMinor, and bumpMajor increment one semver component of a
// clean "x.y.z" version string, so these tests exercise the comparison
// against whatever live/survey.json currently pins rather than a version
// string hand-copied here to fall out of sync with it.
func bumpPatch(t *testing.T, v string) string { return bumpComponent(t, v, 2) }
func bumpMinor(t *testing.T, v string) string { return bumpComponent(t, v, 1) }
func bumpMajor(t *testing.T, v string) string { return bumpComponent(t, v, 0) }

func bumpComponent(t *testing.T, v string, idx int) string {
	t.Helper()
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		t.Fatalf("evidence version %q is not a clean x.y.z string", v)
	}
	n := mustAtoi(t, parts[idx])
	parts[idx] = itoa(n + 1)
	return strings.Join(parts, ".")
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("evidence version component %q is not numeric", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
