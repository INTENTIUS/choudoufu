// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "regexp"

// knownAsRe matches the provider's own documented-alias note, verbatim
// across every page that carries it in the pinned release (lb.html.markdown,
// lb_listener.html.markdown, lb_listener_certificate.html.markdown,
// lb_listener_rule.html.markdown, lb_target_group.html.markdown,
// lb_target_group_attachment.html.markdown):
//
//	~> **Note:** `aws_alb` is known as `aws_lb`. The functionality is identical.
//
// Anchored on the exact "is known as ... The functionality is identical"
// wording rather than a looser "known as" scan, so unrelated prose that
// happens to contain the phrase (ses_configuration_set.html.markdown's "is
// known as a fresh start", naming no backticked aws_ pair at all) never
// matches - the regex itself excludes it before aliasDeclaredFor's own
// canonical-name check would.
var knownAsRe = regexp.MustCompile("`(aws_[a-z0-9_]+)`\\s+is known as\\s+`(aws_[a-z0-9_]+)`\\.\\s*The functionality is identical\\.")

// aliasDeclaredFor returns the TF type names doc (the page fetched for
// canonical) declares as its own documented alias - every match whose
// second name is canonical itself, so a page that happened to name some
// OTHER type's alias in passing (never observed in the pinned release, but
// not ruled out by the regex alone) is not trusted as a claim about this
// page's own type. This is the generative form of fetch.go's own doc
// comment ("aliases like aws_alb are documented once under their canonical
// name") and sweep's the only caller: a 404 on the alias's own page is
// exactly what licenses cloning the canonical row under the alias's name,
// because the alias has no page of its own to ever disagree with it.
func aliasDeclaredFor(doc, canonical string) []string {
	var out []string
	for _, m := range knownAsRe.FindAllStringSubmatch(doc, -1) {
		alias, target := m[1], m[2]
		if target == canonical && alias != canonical {
			out = append(out, alias)
		}
	}
	return out
}
