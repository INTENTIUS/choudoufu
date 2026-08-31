// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package recordtrips holds one measurement: how many times a migrated
// estate's plan goes to the record store, and where from.
//
// It exists because until it did, nothing counted them. The counting proxy
// that grades a plan's cost (internal/live/flocitest) stands in front of the
// AWS endpoint, and a record read never goes there — it goes to a local
// directory, an SSM parameter or an S3 object. So every "a plan costs N
// calls" figure this repository has ever recorded was a figure about one of
// the two things a plan does, presented as if it were both. Stock OpenTofu
// makes zero record trips: it reads its whole state once, from one file.
//
// The unit is one call to the wrapped [staterecord.Store], because that is
// what costs something: a stat plus a read locally, a network round trip
// against SSM or S3. The counter is [staterecord.CountingStore], wired into
// the real binary by projection's TF_LIVE_RECORD_TRIPS trip log, so what is
// measured is a real `tofu plan` in its own process rather than an
// in-process reconstruction of one.
//
// The estate is tools/terralith-gen at scale 1, applied by stock terraform
// and then migrated with live-import — the migration the product actually
// claims, not one choudoufu created for itself.
package recordtrips
