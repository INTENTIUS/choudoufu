// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
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
// tagging verbs are 205 services deep and move with botocore, so they are a
// span.

var markers = mdspan.For("tagverbs-gen")

const (
	referenceMDRel = "site/content/reference.md"
	spanTagVerbs   = "tag-verbs"
)

// renderTagVerbSpan writes the roster of distinct tagging operations into
// site/content/reference.md.
//
// Operations rather than services: an operator writing a role needs the
// action names, and 205 service rows collapse to a handful of verbs because
// AWS standardized on TagResource. Naming that collapse is the useful thing
// - it says most of the roster is one action - and the long tail is where
// the surprises are.
func renderTagVerbSpan(root string, rows []Row) error {
	path := filepath.Join(root, referenceMDRel)
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", referenceMDRel, err)
	}

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

	out, err := markers.Replace(referenceMDRel, string(doc), spanTagVerbs, b.String())
	if err != nil {
		return err
	}
	if out == string(doc) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644) //nolint:gosec // a committed doc
}
