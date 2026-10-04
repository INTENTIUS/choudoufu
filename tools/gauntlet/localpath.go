// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path"
	"regexp"
	"strings"
)

// A stage detail is committed evidence: it lands in live/gauntlet.json and
// live/gauntlet-scale.json, and downstream ingests publish it verbatim
// (#1083). One machine's temporary directory is not evidence anyone else
// can follow. The aws/scale=50 record's test_plan detail read "see
// /private/tmp/claude-501/.../scratchpad/scale50b/work/test_plan.out", a
// path that stopped existing when that session ended, and an ingest
// published it as the row's reason.
//
// localTempPath matches an absolute path under a directory that is
// temporary by construction: /tmp and /var/tmp (and macOS's /private
// aliases of both), macOS's per-user $TMPDIR under /var/folders, and a
// GitHub runner's RUNNER_TEMP. A path elsewhere - a repository-relative
// one, or a log under live/gauntlet/logs/ - is left alone, because it can
// still be followed. The path must start the string or follow a space,
// quote, bracket, '=' or ':', so "live/e2e/x/tmp/y" - a relative path that
// happens to contain a directory named tmp - is not mistaken for one; that
// leading character is part of the match and is put back unchanged.
var localTempPath = regexp.MustCompile(`(?:^|[\s'"(\[=:])(?:/private)?/(?:tmp|var/tmp|var/folders|home/runner/work/_temp)/[^\s'"<>()\[\]]*`)

// localTempPlaceholder is what a scrubbed path's directory becomes. The
// file's own name is kept, since "test_plan.out" still tells a reader which
// output the sentence meant, and the placeholder says plainly that the rest
// was a local path rather than leaving a reader to wonder what was cut.
const localTempPlaceholder = "<local temp path>"

// scrubLocalPaths rewrites every local temporary path in s to
// "<local temp path>/<base name>". Trailing sentence punctuation is not
// part of the path. A string with no such path comes back unchanged.
func scrubLocalPaths(s string) string {
	if !strings.Contains(s, "/") {
		return s
	}
	return localTempPath.ReplaceAllStringFunc(s, func(p string) string {
		lead := ""
		if p != "" && p[0] != '/' {
			lead, p = p[:1], p[1:]
		}
		trail := ""
		for len(p) > 0 && strings.ContainsRune(".,;:!?", rune(p[len(p)-1])) {
			trail = p[len(p)-1:] + trail
			p = p[:len(p)-1]
		}
		base := path.Base(strings.TrimRight(p, "/"))
		switch base {
		case "", ".", "/", "tmp", "private", "folders":
			return lead + localTempPlaceholder + trail
		}
		return lead + localTempPlaceholder + "/" + base + trail
	})
}
