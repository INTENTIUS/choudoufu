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
// The three fixtures below differ in exactly one line - the record_store
// block - which is the mutation check built into the test's own shape
// rather than bolted on afterwards: if the gate stopped reading that
// declaration, the "admitted" case and the "refused" case would agree, and
// one of the two subtests fails whichever way the gate broke.

// provisionerFixture renders a configuration with all three provisioner
// types issue #353 admits uniformly, plus a connection block, optionally
// with a record_store declared.
func provisionerFixture(withStore bool) string {
	store := ""
	if withStore {
		store = `
    record_store "local" {
      path = "./records"
    }
`
	}
	return `
terraform {
  live {
    estate = "provisioner-gate"
` + store + `  }
}

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

func loadProvisionerFixture(t *testing.T, withStore bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(provisionerFixture(withStore)), 0o600); err != nil {
		t.Fatalf("writing the fixture: %s", err)
	}
	return dir
}

// TestProvisionerRefusedWithoutARecordStore is the unchanged half. Without
// a record_store there is nowhere for the tainted bit to live, so all three
// provisioner types and the connection block are refused - and the refusal
// has to NAME the declaration that would change the answer, or an operator
// reading it learns only that they must delete their configuration.
func TestProvisionerRefusedWithoutARecordStore(t *testing.T) {
	cfg := loadConfigDir(t, loadProvisionerFixture(t, false))
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
// issue #353's lint change. The SAME configuration with a record_store
// declared is admitted outright: the three provisioner types are admitted
// uniformly because the predicate ("does this instance have somewhere to
// carry a tainted bit") has nothing in it that could tell them apart.
func TestProvisionerAdmittedWithARecordStore(t *testing.T) {
	cfg := loadConfigDir(t, loadProvisionerFixture(t, true))

	for _, issue := range CheckContext(t.Context(), cfg) {
		if issue.Rule == RuleProvisioner {
			t.Errorf("a provisioner was refused despite a record_store being declared: %s", issue)
		}
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
func TestProvisionerGateDoesNotDisturbTheLogicalPath(t *testing.T) {
	const src = `
terraform {
  live {
    estate = "provisioner-gate"
  }
}

resource "null_resource" "effect" {
  provisioner "local-exec" {
    command = "echo hello"
  }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
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
	if logical != 1 {
		t.Errorf("got %d RuleLogicalResource issues, want exactly 1", logical)
	}
}
