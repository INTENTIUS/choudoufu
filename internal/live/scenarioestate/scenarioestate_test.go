// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package scenarioestate

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// TestRenderWritesAStockEstateOfScaleCopies renders the default tree at
// scale 3 and holds the output to the package doc: no marker, no trace of
// the tree's own name, every label carrying its copy's suffix, and every
// reference in a copy pointing inside that copy.
func TestRenderWritesAStockEstateOfScaleCopies(t *testing.T) {
	out := filepath.Join(t.TempDir(), "estate")
	s, err := Render(Options{RepoRoot: repoRoot(t), Scale: 3, Prefix: "ut", Out: out})
	if err != nil {
		t.Fatal(err)
	}
	// 39 blocks in live/e2e/estate; aws_eip.pool counts 3, aws_subnet.this
	// and aws_route_table_association.this are two each, and the
	// conditional log group is on by default.
	if s.PerCopy != 43 || s.Resources != 129 {
		t.Errorf("summary %s; want 43 per copy and 129 at scale 3. If the tree changed on purpose, change this number with it", s)
	}
	if s.Taggable <= 0 || s.Taggable >= s.Resources {
		t.Errorf("taggable %d of %d; some, not all, of the tree carries tags", s.Taggable, s.Resources)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`\b(aws_[a-z0-9_]+)\.([a-z0-9_]+_(\d{4}))\b`)
	anyRef := regexp.MustCompile(`\b(aws_[a-z0-9_]+)\.([a-z_][a-z0-9_]*)\.`)
	labels := map[string]bool{}
	var bodies []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(out, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		bodies = append(bodies, text)
		if markerLine.MatchString(text) {
			t.Errorf("%s carries an ownership marker; a render is a stock estate", e.Name())
		}
		f, diags := hclsyntax.ParseConfig(src, e.Name(), hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s does not parse: %s", e.Name(), diags.Error())
		}
		code := stripComments(text)
		if strings.Contains(code, TreeEstateName) {
			t.Errorf("%s still names %q outside a comment", e.Name(), TreeEstateName)
		}
		for _, b := range f.Body.(*hclsyntax.Body).Blocks {
			if b.Type != "resource" {
				continue
			}
			if !regexp.MustCompile(`_\d{4}$`).MatchString(b.Labels[1]) {
				t.Errorf("%s: resource %s.%s carries no copy suffix", e.Name(), b.Labels[0], b.Labels[1])
			}
			labels[b.Labels[0]+"."+b.Labels[1]] = true
		}
	}
	if len(labels) != 39*3 {
		t.Errorf("%d resource blocks, want %d", len(labels), 39*3)
	}
	for _, text := range bodies {
		for _, block := range strings.Split(stripComments(text), "\nresource ")[1:] {
			own := regexp.MustCompile(`^"[a-z0-9_]+" "[a-z0-9_]+_(\d{4})"`).FindStringSubmatch(block)
			if own == nil {
				continue
			}
			for _, m := range anyRef.FindAllStringSubmatch(block, -1) {
				if !regexp.MustCompile(`_\d{4}$`).MatchString(m[2]) {
					t.Errorf("copy %s refers to %s.%s, which no render declares (a reference the render did not rewrite)", own[1], m[1], m[2])
				}
			}
			for _, m := range ref.FindAllStringSubmatch(block, -1) {
				if !labels[m[1]+"."+m[2]] {
					t.Errorf("a reference to %s.%s names no rendered resource", m[1], m[2])
				}
				if m[3] != own[1] {
					t.Errorf("copy %s refers to %s.%s in copy %s", own[1], m[1], m[2], m[3])
				}
			}
		}
	}

	versions, err := os.ReadFile(filepath.Join(out, "versions.tf"))
	if err != nil {
		t.Fatal(err)
	}
	v := string(versions)
	anchor := `required_version = ">= 1.5.0"`
	provider := `provider "aws" {`
	if !strings.Contains(v, anchor) || !strings.Contains(v, provider) || strings.Index(v, anchor) > strings.Index(v, provider) {
		t.Errorf("versions.tf does not put %q in the terraform block ahead of %q:\n%s", anchor, provider, v)
	}
	if strings.Contains(v[strings.Index(v, provider):], "terraform {") {
		t.Error("versions.tf has a terraform block after the provider block; callers cut from the provider line to the end")
	}
	if _, err := os.Stat(filepath.Join(out, ".terraform.lock.hcl")); err != nil {
		t.Errorf("the tree's lock file was not copied: %v", err)
	}
	if want := "ut-0002"; !strings.Contains(strings.Join(bodies, ""), `"tofu-`+want+`-app"`) {
		t.Errorf("no copy is named %q", want)
	}
}

func stripComments(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestCountMatchesRender: Count says what Render writes, without writing.
func TestCountMatchesRender(t *testing.T) {
	c, err := Count(repoRoot(t), "", 5)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Render(Options{RepoRoot: repoRoot(t), Scale: 5, Prefix: "cm", Out: filepath.Join(t.TempDir(), "e")})
	if err != nil {
		t.Fatal(err)
	}
	if c != r {
		t.Errorf("Count = %+v, Render = %+v", c, r)
	}
}

// TestRenderRefuses: the inputs a render cannot write a working estate from.
func TestRenderRefuses(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct {
		name string
		o    Options
	}{
		{"scale 0", Options{RepoRoot: root, Scale: 0, Prefix: "ok", Out: t.TempDir()}},
		{"an upper-case prefix", Options{RepoRoot: root, Scale: 1, Prefix: "RT", Out: t.TempDir()}},
		{"a prefix with an underscore", Options{RepoRoot: root, Scale: 1, Prefix: "a_b", Out: t.TempDir()}},
		{"a tree with no versions.tf", Options{Tree: t.TempDir(), Scale: 1, Prefix: "ok", Out: t.TempDir()}},
	} {
		if _, err := Render(tc.o); err == nil {
			t.Errorf("%s: rendered", tc.name)
		}
	}
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(Options{RepoRoot: root, Scale: 1, Prefix: "ok", Out: full}); err == nil {
		t.Error("rendered into a directory that was not empty")
	}
}
