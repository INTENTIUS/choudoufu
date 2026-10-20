// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// GitHub issue #1601: the aws and kubernetes hubs carry the same page names
// (site/src/content/docs/{aws,kubernetes} since the Astro cutover, #2092;
// site/content/{aws,kubernetes} before it), but before this test existed nothing held their `##`
// outlines to each other. adopt.md ran 4 AWS sections against 2 on
// Kubernetes, operate.md ran 6 against 4 in a different order plus an extra
// Kubernetes-only "Records" section, and so on: a reader who read one
// substrate's page could not tell whether a missing section meant "does not
// apply" or "nobody wrote it yet".
//
// Both pages in a pair must now carry the same `##` headings, in the same
// order. Where a section does not apply to one substrate, that page still
// carries the heading, with a line saying why not, rather than staying
// silent. A `###` subheading is not part of the outline this test holds
// equal, so page-specific detail (Kubernetes' Records section, for example)
// can still nest under a shared `##` heading without needing an AWS-side
// counterpart.
//
// Proving it red: rename one `##` heading on one side of a pair, and its
// outline stops matching its pair's.
var siteOutlinePairs = []string{
	"index",
	"adopt",
	"compatibility",
	"gate",
	"operate",
	"proof",
}

// level2Headings returns a Markdown file's `##` headings, in file order,
// text only. A `###` (or deeper) heading is not returned, so nesting
// page-specific detail under a shared `##` section does not affect this.
func level2Headings(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var headings []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	return headings
}

func TestAWSAndKubernetesPagesShareAnOutline(t *testing.T) {
	found := 0
	for _, name := range siteOutlinePairs {
		awsPath := sitePage("aws/" + name)
		if awsPath == "" {
			t.Fatalf("no aws/%s.md or .mdx under %s", name, siteContentDir)
		}
		awsHeadings := level2Headings(t, awsPath)
		// The Azure hub (#1120) joins the pair under the same rule: every
		// other hub's page is held to the AWS page's outline.
		for _, hub := range []string{"kubernetes", "azure"} {
			path := sitePage(hub + "/" + name)
			if path == "" {
				t.Fatalf("no %s/%s.md or .mdx under %s", hub, name, siteContentDir)
			}
			found++
			if got := level2Headings(t, path); !reflect.DeepEqual(awsHeadings, got) {
				t.Errorf("%s and %s carry different ## outlines:\n  aws: %v\n  %s: %v",
					awsPath, path, awsHeadings, hub, got)
			}
		}
	}
	if found == 0 {
		t.Fatalf("checked no page pairs; siteOutlinePairs or siteContentDir is wrong, which would make this test pass for ever")
	}
}
