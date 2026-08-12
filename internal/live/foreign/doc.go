// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package foreign classifies the live resources an estate does not own, and
// is the safety property of stateless mode: a live resource nobody claims is
// surfaced, and is never a deletion candidate.
//
// Its input is one [discovery.Result] gathered with
// [discovery.Request.CollectUnclaimed] set, plus the configuration the
// resolutions came from. Its output is a report, and only a report:
// [Classify] never rewrites a resolution, never stamps a marker, and never
// hands anything to the projection builder. That is not an implementation
// detail deferred to a later phase - it is how the protection property is
// made structural. The prior state a stateless plan runs against is built
// from resolutions, resolutions come from declared addresses, and an
// unclaimed live resource has no declared address, so it can never enter the
// prior state and the plan engine has nothing to propose destroying. Nothing
// here has to remember to exclude it.
//
// # The three classes
//
// Every unclaimed live resource lands in exactly one:
//
//   - [ClassForeign]: no tofu-estate tag at all, and no declared instance it
//     could plausibly be. Report only. Each one carries its type, live ID,
//     display name, tag summary, and a sentence saying why it is not a bind
//     candidate.
//   - [ClassBindCandidate]: no tofu-estate tag, but its content exactly
//     matches a declared instance of the same type that discovery could not
//     find. Surfaced with an adoption hint - the marker pair to stamp - and
//     never bound. live/MARKERS.md is explicit that ownership is the tag
//     and nothing else; inferring it from a content match would be exactly
//     the guess the marker spec exists to forbid.
//   - [ClassOtherEstate]: carries a tofu-estate tag naming a different
//     estate. Counted, not itemized: another estate's resources are that
//     estate's business.
//
// Malformed markers (tofu-estate present, tofu-address missing or
// unparseable) and collisions are neither foreign nor owned. They are named
// errors, raised by discovery as [discovery.Problem]s, and this package does
// not reclassify them as anything - a resource whose ownership record is
// broken is not "unowned", it is broken.
//
// # Matching is conservative on purpose
//
// A bind candidate needs an exact match on the type's identity-bearing
// arguments and nothing else. The rules, all of which must hold:
//
//  1. The declared instance is unbound (discovery found no marker for it) and
//     has no instance key. count and for_each members are a keyed or fungible
//     set whose membership is phase 3's slot matcher to decide; picking one
//     here would be a guess wearing a match's clothes.
//  2. The type has an entry in the match table below. aws_route_table,
//     aws_internet_gateway and aws_eip do not: nothing in their configuration
//     distinguishes one from another, so no content match over them can mean
//     anything.
//  3. Every argument in that entry is statically evaluable from configuration
//     (constants, variables, locals and functions - the same subset identity
//     resolution uses) and non-empty.
//  4. The live object carries every one of those attributes, known, non-null,
//     and equal string-for-string. Near misses are not matches: a security
//     group named "web-sg-old" against a declared "web-sg" is plain foreign.
//  5. The match is one-to-one. Two live resources matching one address, or
//     one live resource matching two addresses, means every resource involved
//     is reported as plain foreign with the ambiguity named.
//
// When in doubt the answer is [ClassForeign], which is the safe direction:
// foreign is report-only either way, and a missed adoption hint costs an
// operator one command while a wrong one would attach a plan to somebody
// else's resource.
//
// # Rename candidates, which are about owned resources rather than unowned
//
// One thing here is not about the unclaimed side at all. When a for_each key
// changes in configuration - this["a"] becomes this["c"] - the live resource
// keeps its old marker, so discovery reports it as an orphan (this estate's
// tag, an address nothing declares) and reports this["c"] as unbound, and the
// plan proposes a create beside a resource that already exists. Both reports
// are correct and neither is useful on its own.
//
// [Result.Renames] pairs them, under a rule as conservative as the bind
// candidate rule above and for the same reason - the marker spec forbids
// guessing at ownership, and a wrong pairing would offer to point a marker at
// the wrong resource:
//
//  1. Same resource type and same resource block. The block match is the
//     whole pairing; nothing crosses from aws_subnet.this to aws_subnet.other.
//  2. The block uses for_each. count members are excluded outright: a live
//     count member past the declared count is surplus, which the slot matcher
//     has already accounted for, and offering to rename it would fight the
//     scale-down rule.
//  3. One orphan and one unbound instance, both ways. Two of either is
//     reported as an ambiguity with no command - which live resource became
//     which key is precisely what a marker cannot say.
//  4. The two keys differ, which they always do in the real case, since equal
//     keys would have bound.
//
// Content plays no part. It could only strengthen a pairing the block match
// has already made, and the identity-bearing attributes it would be made on
// are not carried for an owned resource anyway - discovery brings back the
// full listed object for the resources nobody claims and the tags for the
// ones this estate owns, and matching on tags is what the match table exists
// to refuse.
//
// Nothing is applied. The marker is not rewritten, the orphan is still an
// orphan, and the plan still proposes creating the declared instance. What
// the candidate carries is [RenameCandidate.Command]: the exact
// "choudoufu live-mv <old> <new>" that does the rewrite, for an operator who
// agrees with the pairing.
//
// # What was not swept, and why that matters more than what was
//
// A classification is only as wide as the scan behind it, and "nobody looked"
// must never read as "there are none". [Result.Unswept] carries the types
// this pass cannot speak for, each with a reason:
//
//   - the provider cannot list the type ([discovery.ProblemTypeNotListable]);
//   - listing it failed ([discovery.ProblemListFailed]);
//   - it was scanned with the server-side estate filter on
//     ([discovery.ScopeEstate]), which hides unclaimed resources by
//     construction;
//   - no instance of it needed discovery, so it was never listed at all. This
//     is the big one in practice: a configuration's client-named types
//     (aws_s3_bucket, aws_iam_role) are resolved from configuration and never
//     enumerated, so a foreign bucket in the same account is invisible here
//     and saying otherwise would be a lie.
//
// Even a fully swept type is not a census. The AWS provider appends filters
// of its own to every EC2 list, and one of them is is-default = false: an
// account's default VPC never appears in a list of aws_vpc, so it can never
// be classified, reported, or adopted through this path. Against the floci
// emulator the wide scan finds the default subnets, route tables, gateway and
// security groups, but not the default VPC. No call this package or discovery
// can make changes that, and no output either produces should imply
// completeness.
package foreign
