// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMaintainerAllowReasonDecidesFromExistsLineAndNow is the guard's
// decision function tested directly and purely - no filesystem, no real
// clock - covering the four cases the task calls out: a missing file, an
// expired instant, a future instant, and (separately, since CI is not part
// of the pure decision function) the CI bypass in
// TestCheckMaintainerAllowSkipsInCI below.
func TestMaintainerAllowReasonDecidesFromExistsLineAndNow(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.Local)

	if got := maintainerAllowReason(false, "", now); got == "" {
		t.Fatal("maintainerAllowReason(exists=false) = \"\" (allowed), want a refusal reason - a missing allow file must never read as permission")
	}

	if got := maintainerAllowReason(true, "until 2026-09-11T11:59:59", now); got == "" {
		t.Fatal("maintainerAllowReason with an instant one second in the past = \"\" (allowed), want a refusal reason naming the expiry")
	}

	if got := maintainerAllowReason(true, "until 2026-09-11T12:00:01", now); got != "" {
		t.Fatalf("maintainerAllowReason with an instant one second in the future = %q, want \"\" (allowed)", got)
	}

	// RFC3339 with an explicit offset is accepted too, not just the bare
	// local form.
	future := now.Add(2 * time.Hour).Format(time.RFC3339)
	if got := maintainerAllowReason(true, "until "+future, now); got != "" {
		t.Fatalf("maintainerAllowReason with a future RFC3339 instant %q = %q, want \"\" (allowed)", future, got)
	}

	// Malformed content refuses rather than panicking or reading as
	// allowed - a corrupt file must fail closed.
	for _, bad := range []string{"", "yes", "until", "until nonsense"} {
		if got := maintainerAllowReason(true, bad, now); got == "" {
			t.Fatalf("maintainerAllowReason(%q) = \"\" (allowed), want a refusal reason - malformed content must fail closed", bad)
		}
	}
}

// TestHeavyRunGuardIsProportionate pins the four cases the 2026-09-11
// proportionality correction calls out by name, against
// heavyRunIsNamedEmulatorLoop - the pure decision cmdRun (main.go) now
// consults before ever calling CheckMaintainerAllow. Case 4 (live-cert) is
// proven separately, in TestRunLiveCertRequiresAllowEvenForANamedEstate
// below, because live-cert never calls heavyRunIsNamedEmulatorLoop at all -
// RunLiveCert's own CheckMaintainerAllow call is unconditional, and that is
// exactly the point being pinned.
func TestHeavyRunGuardIsProportionate(t *testing.T) {
	cases := []struct {
		name            string
		names           []string
		setFlagExplicit bool
		wantAllowed     bool
	}{
		{
			name:            "1: named estate, no -set flag - the developer loop - allowed with no allow file",
			names:           []string{"terralith-scale"},
			setFlagExplicit: false,
			wantAllowed:     true,
		},
		{
			name:            "1b: multiple named estates, no -set flag - still the developer loop - allowed",
			names:           []string{"terralith-scale", "reference-ec2-vpc"},
			setFlagExplicit: false,
			wantAllowed:     true,
		},
		{
			name:            "2: -set core (or -set all) explicitly given - a whole set - refused without the allow file",
			names:           nil,
			setFlagExplicit: true,
			wantAllowed:     false,
		},
		{
			name:            "2b: -set explicitly given alongside named estates - still reads as \"I meant a set\" - refused",
			names:           []string{"terralith-scale"},
			setFlagExplicit: true,
			wantAllowed:     false,
		},
		{
			name: "3: bare `gauntlet run` with no names at all - resolves to the \"all\" set " +
				"(RunEstates's Set default), i.e. everything - refused without the allow file, " +
				"the same as -set all",
			names:           nil,
			setFlagExplicit: false,
			wantAllowed:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := heavyRunIsNamedEmulatorLoop(tc.names, tc.setFlagExplicit)
			if got != tc.wantAllowed {
				t.Errorf("heavyRunIsNamedEmulatorLoop(%v, setFlagExplicit=%v) = %v, want %v",
					tc.names, tc.setFlagExplicit, got, tc.wantAllowed)
			}
		})
	}
}

// TestRunLiveCertRequiresAllowEvenForANamedEstate is case 4: the named-estate
// exemption that now lets `gauntlet run <name>` proceed with no allow file
// must never leak into live-cert. RunLiveCert calls CheckMaintainerAllow
// unconditionally, before even validating the estate name against the
// manifest, so this refuses on the allow-file check alone - nothing is
// started, no script runs, target=floci or not.
func TestRunLiveCertRequiresAllowEvenForANamedEstate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("CI", "")

	_, _, _, err := RunLiveCert(".", "terralith-scale", "floci", "us-east-1", 5, 900, "")
	if err == nil {
		t.Fatal("RunLiveCert(target=floci, a single named estate) with no allow file = nil error, want a refusal - live-cert has no named-estate exemption")
	}
	path, pathErr := MaintainerAllowFile()
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("refusal %q does not name the allow file %q", err.Error(), path)
	}
	if !strings.Contains(err.Error(), "just allow-heavy-runs") {
		t.Errorf("refusal %q does not name the `just allow-heavy-runs` recipe", err.Error())
	}
}

// TestCheckMaintainerAllowSkipsInCI proves the one deliberate bypass: a
// GitHub Actions run (or anything else that sets CI) is exempt regardless
// of whether the allow file exists, because the workflow's own schedule or
// dispatch already IS the maintainer's decision.
func TestCheckMaintainerAllowSkipsInCI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // no allow file under this HOME at all
	t.Setenv("GITHUB_ACTIONS", "true")
	if err := CheckMaintainerAllow(); err != nil {
		t.Fatalf("CheckMaintainerAllow() under GITHUB_ACTIONS=true = %v, want nil (CI is exempt)", err)
	}
}

// TestCheckMaintainerAllowEndToEnd exercises CheckMaintainerAllow (not just
// the pure decision function) against a real HOME directory: missing file
// refuses, an expired file refuses, and a future file is allowed - the
// red-then-green proof for the guard as a worker actually calls it.
func TestCheckMaintainerAllowEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("CI", "")

	// RED: no allow file at all.
	err := CheckMaintainerAllow()
	if err == nil {
		t.Fatal("CheckMaintainerAllow() with no allow file = nil, want a refusal")
	}
	path, pathErr := MaintainerAllowFile()
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("refusal %q does not name the allow file %q", err.Error(), path)
	}
	if !strings.Contains(err.Error(), "just allow-heavy-runs") {
		t.Errorf("refusal %q does not name the `just allow-heavy-runs` recipe", err.Error())
	}

	// Still RED: an expired allow file.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour).Format("2006-01-02T15:04:05")
	if err := os.WriteFile(path, []byte("until "+past+"\n"), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := CheckMaintainerAllow(); err == nil {
		t.Fatal("CheckMaintainerAllow() with an expired allow file = nil, want a refusal")
	}

	// GREEN: a future allow file.
	future := time.Now().Add(2 * time.Hour).Format("2006-01-02T15:04:05")
	if err := os.WriteFile(path, []byte("until "+future+"\n"), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := CheckMaintainerAllow(); err != nil {
		t.Fatalf("CheckMaintainerAllow() with a future allow file = %v, want nil", err)
	}
}
