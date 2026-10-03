// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const sharedModule = `
variable "name" { type = string }
variable "prefix" { type = string }
resource "aws_sqs_queue" "q" { name = "${var.prefix}-${var.name}" }
`

func fixtureRoot(name, body string) string {
	return `
module "shared" {
  source = "../../modules/shared"
  name   = "` + name + `"
  prefix = "ls"
}
` + body
}

// chainFiles is #1750's fixture at N=5 as largeset-gen writes it, cut to
// what wave planning reads: one estate per root in an estate.chdf.hcl
// sidecar, one shared module by local path, and the depth-2 chain of
// marker-filtered reads.
func chainFiles() map[string]string {
	return map[string]string{
		"modules/shared/main.tf":      sharedModule,
		"estates/e01/estate.chdf.hcl": `estate = "ls-e01"`,
		"estates/e01/main.tf":         fixtureRoot("e01", `resource "aws_vpc" "network" { cidr_block = "10.100.0.0/16" }`),
		"estates/e02/estate.chdf.hcl": `estate = "ls-e02"`,
		"estates/e02/main.tf": fixtureRoot("e02", `
data "aws_vpc" "upstream" {
  filter {
    name   = "tag:tofu-estate"
    values = ["ls-e01"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.network"]
  }
}
resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.upstream.id
  cidr_block = "10.100.1.0/24"
}`),
		"estates/e03/estate.chdf.hcl": `estate = "ls-e03"`,
		"estates/e03/main.tf": fixtureRoot("e03", `
data "aws_subnet" "upstream" {
  filter {
    name   = "tag:tofu-estate"
    values = ["ls-e02"]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_subnet.app"]
  }
}
resource "aws_sqs_queue" "downstream" {
  name = "ls-e03-downstream"
  tags = { "upstream-subnet" = data.aws_subnet.upstream.id }
}`),
		"estates/e04/estate.chdf.hcl": `estate = "ls-e04"`,
		"estates/e04/main.tf":         fixtureRoot("e04", ""),
		"estates/e05/estate.chdf.hcl": `estate = "ls-e05"`,
		"estates/e05/main.tf":         fixtureRoot("e05", ""),
	}
}

func loadAll(t *testing.T, base string, names ...string) []Root {
	t.Helper()
	var roots []Root
	for _, n := range names {
		r, err := LoadRoot(t.Context(), n, filepath.Join(base, n))
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, r)
	}
	return roots
}

// TestWavesFromTheFixtureConfiguration runs the whole reading on the
// fixture's own configuration: the edges come from the HCL, not from a
// hand-written Read list.
func TestWavesFromTheFixtureConfiguration(t *testing.T) {
	base := writeTree(t, chainFiles())
	roots := loadAll(t, base, "estates/e05", "estates/e03", "estates/e04", "estates/e02", "estates/e01")

	if got := roots[3]; got.Estate != "ls-e02" || !reflect.DeepEqual(got.Reads, []Read{{Estate: "ls-e01", From: "data.aws_vpc.upstream"}}) {
		t.Errorf("e02 read as %+v", got)
	}
	w, err := Split(roots, []string{"ls-e04"})
	if err != nil {
		t.Fatal(err)
	}
	assertReadersAfterProducers(t, roots, w)
	want := [][]string{{"estates/e04"}, {"estates/e01", "estates/e05"}, {"estates/e02"}, {"estates/e03"}}
	if got := waveRoots(w); !reflect.DeepEqual(got, want) {
		t.Errorf("waves %v, want %v", got, want)
	}
}

func TestReadsOfEveryShape(t *testing.T) {
	base := writeTree(t, map[string]string{
		"root/main.tf": `
terraform {
  live { estate = "consumer" }
}
data "aws_vpc" "by_tags" {
  tags = {
    tofu-estate  = "net"
    tofu-address = "aws_vpc.main"
  }
}
data "terraform_estate_outputs" "dns" {
  estate = "dns"
  names  = ["zone"]
}
data "aws_vpc" "two_values" {
  filter {
    name   = "tag:tofu-estate"
    values = ["a", "b"]
  }
}
data "aws_vpc" "not_a_read" {
  filter {
    name   = "tag:Name"
    values = ["net"]
  }
}
module "inner" {
  source = "./inner"
}
`,
		"root/inner/main.tf": `
data "aws_iam_role" "r" {
  filter {
    name   = "tag:tofu-estate"
    values = ["iam"]
  }
}
`,
	})
	r, err := LoadRoot(t.Context(), "root", filepath.Join(base, "root"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Estate != "consumer" {
		t.Errorf("estate %q, want consumer", r.Estate)
	}
	want := []Read{
		{Estate: "net", From: "data.aws_vpc.by_tags"},
		{Estate: "a", From: "data.aws_vpc.two_values"},
		{Estate: "b", From: "data.aws_vpc.two_values"},
		{Estate: "dns", From: "data.terraform_estate_outputs.dns"},
		{Estate: "iam", From: "module.inner.data.aws_iam_role.r"},
	}
	if !reflect.DeepEqual(r.Reads, want) {
		t.Errorf("reads\n%+v\nwant\n%+v", r.Reads, want)
	}
}

// TestReadsRefuseWhatCannotBeTold: a read whose estate is not a literal is
// refused by name, never dropped, because a dropped read can land a reader
// before what it reads.
func TestReadsRefuseWhatCannotBeTold(t *testing.T) {
	cases := map[string]string{
		"a filter value from a variable": `
variable "producer" {
  type    = string
  default = "net"
}
data "aws_vpc" "v" {
  filter {
    name   = "tag:tofu-estate"
    values = [var.producer]
  }
}`,
		"an estate_outputs estate from a variable": `
variable "producer" {
  type    = string
  default = "net"
}
data "terraform_estate_outputs" "o" {
  estate = var.producer
  names  = ["x"]
}`,
		"a tags value from a local": `
locals { producer = "net" }
data "aws_vpc" "v" {
  tags = { "tofu-estate" = local.producer }
}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			base := writeTree(t, map[string]string{
				"r/estate.chdf.hcl": `estate = "consumer"`,
				"r/main.tf":         body,
			})
			_, err := LoadRoot(t.Context(), "r", filepath.Join(base, "r"))
			if err == nil || !strings.Contains(err.Error(), "cannot be told from configuration") {
				t.Fatalf("got %v, want a refusal saying the estate cannot be told", err)
			}
		})
	}
}

func TestLoadRootRefusesAnUninstalledRemoteModule(t *testing.T) {
	base := writeTree(t, map[string]string{
		"r/estate.chdf.hcl": `estate = "consumer"`,
		"r/main.tf": `
module "remote" {
  source  = "example.com/acme/thing/aws"
  version = "1.0.0"
}`,
	})
	_, err := LoadRoot(t.Context(), "r", filepath.Join(base, "r"))
	if err == nil || !strings.Contains(err.Error(), `Module "module.remote" is not called by a local path and is not installed`) {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRootNeedsAnEstate(t *testing.T) {
	base := writeTree(t, map[string]string{"r/main.tf": `resource "aws_sqs_queue" "q" { name = "q" }`})
	_, err := LoadRoot(t.Context(), "r", filepath.Join(base, "r"))
	if err == nil || !strings.Contains(err.Error(), "names no estate") {
		t.Fatalf("got %v", err)
	}
}
