// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package stamp makes ownership markers something the tool guarantees rather
// than something the configuration author remembered to write.
//
// Every taggable managed resource in a stateless run carries the two marker
// tags from live/MARKERS.md - tofu-estate naming the estate and
// tofu-address naming the config address that owns it. Before this package
// existed the only thing that put them there was a human typing them into a
// tags argument, which meant an estate's ownership records were exactly as
// complete as its authors' discipline. Stamping closes that: a resource that
// does not declare its markers gets them, visibly, in the plan.
//
// # The seam: configuration synthesis, before the plan runs
//
// Markers are injected by rewriting the resource's own configuration body -
// adding the two marker entries to its tags argument, or adding the whole
// tags argument when there is none - and then letting the ordinary plan
// engine do what it always does. Nothing about the plan, the provider
// protocol, or the renderer is special-cased.
//
// The alternative seam was post-plan mutation: run the plan, decode each
// [github.com/intentius/choudoufu/internal/plans.ResourceInstanceChangeSrc]'s
// After value, merge the tags in, re-encode. It was rejected for three
// reasons, in increasing order of seriousness.
//
//  1. It duplicates schema knowledge. Every After value is a DynamicValue
//     encoded against the resource type's implied type; merging a tag means
//     decoding and re-encoding it correctly for every resource type, in code
//     that has to stay in step with the provider's schema forever.
//
//  2. The provider never sees the tags. PlanResourceChange has already
//     returned by then, so anything the provider derives from tags - the AWS
//     provider's tags_all, most obviously - is computed from the untagged
//     configuration and then contradicted by the plan the operator reads.
//
//  3. Apply would throw the injection away. A managed resource's apply node
//     re-plans from configuration before it applies (see
//     NodeApplyableResourceInstance.managedResourceExecute, which calls
//     n.plan and then checkPlannedChange), and compares the result against
//     the change the plan recorded. Tags that exist only in the recorded
//     change and not in the configuration would disappear on that re-plan and
//     be reported as an inconsistency. The marker would show in the diff and
//     never reach the cloud, which is the worst of the available outcomes: a
//     promise the tool visibly makes and silently breaks.
//
// Config synthesis has none of those problems, because it does not fight the
// pipeline. The markers are evaluated with the resource's other arguments,
// the provider plans against them, the renderer shows an ordinary in-place
// "~ tags" update (or the tags map inside a create), and an apply re-planning
// from the same stamped configuration produces the same values. The tags a
// stateless plan shows are the tags a stateless apply would write.
//
// It costs two things, both accepted deliberately:
//
//   - The in-memory configuration is mutated. It is this run's own copy,
//     loaded at the start and dropped at the end - no file is touched - but
//     anything that reads a resource body after [Stamp] sees a body its
//     author did not write. In particular the estate-name derivation, which
//     reads tofu-estate values back out of the configuration, must run
//     before stamping and does (see internal/command/live_plan.go).
//
//   - Only HCL-syntax configuration can be stamped. A resource written in
//     JSON syntax (*.tf.json) parses to a body this package cannot rewrite,
//     and is skipped with a warning rather than silently - or with an error,
//     when the resource is one only its marker could ever find. That is the
//     same documented gap the lint package's count.index rule has, and no
//     fixture in this repository is in JSON syntax.
//
// # A tags argument this pass cannot read is merged into, not skipped
//
// The markers go into a tags argument written as an object literal by
// appending two entries to it. Anything else - a merge() call, a variable, a
// conditional, an object with a computed key - cannot be read entry by entry,
// and used to be skipped with a warning. For a resource whose identity the
// provider assigns, that skip was a disaster with a warning in front of it:
// the apply created a subnet with no marker, the next plan could not find it,
// and every plan after that proposed creating another one (audit finding C2).
//
// So the markers are merged in instead. An existing merge() gains the marker
// object as a final argument; any other expression is replaced by
// merge(<the expression>, {markers}). merge's last argument wins, so the
// markers on the applied resource are the ones this run stamped whatever the
// unreadable half produces, and the plan renders the result like any other
// tags argument. Where the expression does evaluate from configuration alone -
// a local, a variable with a default - its entries are read first, so a marker
// already in there is verified rather than overwritten.
//
// # Not stamping is an error for a resource only its marker can find
//
// [Request.NeedsDiscovery] carries the identity package's classification in,
// and it decides the severity of every failure above. A resource named by its
// own configuration survives being unmarked: the estate loses its ownership
// record, which is a real cost and a warning. A resource whose identity the
// provider assigns does not survive it at all, so the run stops instead.
//
// # Which resources are taggable
//
// Taggability is read from the provider schema, never from a list of type
// names: a resource type is taggable when its schema has a top-level "tags"
// attribute of a map type that the configuration is allowed to set. Types
// without one (aws_route, aws_s3_bucket_policy, aws_iam_role_policy_
// attachment) are skipped in silence, because there is nothing wrong with
// them - the marker path simply is not how they are identified, and the
// identity package already knows that.
//
// # Conflicts are never resolved by overwriting
//
// A marker already in the configuration is left exactly as it is when it
// agrees with what this run would write. When it disagrees - a different
// estate, or a different address - stamping fails with a named error instead
// of rewriting it. Overwriting a tofu-address is a rename, which is
// live-mv's job (P3.3); overwriting a tofu-estate is a transfer of
// ownership between estates, which is adoption's. Neither is something a
// plan should do as a side effect of being run.
//
// A marker whose value cannot be read from configuration alone - built from a
// variable, a function call, another resource's attribute - is neither
// verified nor replaced. It gets a warning naming the resource, and stamping
// leaves that key alone: this package will not overwrite a value it cannot
// prove wrong.
//
// # count and for_each
//
// The marker value is the *instance* address, escaped per MARKERS.md:
// aws_eip.pool[2] is stamped as aws_eip.pool:2 and aws_subnet.this["a"] as
// aws_subnet.this:a, which is exactly what discovery normalizes an observed
// tag to before comparing. One configuration body serves every instance of a
// resource, so the synthesized value is not a constant but a template over
// count.index or each.key, evaluated per instance by the ordinary evaluator.
//
// # tofu-slot comes in from outside
//
// The third marker key is the one value this package cannot compute. A slot
// is minted once per instance from a monotonic counter and not reused, so
// which slot an instance holds depends on which instances have ever existed -
// a fact about the live estate, not about the configuration. It arrives as
// [Request.Slots], the assignment
// [github.com/intentius/choudoufu/internal/live/discovery.Result.SlotTable]
// produced from the live set, keyed by the same escaped instance address this
// package already synthesizes for tofu-address.
//
// What is written is a lookup into that table:
//
//	tofu-slot = lookup({ "aws_eip.pool:0" = "0", "aws_eip.pool:1" = "3" }, "aws_eip.pool:${count.index}", "")
//
// count.index appears there and is not a leak. The table is the assignment;
// the index only selects a row from it. `tofu-slot = count.index` would make
// the lexical position the identity, which is the one thing MARKERS.md says a
// slot is not - and which the lint package's count.index rule exists to catch
// (its exemption list holds tofu-address and pointedly not tofu-slot). This
// expression says instead "whatever slot this run worked out that instance k
// holds", and the working out happened in the set matcher, from live tags,
// before this package was called. Nothing is stamped for a count block the
// table says nothing about, and nothing is ever stamped for for_each, whose
// instances are named by their keys.
//
// The seam this arrived through is markerItems, which returns a slice of
// object entries rather than one entry precisely so a third could be added.
package stamp
