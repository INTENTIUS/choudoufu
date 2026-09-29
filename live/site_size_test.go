// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub issue #1414 took the docs site from about 98,000 words to under
// 25,000 by moving evidence and measurement into the repository, beside the
// code that produces them, and leaving short pages behind. Nothing stopped
// the site reaching 98,000 the first time: every page was added for a good
// reason, one at a time. This test is the thing that says no.
//
// It counts words the way `wc -w` does, over every Markdown file under
// site/content, front matter and code blocks included, because that is the
// number the issue was measured in and the one a contributor can reproduce
// in a shell.
//
// When it fails, the fix is almost never to raise the limit. Move the detail
// to a file under live/ or examples/ and link to it, the way the issue did.

const (
	siteWordLimit = 25000
	// sitePathPageLimit bounds each page a newcomer reads between the home
	// page and a first apply.
	sitePathPageLimit = 800
)

var siteNewcomerPath = []string{
	"docs/model/_index.md",
	"docs/tutorial.md",
	"docs/use/start.md",
	"docs/use/setup.md",
	"docs/use/migrate.md",
}

func TestSiteStaysShort(t *testing.T) {
	total := 0
	perFile := map[string]int{}
	err := filepath.WalkDir(siteContentDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		n := len(strings.Fields(string(raw)))
		rel, _ := filepath.Rel(siteContentDir, p)
		perFile[filepath.ToSlash(rel)] = n
		total += n
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatalf("counted no words under %s; the walk found nothing, which would make this test pass for ever", siteContentDir)
	}
	if total > siteWordLimit {
		t.Errorf("site/content is %d words, over the %d #1414 set. Move detail into a file under live/ or examples/ and link to it; do not raise the limit to fit a page.", total, siteWordLimit)
	}
	for _, rel := range siteNewcomerPath {
		n, ok := perFile[rel]
		if !ok {
			t.Errorf("%s is on the newcomer path and was not found; if it moved, update siteNewcomerPath", rel)
			continue
		}
		if n > sitePathPageLimit {
			t.Errorf("%s is %d words, over %d. It is on the path from the home page to a first apply, and #1414's bar is that nobody has to read a long page to get there.", rel, n, sitePathPageLimit)
		}
	}
}
