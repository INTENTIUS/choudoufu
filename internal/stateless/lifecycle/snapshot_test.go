// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// TestStatelessSnapshotAgainstFloci is P4.2's live half: it drives the same
// two plain commands as [TestStatelessLifecycleAgainstFloci] against a real
// (emulated) cloud, but with a "snapshot_path" argument in the stateless
// block, and checks the three claims P4.2 makes about the optional
// observational snapshot rather than the ones P4.1 already covers:
//
//		TF_FLOCI_TEST=1 go test ./internal/stateless/lifecycle/ -run TestStatelessSnapshotAgainstFloci -v
//
//	 1. An apply with snapshot_path set writes the file, after the run, naming
//	    the estate and listing the resources the run applied - metadata and a
//	    hash per resource, never an attribute value.
//	 2. A plan run's behavior does not depend on whether that file exists:
//	    deleting it before a plan and comparing against a plan taken with it
//	    present produces the same output either way. That is the closest a
//	    black-box test gets to proving "nothing reads it" - the white-box
//	    proof is TestSnapshot_noReader in internal/stateless/projection.
//	 3. The snapshot is the only thing this run leaves behind. Every artifact
//	    assertNoState already checks for still must not exist.
//
// It runs on its own floci container on its own port, entirely independent
// of P4.1's lifecycle test, so the two can run concurrently without
// colliding.
func TestStatelessSnapshotAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "stateless snapshot")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	flocitest.StartFloci(t, snapshotContainerFn, snapshotFlociPort)

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+snapshotFlociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	t.Setenv("TF_PLUGIN_CACHE_DIR", t.TempDir())

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()
	writeFixture(t, dir, snapshotFixture())

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	assertNoState(t, dir, "after init")

	// --- 1. Apply writes the snapshot, and only the snapshot -------------

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
	// lock.info, the workspace directory) does not include the snapshot
	// path, so this still passes with the snapshot file present - which is
	// exactly the claim: the snapshot is not a state artifact.
	assertNoState(t, dir, "after the apply")

	snapPath := filepath.Join(dir, "snapshot", "estate.json")
	raw, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("reading the snapshot at %s: %v", snapPath, err)
	}

	var snap snapshotFile
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decoding the snapshot: %v\nraw: %s", err, raw)
	}
	if snap.FormatVersion == "" {
		t.Error("the snapshot has no formatVersion")
	}
	if snap.Estate != snapshotEstate {
		t.Errorf("the snapshot names estate %q, want %q", snap.Estate, snapshotEstate)
	}
	if snap.WrittenAt == "" {
		t.Error("the snapshot has no writtenAt")
	} else if _, err := time.Parse(time.RFC3339, snap.WrittenAt); err != nil {
		t.Errorf("writtenAt %q does not parse as RFC3339: %v", snap.WrittenAt, err)
	}

	byAddr := make(map[string]snapshotResource, len(snap.Resources))
	for _, res := range snap.Resources {
		byAddr[res.Address] = res
	}
	for _, want := range []string{"aws_vpc.main", "aws_s3_bucket.data"} {
		res, found := byAddr[want]
		if !found {
			t.Errorf("the snapshot does not list %s; got %v", want, byAddr)
			continue
		}
		if res.AttributesHash == "" {
			t.Errorf("%s has no attributesHash", want)
		}
		if res.Markers["tofu-estate"] != snapshotEstate {
			t.Errorf("%s markers do not carry tofu-estate: %v", want, res.Markers)
		}
	}
	// Never an attribute value. The bucket name is a poor witness for this:
	// the AWS provider's own resource identity for aws_s3_bucket carries the
	// bucket name too, and identity is explicitly part of the shape (the
	// "importID/identity" field), not a leak. The VPC's CIDR block is a
	// clean test instead - it is a plain attribute, absent from
	// aws_vpc's identity (id, account_id, region only), so its absence from
	// the raw bytes is evidence about attributes generally, not identity.
	if strings.Contains(string(raw), snapshotVPCCIDR) {
		t.Errorf("the snapshot contains an attribute value (the VPC CIDR %q):\n%s", snapshotVPCCIDR, raw)
	}

	// --- 2. A plan's behavior does not depend on the snapshot existing ---

	withFile := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(withFile, "No changes.") {
		t.Fatalf("the plan with the snapshot present is not clean:\n%s", withFile)
	}

	if err := os.Remove(snapPath); err != nil {
		t.Fatalf("deleting the snapshot before the second plan: %v", err)
	}
	withoutFile := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(withoutFile, "No changes.") {
		t.Fatalf("the plan with the snapshot deleted is not clean:\n%s", withoutFile)
	}
	if withFile != withoutFile {
		t.Errorf("plan output changed depending on whether the snapshot file existed, which means something read it:\n--- with file ---\n%s\n--- without file ---\n%s", withFile, withoutFile)
	}
	// The plan does not call PersistState at all (only apply does), so the
	// deleted snapshot must still be gone: a plan recreating it would itself
	// be evidence the file is doing more than caching.
	if _, err := os.Stat(snapPath); !os.IsNotExist(err) {
		t.Errorf("the snapshot reappeared after a plan, which never persists: stat error %v", err)
	}

	assertNoState(t, dir, "at the end of the snapshot test")
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const (
	// snapshotFlociPort is this test's own floci port, distinct from the
	// lifecycle test's (4615) so the two can run at the same time.
	snapshotFlociPort = "4618"

	snapshotEstate      = "p42-snapshot"
	snapshotVPCCIDR     = "10.62.0.0/16"
	snapshotBucketName  = "p42-snapshot-data"
	snapshotContainerFn = "tofu-stateless-p42-snapshot"
)

// snapshotFixture is a small estate - one marker-path resource, one
// client-named one - with snapshot_path set in the live block. Small on
// purpose: this test is about the cache file, not about estate coverage,
// which the P4.1 lifecycle test and the unit suites already own.
func snapshotFixture() string {
	return fmt.Sprintf(`terraform {
  required_version = ">= 1.5.0"

  live {
    estate        = %q
    snapshot_path = "snapshot/estate.json"
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
`, snapshotEstate, snapshotVPCCIDR, snapshotBucketName)
}

// ---------------------------------------------------------------------------
// The snapshot's own shape, decoded independently of the writer
// ---------------------------------------------------------------------------
//
// These types are a deliberate, hand-written duplicate of
// internal/stateless/projection.Snapshot rather than an import of it: this
// test's whole point is to look at the file as an outside consumer would,
// the same way it reads the cloud with the AWS CLI instead of asking tofu to
// confirm its own story. Importing the writer's own struct would make a
// schema drift invisible to exactly the test meant to catch it.

type snapshotFile struct {
	FormatVersion string             `json:"formatVersion"`
	WrittenAt     string             `json:"writtenAt"`
	Estate        string             `json:"estate"`
	Resources     []snapshotResource `json:"resources"`
}

type snapshotResource struct {
	Address        string            `json:"address"`
	Type           string            `json:"type"`
	AttributesHash string            `json:"attributesHash"`
	Markers        map[string]string `json:"markers"`
}

// ---------------------------------------------------------------------------
// floci, on this test's own port
// ---------------------------------------------------------------------------
