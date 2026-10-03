// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/live/waves"
)

// liveWavesFixture writes #1750's chain at N=3 (e01 read by e02, read by
// e03) plus one root reading nothing, and returns the directory holding
// them.
func liveWavesFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"estates/e01/estate.chdf.hcl": `estate = "ls-e01"`,
		"estates/e01/main.tf":         `resource "aws_vpc" "network" { cidr_block = "10.100.0.0/16" }`,
		"estates/e02/estate.chdf.hcl": `estate = "ls-e02"`,
		"estates/e02/main.tf": `
data "aws_vpc" "upstream" {
  filter {
    name   = "tag:tofu-estate"
    values = ["ls-e01"]
  }
}
resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.upstream.id
  cidr_block = "10.100.1.0/24"
}`,
		"estates/e03/estate.chdf.hcl": `estate = "ls-e03"`,
		"estates/e03/main.tf": `
data "aws_subnet" "upstream" {
  filter {
    name   = "tag:tofu-estate"
    values = ["ls-e02"]
  }
}`,
		"estates/e04/estate.chdf.hcl": `estate = "ls-e04"`,
		"estates/e04/main.tf":         `resource "aws_sqs_queue" "q" { name = "q" }`,
		"set.json": `{"roots": [
  {"root": "estates/e01", "estate": "ls-e01", "status": "planned", "plan": {"resource_changes": [{"address": "aws_vpc.network", "change": {"actions": ["create"], "after": {"cidr_block": "10.100.0.0/16"}}}]}},
  {"root": "estates/e02", "estate": "ls-e02", "status": "planned", "plan": {"resource_changes": []}},
  {"root": "estates/e03", "estate": "ls-e03", "status": "planned", "plan": {"resource_changes": []}},
  {"root": "estates/e04", "estate": "ls-e04", "status": "planned", "plan": {"resource_changes": []}}
]}`,
	}
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

func runLiveWaves(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	view, done := testView(t)
	c := &LiveWavesCommand{Meta: Meta{View: view}}
	code := c.Run(args)
	out := done(t)
	return code, out.Stdout(), out.Stderr()
}

func TestLiveWavesCommand(t *testing.T) {
	dir := liveWavesFixture(t)
	t.Chdir(dir)
	roots := []string{"estates/e03", "estates/e01", "estates/e04", "estates/e02"}

	t.Run("text", func(t *testing.T) {
		code, stdout, stderr := runLiveWaves(t, append([]string{"-canary=ls-e04"}, roots...)...)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		for _, want := range []string{
			"4 waves over 4 roots.",
			"Wave 1 (canaries): 1 root\n  estates/e04 (estate ls-e04)\n",
			"Wave 2: 1 root\n  estates/e01 (estate ls-e01)\n",
			"Wave 3: 1 root\n  estates/e02 (estate ls-e02), after estates/e01 via data.aws_vpc.upstream\n",
			"Wave 4: 1 root\n  estates/e03 (estate ls-e03), after estates/e02 via data.aws_subnet.upstream\n",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("one wave, one root per line", func(t *testing.T) {
		code, stdout, stderr := runLiveWaves(t, append([]string{"-wave=1"}, roots...)...)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		if stdout != "estates/e01\nestates/e04\n" {
			t.Errorf("wave 1 printed %q", stdout)
		}
	})

	t.Run("json with digests from the set plan", func(t *testing.T) {
		code, stdout, stderr := runLiveWaves(t, "-json", "-plan-set=set.json")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		var doc struct {
			Waves []struct {
				Wave   int      `json:"wave"`
				Roots  []string `json:"roots"`
				Digest string   `json:"digest"`
			} `json:"waves"`
			Digest      string            `json:"digest"`
			RootDigests map[string]string `json:"root_digests"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("%v\n%s", err, stdout)
		}
		if len(doc.Waves) != 3 || !strings.HasPrefix(doc.Digest, "sha256:") || len(doc.RootDigests) != 4 {
			t.Fatalf("document %+v", doc)
		}
		for _, w := range doc.Waves {
			if !strings.HasPrefix(w.Digest, "sha256:") {
				t.Errorf("wave %d has no digest", w.Wave)
			}
		}
		if doc.Waves[0].Digest == doc.Digest {
			t.Error("wave 1's digest equals the whole set's though wave 1 is a strict subset")
		}
	})

	t.Run("a cycle is refused by name", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "estates/e01/reads.tf"), []byte(`
data "aws_subnet" "back" {
  filter {
    name   = "tag:tofu-estate"
    values = ["ls-e03"]
  }
}`), 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(filepath.Join(dir, "estates/e01/reads.tf"))
		code, _, stderr := runLiveWaves(t, roots...)
		if code != 1 || !strings.Contains(stderr, "cycle") || !strings.Contains(stderr, "data.aws_subnet.back") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
	})

	t.Run("roots that differ from the set plan are refused", func(t *testing.T) {
		code, _, stderr := runLiveWaves(t, "-plan-set=set.json", "estates/e01")
		if code != 1 || !strings.Contains(stderr, "roots differ") || !strings.Contains(stderr, "estates/e04") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
	})
}

// TestWavesEstateOutputsTypeName holds internal/live/waves' copy of the
// estate-outputs data source's type name to the provider's own.
func TestWavesEstateOutputsTypeName(t *testing.T) {
	if waves.EstateOutputsTypeName != tf.EstateOutputsTypeName {
		t.Fatalf("waves reads %q as the estate-outputs data source; the provider names it %q", waves.EstateOutputsTypeName, tf.EstateOutputsTypeName)
	}
}
