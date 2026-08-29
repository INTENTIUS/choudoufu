// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"
)

func TestBucketOf(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"internal/live/lifecycle/marker.go", "internal/live/"},
		{"internal/live", "other"}, // no trailing slash: not under the root
		{"tools/forkdiff-gen/main.go", "tools/"},
		{"live/fork-surface.json", "live/"},
		{"site/content/docs/index.md", "site/"},
		{".github/workflows/ci.yml", ".github/"},
		{"rfc/20260814-upstream-sync.md", "rfc/"},
		{"internal/command/apply.go", "other"},
		{"internal/tofu/context.go", "other"},
		{"cmd/choudoufu/main.go", "other"},
		{"README.md", "other"},
	}
	for _, c := range cases {
		if got := bucketOf(c.path); got != c.want {
			t.Errorf("bucketOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestMechanicalModuleRenameOnly(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{
			name: "single import line renamed",
			old:  "package foo\n\nimport \"github.com/opentofu/opentofu/internal/addrs\"\n",
			new:  "package foo\n\nimport \"github.com/intentius/choudoufu/internal/addrs\"\n",
			want: true,
		},
		{
			name: "import block reordered by goimports after the rename",
			old:  "import (\n\t\"fmt\"\n\n\t\"github.com/opentofu/opentofu/internal/addrs\"\n\t\"github.com/opentofu/opentofu/internal/configs\"\n)\n",
			new:  "import (\n\t\"fmt\"\n\n\t\"github.com/intentius/choudoufu/internal/addrs\"\n\t\"github.com/intentius/choudoufu/internal/configs\"\n)\n",
			want: true,
		},
		{
			// A real example found in internal/initwd/module_install.go:
			// an upstream issue URL in a bare comment (no leading quote)
			// must NOT be rewritten, so a file that kept it unrenamed is
			// mechanical; one where it got rewritten anyway is not.
			name: "comment URL citing an upstream issue is untouched, not mechanical-excluded",
			old:  "import \"github.com/opentofu/opentofu/internal/addrs\"\n\n// see https://github.com/opentofu/opentofu/issues/2117\n",
			new:  "import \"github.com/intentius/choudoufu/internal/addrs\"\n\n// see https://github.com/opentofu/opentofu/issues/2117\n",
			want: true,
		},
		{
			name: "comment URL rewritten too would be a real, non-mechanical change",
			old:  "import \"github.com/opentofu/opentofu/internal/addrs\"\n\n// see https://github.com/opentofu/opentofu/issues/2117\n",
			new:  "import \"github.com/intentius/choudoufu/internal/addrs\"\n\n// see https://github.com/intentius/choudoufu/issues/2117\n",
			want: false,
		},
		{
			name: "a bare, unquoted occurrence (a build-flag string value, not an import) is not rewritten by the filter",
			old:  "args = append(args, \"-coverpkg=github.com/opentofu/opentofu/...\")\n",
			new:  "args = append(args, \"-coverpkg=github.com/intentius/choudoufu/...\")\n",
			want: false,
		},
		{
			name: "a real logic change alongside the rename is not mechanical",
			old:  "import \"github.com/opentofu/opentofu/internal/addrs\"\n\nfunc F() int { return 1 }\n",
			new:  "import \"github.com/intentius/choudoufu/internal/addrs\"\n\nfunc F() int { return 2 }\n",
			want: false,
		},
		{
			name: "identical content (no rename needed) is mechanical",
			old:  "package foo\n",
			new:  "package foo\n",
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mechanicalModuleRenameOnly([]byte(c.old), []byte(c.new))
			if got != c.want {
				t.Errorf("mechanicalModuleRenameOnly() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLineMultisetEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "a\nb\nc\n", "a\nb\nc\n", true},
		{"reordered", "a\nb\nc\n", "c\na\nb\n", true},
		{"different line count", "a\nb\n", "a\nb\nc\n", false},
		{"same count, different content", "a\nb\nc\n", "a\nb\nd\n", false},
		{"duplicate lines matter", "a\na\nb\n", "a\nb\nb\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lineMultisetEqual([]byte(c.a), []byte(c.b)); got != c.want {
				t.Errorf("lineMultisetEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
