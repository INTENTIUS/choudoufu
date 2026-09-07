// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package stamp is the marker-stamping LAYER's refusal registry and the one
// sentence that layer says to an operator. The engine that used to live
// here is gone.
//
// # What this package was, and why it was retired
//
// Until GitHub issue #644 this package injected the two ownership marker
// tags from live/MARKERS.md by rewriting the resource's own HCL body -
// appending entries to its tags argument, or synthesizing the whole
// argument - and then letting the ordinary plan engine evaluate what it had
// written. That seam was chosen over post-plan mutation for three reasons
// that were all correct at the time: it did not duplicate schema knowledge,
// the provider saw the markers (so tags_all was computed from them), and an
// apply re-planning from the same configuration produced the same values.
//
// It also cost two things. The in-memory configuration was mutated, so
// anything reading a resource body afterwards saw a body its author did not
// write. And it could only rewrite HCL syntax - a *.tf.json resource parses
// to a body it could not touch - which meant the marker writer had a
// syntax-shaped blind spot the rest of the pipeline did not.
//
// The deeper cost was that one HCL body serves every instance of a resource
// and every instance of the module call it sits in, so a per-instance value
// had to be written as a TEMPLATE over expressions the evaluator would fold
// later: count.index, each.key, a lookup() into a slot table, and - for a
// resource inside a keyed module call - tofu.marker_module_prefix, an
// evaluator symbol this fork added to the language for no other reader.
// Every one of those was machinery in service of not having the instance in
// hand.
//
// GitHub issue #388's plan-node seam has the instance in hand.
// [github.com/intentius/choudoufu/internal/live/projection.NodeResolver.AdjustConfigValue]
// is called once per concrete [github.com/intentius/choudoufu/internal/addrs.AbsResourceInstance]
// with that instance's already-evaluated configuration value, so it writes
// the address as a plain string, needs no template, no evaluator symbol and
// no HCL at all, and works the same for JSON-syntax configuration. It
// defaulted on 2026-08-25 and this package's engine has not run in a
// default build since; #451 ported the two capabilities that engine was
// still the only source of (the marker-conflict refusal and the #380
// ignore_changes protection) and #454 ported internal/live/check's offline
// LayerStamp report off it. #644 deleted what was left.
//
// # What is still here
//
//   - [Refusals] and [LookupRefusal]: the layer's refusal registry, folded
//     into internal/live/check's catalog under check.LayerStamp and
//     rendered into live/LIMITATIONS.md. refusals.go carries the per-entry
//     account of which five entries #644 retired and why each could not
//     survive the engine.
//   - The [SummaryMarkerConflict], [SummaryNotStamped] and
//     [SummaryUnmarkedApply] constants, which the two packages that now
//     raise these diagnostics - internal/live/check's node-path port and
//     internal/live/projection - use so that a summary an operator reads
//     and a registry entry they look it up under cannot drift apart.
//   - [UnmarkedDiscoveryDetail]: the sentence a marker-only resource with
//     nowhere to write a marker gets, one per [identity.DiscoveryCause], so
//     that the two thirds of affected configurations with a next step are
//     told what it is.
//
// # Which resources can carry a marker
//
// Unchanged, and it never lived in this package's engine anyway:
// taggability is read from the provider schema ([markers.Taggable] and
// [markers.TagSurface]), never from a list of type names. The hand-written
// pin that holds those predicates to the admission table and to the
// provider's real schema is this package's taggability_test.go, kept for
// the reason its own doc comment gives.
package stamp
