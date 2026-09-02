// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestStatelessHintAgainstFloci is issue #109's live half: it drives the
// same two plain commands as [TestStatelessLifecycleAgainstFloci] against a
// real (emulated) cloud, but with a "record_store" block in the live block,
// and checks the three claims the guided-discovery hint makes:
//
//	TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestStatelessHintAgainstFloci -v
//
//	1. An apply with a record_store persists the hint into that store,
//	   after the run, naming the estate and the resource types the run
//	   applied - a type roster and a timestamp, never an attribute value.
//	   Read back through the real staterecord.Store + projection reader,
//	   the same pair a later run's guided discovery uses.
//	2. A plan's ANSWER does not depend on the hint existing: with the hint
//	   present and with it deleted, the plan is clean either way. The hint
//	   changes what a pass costs, never what it concludes - the black-box
//	   half of what TestGuided_equivalence proves white-box.
//	3. The record directory is the only thing this run leaves behind. Every
//	   artifact assertNoState already checks for still must not exist, and
//	   a plan (which never persists) must not resurrect a deleted hint.
//
// It runs on its own floci container on its own port, entirely independent
// of P4.1's lifecycle test, so the two can run concurrently without
// colliding.
func TestStatelessHintAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "stateless hint")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	hintFlociPort := flocitest.StartFloci(t, hintContainerFn)

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+hintFlociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()
	writeFixture(t, dir, hintFixture())

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	assertNoState(t, dir, "after init")

	// --- 1. Apply persists the hint, and only the hint -------------------

	apply := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(apply, "Apply complete!") {
		t.Fatalf("the apply did not complete:\n%s", apply)
	}
	added, changed, destroyed, ok := applySummary(apply)
	if !ok {
		t.Fatalf("no apply summary:\n%s", apply)
	}
	if added != 2 || changed != 0 || destroyed != 0 {
		t.Errorf("want 2 added / 0 changed / 0 destroyed, got %d/%d/%d", added, changed, destroyed)
	}
	// assertNoState's own list of state artifacts (tfstate, tfstate.backup,
	// lock.info, the workspace directory) does not include the record
	// directory, so this still passes with the hint present - which is
	// exactly the claim: the hint is not a state artifact.
	assertNoState(t, dir, "after the apply")

	store, err := staterecord.NewLocalStore(filepath.Join(dir, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store the apply should have written into: %v", err)
	}
	ctx := context.Background()
	hint, err := projection.ReadHintStore(ctx, store, hintEstate)
	if err != nil {
		t.Fatalf("no hint was persisted by the apply: %v", err)
	}
	if hint.Estate != hintEstate {
		t.Errorf("the hint names estate %q, want %q", hint.Estate, hintEstate)
	}
	if hint.WrittenAt.IsZero() {
		t.Error("the hint has no writtenAt")
	} else if age := time.Since(hint.WrittenAt); age < 0 || age > 10*time.Minute {
		t.Errorf("the hint's writtenAt (%s) is not from this run", hint.WrittenAt)
	}
	for _, want := range []string{"aws_vpc", "aws_s3_bucket"} {
		if !hint.Types[want] {
			t.Errorf("the hint does not record type %s: %v", want, hint.Types)
		}
	}
	// Never an attribute value: the whole record is a type roster and a
	// timestamp, so the VPC's CIDR (a plain attribute) has nowhere to
	// travel. Checked against the raw stored bytes, not the decoded shape.
	raw, _, exists, err := store.Get(ctx, projection.HintKey(hintEstate))
	if err != nil || !exists {
		t.Fatalf("re-reading the raw hint record: exists=%v err=%v", exists, err)
	}
	if strings.Contains(string(raw), hintVPCCIDR) {
		t.Errorf("the hint contains an attribute value (the VPC CIDR %q):\n%s", hintVPCCIDR, raw)
	}

	// --- 2. A plan's answer does not depend on the hint existing ---------

	withHint := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(withHint, "No changes.") {
		t.Fatalf("the plan with the hint present is not clean:\n%s", withHint)
	}

	_, version, _, err := store.Get(ctx, projection.HintKey(hintEstate))
	if err != nil {
		t.Fatalf("reading the hint's version before deleting it: %v", err)
	}
	if err := store.Delete(ctx, projection.HintKey(hintEstate), version); err != nil {
		t.Fatalf("deleting the hint before the second plan: %v", err)
	}
	withoutHint := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(withoutHint, "No changes.") {
		t.Fatalf("the plan with the hint deleted is not clean:\n%s", withoutHint)
	}

	// --- 3. A plan never persists, so the deleted hint stays gone --------

	if _, err := projection.ReadHintStore(ctx, store, hintEstate); err == nil {
		t.Error("the hint reappeared after a plan, which never persists")
	}

	assertNoState(t, dir, "at the end of the hint test")
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const (
	hintEstate      = "issue109-hint"
	hintVPCCIDR     = "10.63.0.0/16"
	hintBucketName  = "issue109-hint-data"
	hintContainerFn = "cdf-109-hint"
)

// hintFixture is a small estate - one marker-path resource, one
// client-named one - with a record_store "local" block in the live block.
// Small on purpose: this test is about the hint, not about estate coverage,
// which the P4.1 lifecycle test and the unit suites already own.
func hintFixture() string {
	return fmt.Sprintf(`terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q

    record_store "local" {}
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}

resource "aws_vpc" "main" {
  cidr_block = %q
}

resource "aws_s3_bucket" "data" {
  bucket = %q
}
`, hintEstate, hintVPCCIDR, hintBucketName)
}
