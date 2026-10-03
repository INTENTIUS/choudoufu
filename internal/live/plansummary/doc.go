// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package plansummary groups identical changes across many plans (GitHub
// issue #1753, part of #1749). It reads the set plan's -json document, one
// stock plan per estate root, or a single stock plan, and reports "18
// roots: identical change X. 1: X plus Y. 1 failed: here is why", so a
// reviewer reads each distinct change once instead of N times.
//
// # What "identical" means
//
// A unit is a root of a set document, or, for a single plan, one instance
// of a resource; a single plan's instances are only grouped with other
// instances of the same expansion (the address with its keys removed).
// Two units are identical when their normalized change sets are equal as
// multisets. A normalized change is:
//
//   - the action: create, update, delete, replace, read or forget. No-op
//     changes are not changes and are dropped.
//   - the address, with every instance key removed (`web["a"]` and
//     `web[0]` are both `web`) and the unit's tokens stripped from module
//     call names and the resource name. The resource type is never
//     touched.
//   - for each top-level attribute the change touches, its value before
//     and after, with the unit's tokens stripped from every string. An
//     attribute equal on both sides is not part of an update, so
//     server-assigned identifiers (id, arn) never split a group. A create
//     carries every value it sets; a delete carries only its address.
//
// A unit's tokens are what names it, and nothing else: for a root, its
// estate name and its directory's base name; for an instance, its own
// instance keys. Each token is also tried with '-' and '_' swapped. A token
// is stripped only where it stands as a whole word (the characters either
// side are not letters or digits), so "acme" leaves "acmeweb" alone; a
// token under three characters must also touch a '-' or '_', so the key
// "1" is stripped from "web-1" and never from "10.1.0.0/16".
//
// Markers are judged by what they hold, never blanked. A tofu-estate equal
// to the unit's estate, and a tofu-address (or its continuations, or the
// Kubernetes address annotation) equal to the instance's own escaped
// address, normalize away; a marker holding anything else stays, so a
// resource stamped for some other estate is an outlier rather than a
// member.
//
// Nothing else is stripped. A different instance size, CIDR, policy
// document or tag value that does not carry the unit's name is a different
// change.
//
// # What is never folded
//
// Every destroy and replace is listed by its real address in every format,
// however large its group. A root whose status is not "planned", that
// carries no plan, or whose plan errored is a line of its own with its
// reason and never a group member. The summary names changed attributes
// and never prints their values.
package plansummary
