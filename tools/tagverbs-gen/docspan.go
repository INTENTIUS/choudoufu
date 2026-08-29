// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

// This file renders the tagging half of the docs site's permissions section
// (issue #143). The rest of that section is a fixed handful of calls this
// fork makes directly, which is hand-written beside a file reference; the
// tagging verbs move with botocore across a service roster large enough
// (spanTagVerbsTotal below) that it is a span rather than a sentence someone
// edits.

var markers = mdspan.For("tagverbs-gen")

const (
	referenceMDRel = "site/content/docs/use/reference.md"
	spanTagVerbs   = "tag-verbs"

	// spanTagVerbsTotal is the inline span naming how many services the
	// roster covers, in the lead-in sentence above the table. Issue #421:
	// this used to be a hand-typed "205" next to a table that already
	// carries len(rows) as len(rows)-noVerb plus noVerb; rendering it here
	// means a botocore-driven change to the roster size can no longer drift
	// from the sentence that quotes it.
	spanTagVerbsTotal = "tag-verbs-total"
)

// applyTagVerbSpans writes both of this generator's site/content/docs/use/
// reference.md spans into md, in one place, so renderTagVerbSpan and
// docspan_test.go's drift guard render exactly the same bytes.
func applyTagVerbSpans(md string, rows []Row) (string, error) {
	out, err := markers.Replace(referenceMDRel, md, spanTagVerbs, renderTagVerbTable(rows))
	if err != nil {
		return "", err
	}
	out, err = markers.ReplaceInline(referenceMDRel, out, spanTagVerbsTotal, renderTagVerbTotal(rows))
	if err != nil {
		return "", err
	}
	return out, nil
}

// renderTagVerbTotal is spanTagVerbsTotal's body: the roster size, the same
// count renderTagVerbTable's own closing sentence already sums back from its
// two halves (services with an unambiguous verb, plus services with none).
func renderTagVerbTotal(rows []Row) string {
	return fmt.Sprintf("%d", len(rows))
}

// renderTagVerbSpan writes the roster of distinct tagging operations, and
// the roster's total size, into site/content/docs/use/reference.md.
//
// Operations rather than services in the table: an operator writing a role
// needs the action names, and the service rows collapse to a handful of
// verbs because AWS standardized on TagResource. Naming that collapse is the
// useful thing - it says most of the roster is one action - and the long
// tail is where the surprises are.
func renderTagVerbSpan(root string, rows []Row) error {
	path := filepath.Join(root, referenceMDRel)
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", referenceMDRel, err)
	}

	out, err := applyTagVerbSpans(string(doc), rows)
	if err != nil {
		return err
	}
	if out == string(doc) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644) //nolint:gosec // a committed doc
}

// runRender is the -render entry point (issue #421, mirroring
// tools/survey-gen's own -render mode): rewrite reference.md's spans from
// the already-committed live/tag-verbs.json artifact, with no network. Kept
// so a doc-only drift (someone hand-edits the sentence, or the artifact
// changes without `just tagverbs` having been rerun) can be repaired without
// the botocore fetch run() needs.
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tagVerbsJSONRel))) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s (regenerate with `go run ./tools/tagverbs-gen`): %w", tagVerbsJSONRel, err)
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		return fmt.Errorf("decoding %s: %w", tagVerbsJSONRel, err)
	}
	return renderTagVerbSpan(root, art.Rows)
}

// renderTagVerbTable builds the span's body from rows alone - no file I/O -
// so docspan_test.go's drift guard can render the same bytes renderTagVerbSpan
// would write without touching the filesystem.
func renderTagVerbTable(rows []Row) string {
	byOp := map[string][]string{}
	var noVerb int
	for _, r := range rows {
		if r.Operation == "" {
			noVerb++
			continue
		}
		byOp[r.Operation] = append(byOp[r.Operation], r.Service)
	}
	ops := make([]string, 0, len(byOp))
	for op := range byOp {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool {
		if len(byOp[ops[i]]) != len(byOp[ops[j]]) {
			return len(byOp[ops[i]]) > len(byOp[ops[j]])
		}
		return ops[i] < ops[j]
	})

	var b strings.Builder
	b.WriteString("| Action | Services |\n|---|---|\n")
	for _, op := range ops {
		svcs := byOp[op]
		sort.Strings(svcs)
		shown := svcs
		suffix := ""
		if len(shown) > 6 {
			shown, suffix = shown[:6], fmt.Sprintf(" and %d more", len(svcs)-6)
		}
		fmt.Fprintf(&b, "| `%s` | %d. %s%s |\n", op, len(svcs), strings.Join(shown, ", "), suffix)
	}
	fmt.Fprintf(&b, "\n%d services carry an unambiguous tagging verb. %d do not, and a run cannot stamp a marker on those.\n",
		len(rows)-noVerb, noVerb)

	return b.String()
}
