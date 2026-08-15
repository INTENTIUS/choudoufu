// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"sort"
	"strings"
)

// The exactly-one-of scrape.
//
// Several resources build one segment of their import ID out of whichever of
// a set of mutually exclusive arguments the configuration supplies. aws_route
// is the ratified example: its documented ID is ROUTETABLEID_DESTINATION and
// "DESTINATION" is not an argument name at all - it is whichever of
// destination_cidr_block, destination_ipv6_cidr_block and
// destination_prefix_list_id the route uses. internal/live/identity's
// Component.Attrs is exactly that shape ("names the resource arguments to
// read, in preference order"), and until now the SET of arguments went into
// the ratified table by hand, because nothing in the scrape carried it.
//
// The docs do state it, in prose, and the statement is what this file reads:
//
//	~> Exactly one of of `destination_cidr_block`,
//	`destination_ipv6_cidr_block`, or `destination_prefix_list_id` is
//	required.                                    (route.html.markdown:131)
//
//	~> **Note** Although `cidr_blocks`, `ipv6_cidr_blocks`,
//	`prefix_list_ids`, and `source_security_group_id` are all marked as
//	optional, you _must_ provide one of them in order to configure the
//	source of the traffic.          (security_group_rule.html.markdown:102)
//
// Two spans are read, in this order, because they answer different
// questions. The Import section's own commentary - which on newer pages sits
// inside the Terraform 1.12+ "### Identity Schema" block, as aws_route's
// does - is a statement about the IDENTITY, and is therefore preferred. The
// Argument Reference's is a statement about the CONFIGURATION, true of
// plenty of alternatives that have nothing to do with the import ID, and is
// only read when the Import section says nothing.
//
// WHAT IS DELIBERATELY NOT READ. Per-argument conflict clauses ("Cannot be
// specified with `source_security_group_id` or `self`", "Conflicts with
// `launch_type`) are far more common - 199 pages against 96 - and a
// transitive closure over them reconstructs the documented group correctly
// in only 6 of the 29 cases where both signals exist on one page. It
// over-merges: aws_lambda_function's clauses fuse {filename, image_uri,
// s3_bucket} with s3_object_version and source_kms_key_arn, which are
// COMPANIONS of s3_bucket rather than alternatives to it, and
// aws_dlm_lifecycle_policy's fuse interval with interval_unit, which must be
// set TOGETHER. A wrong group here would put a wrong argument in an import
// identity, so the wider net is not worth having.
//
// The "one of the following ...:" header-plus-bullet-list shape (16 pages,
// aws_route's own Argument Reference among them) is also not read: the names
// live in the bullets rather than the sentence, a page can carry more than
// one such list (aws_route has a destination list AND a target list) and
// nothing in the shape says which one a given ID segment is. aws_route needs
// none of it, because its Import section states the group inline.

// exclusiveRequirementRe matches the requirement half of a one-of statement.
// Both halves must be present: "one of" alone is overwhelmingly a
// valid-value enumeration ("Valid values: one of `Public`, `Private`"),
// which names values rather than arguments and asserts nothing about
// exclusivity.
var (
	exclusiveOneOfRe    = regexp.MustCompile(`(?i)\bone of\b`)
	exclusiveRequiredRe = regexp.MustCompile(`(?i)\bmust\b|\brequired\b`)
	exclusiveBacktickRe = regexp.MustCompile("`([a-z0-9_]+)`")
	// exclusiveEmphasisRe strips Markdown emphasis so the modal is visible:
	// the docs write "you _must_ provide one of them" and "you *must*
	// provide one of them", and a plain search for "must" misses both.
	exclusiveEmphasisRe = regexp.MustCompile(`[_*]+`)
)

// exclusiveGroups returns the mutually exclusive argument groups the doc
// declares exactly one of must be supplied, strongest span first: the Import
// section, then the Argument Reference's own top-level span.
//
// A statement contributes a group only when every backticked name it carries
// is a documented top-level argument and at least two of them are. That
// filter is what separates a real group from a valid-value list, and it is
// also why a nested block's own alternatives never appear: topLevelSpan cuts
// the Argument Reference at its first sub-heading, so a block's arguments
// are not in the pool a name has to be in.
//
// Groups are returned sorted, deduplicated, and with sorted members, so the
// artifact is stable across runs. Nil when the doc states none, so the field
// is omitted rather than empty.
func exclusiveGroups(section string, args []ArgumentRefEntry, doc string) [][]string {
	argSet := map[string]bool{}
	for _, a := range args {
		argSet[a.Name] = true
	}
	if len(argSet) == 0 {
		return nil
	}

	if groups := exclusiveGroupsIn(section, argSet); len(groups) > 0 {
		return groups
	}
	if loc := argHeadingRe.FindStringIndex(doc); loc != nil {
		return exclusiveGroupsIn(topLevelSpan(doc[loc[1]:]), argSet)
	}
	return nil
}

// exclusiveGroupsIn scans one span, a line at a time. Line-at-a-time is not
// a simplification that loses statements: of the 96 pages carrying a
// one-of statement in the 6.59.0 cache, exactly one wraps across source
// lines, and its second line carries no argument name.
func exclusiveGroupsIn(span string, argSet map[string]bool) [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, line := range strings.Split(span, "\n") {
		plain := exclusiveEmphasisRe.ReplaceAllString(line, "")
		if !exclusiveOneOfRe.MatchString(plain) || !exclusiveRequiredRe.MatchString(plain) {
			continue
		}
		var members []string
		allArguments := true
		for _, m := range exclusiveBacktickRe.FindAllStringSubmatch(line, -1) {
			name := m[1]
			if !argSet[name] {
				allArguments = false
				break
			}
			members = append(members, name)
		}
		if !allArguments {
			continue
		}
		members = dedupeStrings(members)
		if len(members) < 2 {
			continue
		}
		sort.Strings(members)
		key := strings.Join(members, ",")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, members)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i], ",") < strings.Join(out[j], ",")
	})
	return out
}
