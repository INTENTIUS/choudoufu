// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// This file is issue #578's guard, and it exists because the claim
// "terralith-gen emits canonical HCL" was true of the root module and false
// of the module subdirectory for the whole life of #574, with nothing able
// to notice.
//
// The generator formats its own output by shelling out to terraform/tofu
// (main.go's formatWithBinary). That call had two holes: it omitted
// -recursive, so modules/team_pod/*.tf - the only files #574 added below the
// root - were never formatted, and it discarded the exit code, so the free
// parse `terraform fmt` performs on the way was thrown away.
//
// The checks below are deliberately layered by what they need:
//
//   - unformattedGeneratedFiles needs nothing but the process it runs in. It
//     is the fast-tier check, it runs on every ordinary `go test ./tools/...`
//     with no binary, no network and no environment variable, and it is the
//     one that would have caught #578's defect 1 on the commit that
//     introduced it.
//   - the fmt-binary subtest is the acceptance command from the issue
//     verbatim, run when a binary is present. It is the belt to the fast
//     check's braces: the same oracle, computed by the real tool.
//
// The teeth test underneath feeds the first check main's own pre-fix
// rendering and requires it to fail on it, because a formatting check
// written from the formatter's own output passes forever.

// unformattedGeneratedFiles names every generated file whose bytes are not
// already what the HCL formatter would produce, sorted.
//
// hclwrite.Format is the same function `terraform fmt` and `tofu fmt` are
// built on (internal/command/fmt.go's formatSourceCode parses with
// hclwrite and returns f.Bytes(), which formats), so "canonical" here means
// the same thing it means at the command line for the whitespace and
// alignment rules that are the entire subject of #578.
//
// Only *.tf is considered: GENERATED.md is Markdown and no formatter of
// this kind has an opinion about it.
func unformattedGeneratedFiles(dir string) ([]string, error) {
	var bad []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".tf") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path this test just generated
		if err != nil {
			return err
		}
		if string(hclwrite.Format(src)) != string(src) {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				rel = path
			}
			bad = append(bad, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(bad)
	return bad, nil
}

// TestGeneratedTerralithIsCanonicallyFormatted is #578's acceptance bullet
// "terraform fmt -check -recursive returns 0 on freshly generated output at
// scale 1 and 4", asserted on output no formatter has been pointed at since
// generation.
//
// That last clause is not pedantry. `terraform fmt -diff` REWRITES the files
// it reports on; only -check is read-only. A run of this check against a
// directory somebody has already inspected with -diff passes unconditionally
// and says nothing, which is how the defect survived being looked at. Each
// subtest here generates into its own fresh t.TempDir() and never invokes a
// writing form.
func TestGeneratedTerralithIsCanonicallyFormatted(t *testing.T) {
	for _, scale := range []int{1, 4} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			if err := buildEstate(scale, "tl").write(out); err != nil {
				t.Fatal(err)
			}

			bad, err := unformattedGeneratedFiles(out)
			if err != nil {
				t.Fatalf("walking the generated estate: %v", err)
			}
			if len(bad) > 0 {
				t.Errorf("the generator emitted %d file(s) the HCL formatter would rewrite: %s\n"+
					"Generated output is meant to be canonical stock Terraform; run `terraform fmt -diff -recursive` on a THROWAWAY copy to see what differs, and fix the template, not the output.",
					len(bad), strings.Join(bad, ", "))
			}
		})
	}
}

// TestUnformattedGeneratedFilesHasTeeth feeds the check main's own pre-#578
// rendering of modules/team_pod/main.tf - the exact bytes `terraform fmt
// -check -recursive` flagged at f4611196e5 - and requires it to report it.
//
// Without this control, TestGeneratedTerralithIsCanonicallyFormatted is a
// check written from the implementation: it compares the generator's output
// against a formatter and would pass just as happily if the walk matched no
// files at all, or if hclwrite.Format were the identity function.
func TestUnformattedGeneratedFilesHasTeeth(t *testing.T) {
	// Verbatim from the pre-fix generator: `count` and `name` padded to the
	// width of `assume_role_policy`, which the formatter does not do because
	// the multi-line jsonencode ends the alignment group before it.
	broken := `resource "aws_iam_role" "pod_role" {
  count              = var.pod_size
  name               = "${var.prefix}-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
  })
}
`

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modules", "team_pod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules", "team_pod", "main.tf"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	// A canonical file next to it, in the root, so a check that flagged
	// everything would not pass this test either.
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource \"aws_iam_role\" \"r\" {\n  name = \"x\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad, err := unformattedGeneratedFiles(dir)
	if err != nil {
		t.Fatalf("walking the fixture: %v", err)
	}
	want := []string{"modules/team_pod/main.tf"}
	if strings.Join(bad, ",") != strings.Join(want, ",") {
		t.Errorf("unformattedGeneratedFiles on the known-unformatted pre-#578 rendering returned %v, want %v.\n"+
			"If this is empty, the formatting check cannot fail and proves nothing; if it names main.tf too, it flags canonical files.",
			bad, want)
	}
}

