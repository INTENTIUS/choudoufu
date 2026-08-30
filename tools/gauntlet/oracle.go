// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
)

// OracleVersionsPin is live/oracle-versions.json's own path, repo-relative -
// the single place issue #544 asks both .github/workflows/gauntlet.yml and
// .github/workflows/contribute.yml to read the stock terraform and tofu
// releases from, instead of each hashicorp/setup-terraform and
// opentofu/setup-opentofu step resolving its own unpinned "latest". A human
// maintains this file deliberately - see justfile's oracle-bump recipe and
// tools/oracle-bump-report for the reviewed-event procedure a bump follows.
const OracleVersionsPin = "live/oracle-versions.json"

// oracleVersions reads live/oracle-versions.json - the CONFIGURATION half of
// issue #544, a.Oracle's value on every Rebuild. A missing or unparsable
// file reads as the zero value, the same graceful-empty behaviour
// emulatorPin already has for a missing live/floci-image.
func oracleVersions(root string) OracleVersions {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OracleVersionsPin)))
	if err != nil {
		return OracleVersions{}
	}
	var pin struct {
		Terraform string `json:"terraform_version"`
		Tofu      string `json:"tofu_version"`
	}
	if err := json.Unmarshal(b, &pin); err != nil {
		return OracleVersions{}
	}
	return OracleVersions{Terraform: pin.Terraform, Tofu: pin.Tofu}
}

// oracleVersionProbe is overridden in tests so a probeOracle assertion never
// depends on what terraform/tofu happen to be installed on the machine
// running `go test` - see oracle_test.go.
var oracleVersionProbe = probeOracleVersion

// probeOracle is the EVIDENCE half of issue #544: what run actually used,
// found by asking the binaries on PATH right now, exactly the way commit is
// a real `git rev-parse HEAD` rather than a value copied from
// configuration. Called once per RunEstates invocation (run.go) and stamped
// onto every row that run touches - PATH does not change mid-run, so one
// probe describes every script that run launches.
//
// This is deliberately NOT a check against live/oracle-versions.json: CI's
// hashicorp/setup-terraform and opentofu/setup-opentofu steps install
// exactly what that file pins, so the two agree there, but nothing enforces
// that on a local checkout - a developer's PATH can drift from the pin
// silently, which was #498's actual root cause (a local terraform 1.15.8
// against a CI terraform that had just become 1.16.0). Recording what was
// really on PATH, rather than asserting the pin was honoured, is what makes
// that drift visible after the fact instead of assumed away.
func probeOracle() OracleVersions {
	return OracleVersions{
		Terraform: oracleVersionProbe("terraform"),
		Tofu:      oracleVersionProbe("tofu"),
	}
}

// probeOracleVersion runs `<bin> version -json` and returns its
// terraform_version field (OpenTofu kept that JSON key name for
// compatibility, so one parse handles both binaries). "" means the binary is
// not on PATH, or its output did not parse - an absent oracle is recorded as
// absent evidence, never guessed at.
func probeOracleVersion(bin string) string {
	out, err := exec.Command(bin, "version", "-json").Output()
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return ""
	}
	return v.Version
}
