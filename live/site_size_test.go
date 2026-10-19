// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #1414 took the docs site from about 98,000 words to under
// 25,000 by moving evidence and measurement into the repository, beside the
// code that produces them, and leaving short pages behind. #2092 rebuilt it
// on Astro + Starlight and cut it to the reader path, under 10,000. Nothing
// stopped the site reaching 98,000 the first time: every page was added for
// a good reason, one at a time. This test is the thing that says no.
//
// It counts words the way `wc -w` does, over every Markdown and MDX file
// under site/src/content/docs, front matter and code blocks included,
// because that is the number the issues were measured in and the one a
// contributor can reproduce in a shell:
//
//	find site/src/content/docs -name '*.md*' | xargs cat | wc -w
//
// The home page's picker and the rooms under /for/ are written in
// site/src/data/rooms.ts, not in a page, so their text is counted too: the
// words of every string literal in that file that contains a space. A label
// or a sentence has a space; an href, an id or an import path does not, so
// this counts what a reader sees and none of the code around it.
//
// When it fails, the fix is almost never to raise the limit. Move the detail
// to a file under live/ or examples/ and link to it, the way the issues did.

const (
	siteWordLimit = 10000
	// sitePathPageLimit bounds each page a newcomer reads between the home
	// page and a first apply.
	sitePathPageLimit = 800
)

// siteNewcomerPath is relative to siteContentDir.
var siteNewcomerPath = []string{
	"how-it-works/index.md",
	"docs/tutorial.md",
	"docs/use/start.md",
	"docs/use/setup.md",
	"docs/use/migrate.md",
}

// roomsStringLiteral matches a single-quoted, double-quoted or template
// string literal on one line (a template literal may span lines).
var roomsStringLiteral = regexp.MustCompile("'((?:[^'\\\\\\n]|\\\\.)*)'|\"((?:[^\"\\\\\\n]|\\\\.)*)\"|`([^`]*)`")

// roomsProseWords counts the words of every string literal in rooms.ts
// that contains a space, skipping whole-line comments.
func roomsProseWords(src string) int {
	var code []string
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code = append(code, line)
		}
	}
	n := 0
	for _, m := range roomsStringLiteral.FindAllStringSubmatch(strings.Join(code, "\n"), -1) {
		lit := m[1] + m[2] + m[3]
		if strings.Contains(strings.TrimSpace(lit), " ") {
			n += len(strings.Fields(lit))
		}
	}
	return n
}

func TestSiteStaysShort(t *testing.T) {
	total := 0
	perFile := map[string]int{}
	err := filepath.WalkDir(siteContentDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".mdx")) {
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
	rooms, err := os.ReadFile(siteRoomsTS)
	if err != nil {
		t.Fatal(err)
	}
	roomWords := roomsProseWords(string(rooms))
	if roomWords == 0 {
		t.Fatalf("counted no words in %s's strings; the count is broken, which would make this test pass for ever", siteRoomsTS)
	}
	t.Logf("site: %d words (%d in site/src/content/docs, %d in rooms.ts), limit %d", total+roomWords, total, roomWords, siteWordLimit)
	if total+roomWords > siteWordLimit {
		t.Errorf("the site is %d words (%d in site/src/content/docs, %d in rooms.ts), over the %d #2092 set. Move detail into a file under live/ or examples/ and link to it; do not raise the limit to fit a page.", total+roomWords, total, roomWords, siteWordLimit)
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