// TestFmtBinaryAgreesTheOutputIsCanonical runs the issue's own reproduce
// command - `<bin> fmt -check -recursive` - against freshly generated output,
// when a formatter binary is on PATH.
//
// Gated on the binary's presence, never on TF_ACC or TF_FLOCI_TEST: an
// env-var gate is what left TestValidateGeneratedTerralith unexecuted in
// every automated run for the whole life of this package (#578's defect 3),
// and repeating it here would leave this check in the same state.
//
// -check, and only -check. The -diff form writes.
func TestFmtBinaryAgreesTheOutputIsCanonical(t *testing.T) {
	bin := ""
	for _, candidate := range []string{"terraform", "tofu"} {
		if _, err := exec.LookPath(candidate); err == nil {
			bin = candidate
			break
		}
	}
	if bin == "" {
		t.Skip("neither terraform nor tofu is on PATH; the in-process check in TestGeneratedTerralithIsCanonicallyFormatted covers the same ground without one")
	}

	for _, scale := range []int{1, 4} {
		t.Run(fmt.Sprintf("scale=%d", scale), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "terralith")
			if err := buildEstate(scale, "tl").write(out); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(bin, "fmt", "-check", "-recursive", "-no-color", ".") //nolint:gosec // a binary this test just found on PATH
			cmd.Dir = out
			combined, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("%s fmt -check -recursive on fresh scale=%d output: %v\nfiles it would rewrite:\n%s", bin, scale, err, combined)
			}
		})
	}
}

// TestFormatWithBinaryReportsAParseFailure is the red-and-green proof for
// #578's defect 2: main.go's format call used to be `_ = cmd.Run()`, so a
// `terraform fmt` that exited 2 with "Error: Invalid expression" over HCL
// this tool had just written looked exactly like a successful format.
//
// The broken file is placed in a SUBDIRECTORY on purpose. That is the same
// assertion as defect 1 seen from the other side: without -recursive the
// formatter never opens modules/team_pod at all, so this subtest fails if
// either half of the fix is reverted.
func TestFormatWithBinaryReportsAParseFailure(t *testing.T) {
	bin := ""
	for _, candidate := range []string{"terraform", "tofu"} {
		if _, err := exec.LookPath(candidate); err == nil {
			bin = candidate
			break
		}
	}
	if bin == "" {
		t.Skip("neither terraform nor tofu is on PATH; this test drives a real formatter binary")
	}

	write := func(t *testing.T, dir, rel, content string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	const good = "resource \"aws_iam_role\" \"r\" {\n  name = \"x\"\n}\n"
	// Unparseable, not merely unformatted: an operator with no right operand.
	const bad = "resource \"aws_iam_role\" \"r\" {\n  name = \"x\" +\n}\n"

	t.Run("red/nested unparseable file fails the generation", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "main.tf", good)
		write(t, dir, "modules/team_pod/main.tf", bad)

		err := formatWithBinary(bin, dir)
		if err == nil {
			t.Fatal("formatWithBinary returned nil over a module subdirectory holding unparseable HCL.\n" +
				"Either the exit code is being discarded again (#578 defect 2) or -recursive is gone (#578 defect 1); both make this tool ship HCL nothing has parsed.")
		}
		if !strings.Contains(err.Error(), "Invalid expression") {
			t.Errorf("the error does not carry the formatter's own diagnostic, so a caller cannot see which file or line is at fault: %v", err)
		}
		if !strings.Contains(err.Error(), "team_pod") {
			t.Errorf("the error does not name the offending file: %v", err)
		}
	})

	t.Run("green/canonical tree succeeds", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "main.tf", good)
		write(t, dir, "modules/team_pod/main.tf", good)

		if err := formatWithBinary(bin, dir); err != nil {
			t.Fatalf("formatWithBinary over a canonical tree returned %v, want nil - a check that fails on everything is not a check either", err)
		}
	})

	t.Run("a binary that is not on PATH stays non-fatal", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "modules/team_pod/main.tf", bad)

		if err := formatWithBinary("terralith-gen-no-such-formatter", dir); err != nil {
			t.Fatalf("formatWithBinary with an absent binary returned %v, want nil - formatting is a convenience and must not become a hard dependency of generating an estate", err)
		}
	})

	t.Run("an empty -fmt-bin skips formatting", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "modules/team_pod/main.tf", bad)

		if err := formatWithBinary("", dir); err != nil {
			t.Fatalf("formatWithBinary with -fmt-bin=\"\" returned %v, want nil", err)
		}
	})
}
