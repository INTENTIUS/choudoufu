// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/command/workdir"
)

const liveOutputConfig = `
terraform {
  live {
    estate = "net"
    record_store "local" {}
  }
}

output "vpc_id" {
  value = "vpc-123"
}

output "subnets" {
  value = ["a", "b"]
}

output "secret" {
  value     = "hunter2"
  sensitive = true
}

output "later" {
  value = "x"
}
`

func runLiveOutput(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	td := t.TempDir()
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(liveOutputConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(td)
	recordProducerOutput(t, td, "net", "vpc_id", cty.StringVal("vpc-123"))
	recordProducerOutput(t, td, "net", "subnets", cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}))

	view, done := testView(t)
	meta := Meta{WorkingDir: workdir.NewDir("."), testingOverrides: metaOverridesForProvider(testProvider()), View: view}
	code := RunCommander(t, OutputCommander(), meta, args)
	out := done(t)
	return code, out.Stdout(), out.Stderr()
}

// A fresh checkout has no state cache; the answer comes from the store, and
// an output with no recorded value (sensitive, or not yet applied) is left
// out with a warning rather than answered as null.
func TestOutput_liveJSONFromRecordStore(t *testing.T) {
	code, stdout, stderr := runLiveOutput(t, "-json")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"vpc_id"`) || !strings.Contains(stdout, `"vpc-123"`) || !strings.Contains(stdout, `"subnets"`) {
		t.Errorf("recorded outputs missing:\n%s", stdout)
	}
	if strings.Contains(stdout, "hunter2") || strings.Contains(stdout, `"secret"`) || strings.Contains(stdout, `"later"`) {
		t.Errorf("unrecorded outputs must be omitted:\n%s", stdout)
	}
	if strings.Contains(stdout, "Warning") {
		t.Errorf("a warning on stdout corrupts the JSON document:\n%s", stdout)
	}
	if !strings.Contains(stderr, "later, secret") {
		t.Errorf("warning does not name the omitted outputs:\nstdout:%s\nstderr:%s", stdout, stderr)
	}
}

func TestOutput_liveNamed(t *testing.T) {
	code, stdout, stderr := runLiveOutput(t, "vpc_id")
	if code != 0 || strings.TrimSpace(stdout) != `"vpc-123"` {
		t.Fatalf("exit %d stdout %q\n%s", code, stdout, stderr)
	}
}

func TestOutput_liveNamedSensitiveRefuses(t *testing.T) {
	code, stdout, stderr := runLiveOutput(t, "-json", "secret")
	if code == 0 {
		t.Fatalf("a sensitive output has no recorded value and must not succeed:\n%s", stdout)
	}
	if !strings.Contains(strings.Join(strings.Fields(stderr), " "), "marked sensitive") {
		t.Errorf("refusal does not say why:\n%s", stderr)
	}
}

func TestOutput_liveNamedUnappliedRefuses(t *testing.T) {
	code, _, stderr := runLiveOutput(t, "later")
	if code == 0 || !strings.Contains(stderr, "no recorded value") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
}
