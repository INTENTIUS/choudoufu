// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub issue #353's lint half: a provisioner is refused when the estate
// has nowhere to keep the one bit stock OpenTofu keeps about it (the
// tainted flag a failed create-time provisioner sets), and admitted when it
// does.
//
// Issue #364 moved where the line falls without moving the gate itself.
// "Nowhere to keep it" used to mean "a live block with no record_store
// block in it"; it now means "no live block at all", because every live
// block implies a local record store
// (internal/configs.impliedRecordStore). The gate still reads
// cfg.Module.Live.RecordStore and still refuses exactly when it is nil -
// what changed is which configurations produce a nil.
//
// The three fixtures below differ in exactly one thing - what the live
// block says about a store - which is the mutation check built into the
// test's own shape rather than bolted on afterwards: if the gate stopped
// reading that declaration, the "admitted" cases and the "refused" case
// would agree, and one of the subtests fails whichever way the gate broke.

// provisionerStore is what a [provisionerFixture] says about its record
// store. The three values are the three configurations that exist after
// issue #364, and every one of them is a shape a real user writes.
type provisionerStore int

const (
	// provisionerNoLiveBlock is a configuration with no live block at all:
	// not an estate, nothing to imply a store for, and the one remaining
	// way to reach a nil RecordStore. This is what `choudoufu live-check`
	// reads when it analyses a stock configuration before anyone has
	// adopted it, which is why the refusal it produces still matters.
	provisionerNoLiveBlock provisionerStore = iota
	// provisionerImpliedStore is a live block with no record_store block:
	// the implied local store, and the shape HANDOFF.md's "a configuration
	// that works on stock OpenTofu works here with a live block added and
	// nothing else" names.
	provisionerImpliedStore
	// provisionerDeclaredStore is a live block with a record_store block
	// written out longhand. It must behave identically to
	// provisionerImpliedStore - that is what implying it means.
	provisionerDeclaredStore
)

// provisionerFixture renders a configuration with all three provisioner
// types issue #353 admits uniformly, plus a connection block.
func provisionerFixture(store provisionerStore) string {
	var live string
	switch store {
	case provisionerNoLiveBlock:
		live = ""
	case provisionerImpliedStore:
		live = `
terraform {
  live {
    estate = "provisioner-gate"
  }
}
`
	case provisionerDeclaredStore:
		live = `
terraform {
  live {
    estate = "provisioner-gate"
    record_store "local" {
      path = "./records"
    }
  }
}
`
	}
	return live + `
resource "aws_instance" "web" {
  ami           = "ami-1234"
  instance_type = "t3.micro"

  connection {
    type = "ssh"
    host = "example.invalid"
  }

  provisioner "local-exec" {
    command = "echo hello"
  }

  provisioner "remote-exec" {
    inline = ["echo hello"]
  }

  provisioner "file" {
    source      = "conf/app.conf"
    destination = "/etc/app.conf"
  }
}
`
}

func loadProvisionerFixture(t *testing.T, store provisionerStore) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(provisionerFixture(store)), 0o600); err != nil {
		t.Fatalf("writing the fixture: %s", err)
	}
	return dir
}

