// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// justRecipeWithParams is ci_coverage_test.go's justRecipe for a recipe whose
// header carries parameters (`verify bucket="":`), which that one's exact
// `name:` match does not find. It returns every line from the header to the
// next line that starts in column zero.
func justRecipeWithParams(t *testing.T, justfile, name string) string {
	t.Helper()
	lines := strings.Split(justfile, "\n")
	header := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `( [^:]*)?:`)
	var body []string
	in := false
	for _, line := range lines {
		switch {
		case header.MatchString(line):
			in = true
		case in && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			return strings.Join(body, "\n")
		}
		if in {
			body = append(body, line)
		}
	}
	if !in {
		t.Fatalf("no recipe %q in the justfile", name)
	}
	return strings.Join(body, "\n")
}

// TestRecordStoreBucketVerifyAsksTheBinary holds GitHub issue #1341's first
// rule. Two codebases checking the same three settings drift, and the one
// that drifts is the one nobody runs - the first version of this project
// checked them in bash and had already drifted (it demanded SSE-KMS, which
// the tool does not assert). So `verify` calls `choudoufu live-bucket`, and
// reads none of the three settings itself.
func TestRecordStoreBucketVerifyAsksTheBinary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "examples", "record-store-bucket", "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	verify := justRecipeWithParams(t, string(raw), "verify")
	if !strings.Contains(verify, "live-bucket") {
		t.Error("`just verify` does not call `choudoufu live-bucket`")
	}
	for _, reimplementation := range []string{"get-bucket-versioning", "get-bucket-lifecycle", "get-public-access-block"} {
		if strings.Contains(verify, reimplementation) {
			t.Errorf("`just verify` reads %s itself: that is a second implementation of a check the binary owns", reimplementation)
		}
	}

	// `down` counts VERSIONS under the record roots, not current objects. A
	// deleted record is a delete marker over a recoverable noncurrent
	// version, and a count of current objects reads that bucket as empty.
	down := justRecipeWithParams(t, string(raw), "down")
	if !strings.Contains(down, "list-object-versions") || !strings.Contains(down, `--prefix "tofu-"`) {
		t.Error("`just down` does not count object versions under tofu-* before tearing the bucket down")
	}
	// The COUNT expressions, not the bare words: `down` also names Versions
	// and DeleteMarkers where it cleans up verify's probe objects, so
	// matching the words alone passed with the delete-marker count removed.
	for _, counted := range []string{"length(Versions", "length(DeleteMarkers"} {
		if !strings.Contains(down, counted) {
			t.Errorf("`just down`'s refusal does not count %s...): a bucket holding only deleted records would read as empty", counted)
		}
	}
}
