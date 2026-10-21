// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// emptySegment reports whether placeholder name of the id format renders
// as an empty segment when the configuration leaves it unset, so that an
// instance without it still has stock's import ID rather than a refusal
// (#2145 item 6, ruled in issuecomment-6305339248). Both halves have to
// hold:
//
//   - The argument is optional with no schema default (flag "O", not
//     computed, and neither a string nor any other Default in its schema
//     field). ReplaceVars then substitutes "" for it: nothing else fills
//     it in.
//   - Every import format of every provider captures it with `[^/]*`, so
//     the id stock writes with the empty segment parses back. A format
//     that captures it with `[^/]+` (google_spanner_backup_schedule's
//     name, which the create mints when unset) or does not capture it at
//     all rules it out.
//
// google_compute_global_network_endpoint and
// google_compute_region_network_endpoint qualify for ip_address and fqdn,
// google_compute_network_endpoint for instance.
func emptySegment(d Decision, src Source, flag, name string) bool {
	if flag != "O" {
		return false
	}
	if _, ok := src.Defaults[name]; ok || containsStr(src.OtherDefaults, name) {
		return false
	}
	if len(d.importFormats) == 0 {
		return false
	}
	want := "(?P<" + name + ">[^/]*)"
	group := regexp.MustCompile(`\(\?P<` + regexp.QuoteMeta(name) + `>`)
	for _, formats := range d.importFormats {
		if len(formats) == 0 {
			return false
		}
		for _, f := range formats {
			locs := group.FindAllStringIndex(f, -1)
			if len(locs) != 1 || len(f) < locs[0][0]+len(want) || f[locs[0][0]:locs[0][0]+len(want)] != want {
				return false
			}
		}
	}
	return true
}

// attachAtLeastOneOf copies the schema's AtLeastOneOf group onto every
// empty-segment component of the row that belongs to it, so that an
// instance setting none of the group refuses instead of rendering every
// segment empty. A group naming an argument that is not itself an
// empty-segment component of this row is not attached: setting that other
// argument satisfies the provider, and the row cannot see it.
func attachAtLeastOneOf(comps []identity.Component, src Source) {
	empty := map[string]bool{}
	for _, c := range comps {
		if c.OmitIfAbsent && c.Literal == "" && len(c.Attrs) == 1 {
			empty[c.Attrs[0]] = true
		}
	}
	for _, arg := range sortedKeys(src.AtLeastOneOf) {
		group := src.AtLeastOneOf[arg]
		whole := len(group) > 0
		for _, n := range group {
			if !empty[n] {
				whole = false
			}
		}
		if !whole {
			continue
		}
		for i := range comps {
			c := &comps[i]
			if c.OmitIfAbsent && len(c.Attrs) == 1 && containsStr(group, c.Attrs[0]) && c.AtLeastOneOf == nil {
				c.AtLeastOneOf = append([]string(nil), group...)
			}
		}
	}
}