// TestProvisionerRefusedWithoutARecordStore is the unchanged half, on the
// one configuration that still has no record store after issue #364: no
// live block at all. There is nowhere for the tainted bit to live, so all
// three provisioner types and the connection block are refused - and the
// refusal has to NAME the declaration that would change the answer, or an
// operator reading it learns only that they must delete their
// configuration.
//
// This is `live-check`'s reading of a configuration nobody has adopted yet,
// and the reason the branch is still worth having: the answer it gives is
// "add a live block and this is admitted", which is now literally the whole
// setup step.
func TestProvisionerRefusedWithoutARecordStore(t *testing.T) {
	cfg := loadConfigDir(t, loadProvisionerFixture(t, provisionerNoLiveBlock))
	issues := CheckContext(t.Context(), cfg)

	var got []Issue
	for _, issue := range issues {
		if issue.Rule == RuleProvisioner {
			got = append(got, issue)
		}
	}
	// Three provisioners plus the connection block.
	if len(got) != 4 {
		t.Fatalf("got %d RuleProvisioner issues, want 4 (three provisioners and the connection block): %v", len(got), got)
	}

	wantConstructs := map[string]bool{
		`provisioner "local-exec" on aws_instance.web`:  false,
		`provisioner "remote-exec" on aws_instance.web`: false,
		`provisioner "file" on aws_instance.web`:        false,
		`connection block on aws_instance.web`:          false,
	}
	for _, issue := range got {
		if _, known := wantConstructs[issue.Construct]; !known {
			t.Errorf("unexpected refused construct %q", issue.Construct)
			continue
		}
		wantConstructs[issue.Construct] = true
		if !strings.Contains(issue.Detail, "record_store") {
			t.Errorf("%s: Detail = %q, want it to name the record_store declaration that would admit it", issue.Construct, issue.Detail)
		}
	}
	for construct, fired := range wantConstructs {
		if !fired {
			t.Errorf("%s was not refused", construct)
		}
	}
}

// TestProvisionerAdmittedWithARecordStore is the new half, and the whole of
// issue #353's lint change, now asserted for BOTH stores. The three
// provisioner types are admitted uniformly because the predicate ("does
// this instance have somewhere to carry a tainted bit") has nothing in it
// that could tell them apart - and the implied store and the declared one
// have nothing in them that could tell each other apart either, which is
// issue #364's own claim.
func TestProvisionerAdmittedWithARecordStore(t *testing.T) {
	for name, store := range map[string]provisionerStore{
		"declared record_store":      provisionerDeclaredStore,
		"implied local record store": provisionerImpliedStore,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadConfigDir(t, loadProvisionerFixture(t, store))

			for _, issue := range CheckContext(t.Context(), cfg) {
				if issue.Rule == RuleProvisioner {
					t.Errorf("a provisioner was refused with a %s: %s", name, issue)
				}
			}
		})
	}
}

// TestProvisionerGateDoesNotDisturbTheLogicalPath is the regression guard
// the issue asks for by name. A record-backed logical type's provisioners
// were already admitted through checkProvisioners' isLogical branch, which
// runs BEFORE the new record_store branch; the two must not have become one
// test that passes for the wrong reason. So: a null_resource with a
// provisioner and NO record_store must still produce exactly the
// RuleLogicalResource refusal it always did, and no RuleProvisioner issue
// on top of it - "one verdict per resource", which is the property the
// isLogical branch exists to keep.
//
// Since issue #364 the refusing half of this needs a configuration with no
// live block, which is the only remaining nil-store shape; the second
// subtest is the same resource under the implied store, where BOTH counts
// must be zero. That pairing is what keeps the guard honest: a change that
// made checkProvisioners fire on logical types would show up as a non-zero
// provisioner count in either half.
func TestProvisionerGateDoesNotDisturbTheLogicalPath(t *testing.T) {
	const resource = `
resource "null_resource" "effect" {
  provisioner "local-exec" {
    command = "echo hello"
  }
}
`
	const liveBlock = `
terraform {
  live {
    estate = "provisioner-gate"
  }
}
`
	for _, tc := range []struct {
		name        string
		src         string
		wantLogical int
	}{
		{"no live block: one logical refusal and nothing else", resource, 1},
		{"implied local record store: admitted, and still no provisioner verdict", liveBlock + resource, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tc.src), 0o600); err != nil {
				t.Fatalf("writing the fixture: %s", err)
			}
			cfg := loadConfigDir(t, dir)

			var logical, provisioner int
			for _, issue := range CheckContext(t.Context(), cfg) {
				switch issue.Rule {
				case RuleLogicalResource:
					logical++
				case RuleProvisioner:
					provisioner++
					t.Errorf("a record-backed logical type also got a provisioner refusal: %s", issue)
				}
			}
			if logical != tc.wantLogical {
				t.Errorf("got %d RuleLogicalResource issues, want exactly %d", logical, tc.wantLogical)
			}
		})
	}
}
