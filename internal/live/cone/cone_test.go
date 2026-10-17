// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cone

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// An estate of eleven blocks, so one changed block and its readers stay
// under the 30% share: a -> b -> c chains, and eight unrelated blocks.
const baseMain = `
provider "aws" {}
resource "aws_s3_bucket" "a" { bucket = "a" }
resource "aws_s3_bucket_versioning" "b" { bucket = aws_s3_bucket.a.id }
locals { b_id = aws_s3_bucket_versioning.b.id }
resource "aws_ssm_parameter" "c" { value = local.b_id }
resource "aws_ssm_parameter" "p1" { value = "1" }
resource "aws_ssm_parameter" "p2" { value = "2" }
resource "aws_ssm_parameter" "p3" { value = "3" }
resource "aws_ssm_parameter" "p4" { value = "4" }
resource "aws_ssm_parameter" "p5" { value = "5" }
resource "aws_ssm_parameter" "p6" { value = "6" }
resource "aws_ssm_parameter" "p7" { value = "7" }
resource "aws_ssm_parameter" "gone" { value = "x" }
`

func repo(t *testing.T, base string, head func(dir string)) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "base")
	head(dir)
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compute(t *testing.T, dir string) *Result {
	t.Helper()
	res, err := Compute(context.Background(), Options{Dir: dir, Base: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The cone is the changed block and its descendants, not its ancestors
// (the plan graph adds those), plus the deleted block's address.
func TestConeReachesDescendants(t *testing.T) {
	var dir string
	dir = repo(t, baseMain, func(d string) {})
	head := `
provider "aws" {}
resource "aws_s3_bucket" "a" { bucket = "a" }
resource "aws_s3_bucket_versioning" "b" { bucket = "${aws_s3_bucket.a.id}-v" }
locals { b_id = aws_s3_bucket_versioning.b.id }
resource "aws_ssm_parameter" "c" { value = local.b_id }
resource "aws_ssm_parameter" "p1" { value = "1" }
resource "aws_ssm_parameter" "p2" { value = "2" }
resource "aws_ssm_parameter" "p3" { value = "3" }
resource "aws_ssm_parameter" "p4" { value = "4" }
resource "aws_ssm_parameter" "p5" { value = "5" }
resource "aws_ssm_parameter" "p6" { value = "6" }
resource "aws_ssm_parameter" "p7" { value = "7" }
`
	write(t, dir, "main.tf", head)
	res := compute(t, dir)
	if res.Guard.Full {
		t.Fatalf("guard fell back: %+v", res.Guard)
	}
	want := []string{"aws_s3_bucket_versioning.b", "aws_ssm_parameter.c", "aws_ssm_parameter.gone"}
	if !reflect.DeepEqual(res.Targets, want) {
		t.Fatalf("targets = %v, want %v", res.Targets, want)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"aws_ssm_parameter.gone"}) {
		t.Fatalf("deleted = %v", res.Deleted)
	}
}

// Each guard condition forces a full plan and names itself.
func TestConeGuards(t *testing.T) {
	cases := map[string]struct {
		edit func(t *testing.T, dir string)
		base string
		kind string
	}{
		"provider": {edit: func(t *testing.T, d string) {
			write(t, d, "main.tf", baseMain+"\nprovider \"aws\" {\n alias = \"west\"\n}\n")
		}, kind: GuardProvider},
		"lock file": {edit: func(t *testing.T, d string) {
			write(t, d, ".terraform.lock.hcl", "# changed\n")
			write(t, d, "x.tf", `resource "aws_ssm_parameter" "p1x" { value = "1" }`)
		}, kind: GuardLockFile},
		"live block": {edit: func(t *testing.T, d string) {
			write(t, d, "live.tf", "live {\n estate = \"e\"\n}\n")
		}, kind: GuardLiveBlock},
		"share": {edit: func(t *testing.T, d string) {
			write(t, d, "main.tf", baseMain+"\nvariable \"v\" {}\n")
			write(t, d, "terraform.tfvars", "v = 1\n")
			write(t, d, "x.tf", `
resource "aws_ssm_parameter" "q1" { value = var.v }
resource "aws_ssm_parameter" "q2" { value = var.v }
resource "aws_ssm_parameter" "q3" { value = var.v }
resource "aws_ssm_parameter" "q4" { value = var.v }
resource "aws_ssm_parameter" "q5" { value = var.v }
resource "aws_ssm_parameter" "q6" { value = var.v }
`)
		}, kind: GuardShare},
		"unplaced moved": {edit: func(t *testing.T, d string) {
			write(t, d, "moved.tf", "moved {\n from = aws_ssm_parameter.p1\n to = aws_ssm_parameter.nowhere\n}\n")
		}, kind: GuardUnplaced},
		"empty":   {edit: func(t *testing.T, d string) {}, kind: GuardEmpty},
		"no base": {edit: func(t *testing.T, d string) {}, base: "no-such-ref", kind: GuardNoBase},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := repo(t, baseMain, func(string) {})
			tc.edit(t, dir)
			base := "HEAD"
			if tc.base != "" {
				base = tc.base
			}
			res, err := Compute(context.Background(), Options{Dir: dir, Base: base})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Guard.Full || res.Guard.Kind != tc.kind {
				t.Fatalf("guard = %+v, want full for %s", res.Guard, tc.kind)
			}
		})
	}
}

// A co-managed pair widens the cone to the other type's blocks.
func TestConeWidensCoManaged(t *testing.T) {
	base := baseMain + `
resource "aws_security_group" "sg" { name = "sg" }
resource "aws_security_group_rule" "r" { type = "ingress" }
`
	dir := repo(t, base, func(string) {})
	write(t, dir, "main.tf", base+"\n")
	write(t, dir, "main.tf", baseMain+`
resource "aws_security_group" "sg" { name = "sg2" }
resource "aws_security_group_rule" "r" { type = "ingress" }
`)
	res := compute(t, dir)
	if !reflect.DeepEqual(res.Widened, []string{"aws_security_group_rule.r"}) {
		t.Fatalf("widened = %v", res.Widened)
	}
}

// BREAK: a cone whose reach drops dependents leaves the dependent's update
// out, and the claim's check reports it rather than passing.
func TestConeBreakDroppedDependentIsCaught(t *testing.T) {
	full := []string{"aws_s3_bucket_versioning.b", `aws_ssm_parameter.c`}
	dir := repo(t, baseMain, func(string) {})
	write(t, dir, "main.tf", strings.Replace(baseMain, `bucket = aws_s3_bucket.a.id`, `bucket = "${aws_s3_bucket.a.id}-v"`, 1))
	res := compute(t, dir)
	if got := MissedUpdates(full, res.Targets); len(got) != 0 {
		t.Fatalf("the real cone missed %v", got)
	}
	broken := []string{"aws_s3_bucket_versioning.b"} // reach without descendants
	if got := MissedUpdates(full, broken); !reflect.DeepEqual(got, []string{"aws_ssm_parameter.c"}) {
		t.Fatalf("a cone without descendants was not caught: %v", got)
	}
}

// A root in a subdirectory of its repository reads its base files from
// that subdirectory (#2062). The listing used to come back empty there, so
// every block read as added and the cone fell back naming the terraform
// block.
func TestConeRootInSubdirectory(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, "live", "a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const tfBlock = "terraform {\n  required_providers {\n    aws = { source = \"hashicorp/aws\" }\n  }\n}\n"
	gitT(t, top, "init", "-q")
	write(t, dir, "versions.tf", tfBlock)
	write(t, dir, "main.tf", baseMain)
	write(t, top, "README", "repo top\n")
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "-m", "base")
	write(t, dir, "main.tf", strings.Replace(baseMain, `{ value = "7" }`, `{ value = "8" }`, 1))

	res := compute(t, dir)
	if res.Guard.Full {
		t.Fatalf("guard fell back: %+v", res.Guard)
	}
	if want := []string{"aws_ssm_parameter.p7"}; !reflect.DeepEqual(res.Targets, want) {
		t.Fatalf("targets = %v, want %v", res.Targets, want)
	}

	// A fallback names what differs: an edited terraform block reads as
	// changed, and one with no counterpart at base reads as added.
	write(t, dir, "versions.tf", strings.Replace(tfBlock, "terraform {\n", "terraform {\n  backend \"local\" {}\n", 1))
	if res := compute(t, dir); res.Guard.Kind != GuardTerraform || !strings.HasPrefix(res.Guard.Reason, "the terraform block changed") {
		t.Fatalf("edited terraform block: guard = %+v", res.Guard)
	}
	write(t, dir, "versions.tf", "")
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-q", "-m", "drop terraform block")
	write(t, dir, "versions.tf", tfBlock)
	if res := compute(t, dir); res.Guard.Kind != GuardTerraform || res.Guard.Reason != "the terraform block was added" {
		t.Fatalf("added terraform block: guard = %+v", res.Guard)
	}
}
