// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package datasources is a choudoufu fork port (#2414, of #2256): data
// sources on OpenTofu's new runtime (internal/engine), until upstream's
// `#4058` and its pull request `#4740` land.
//
// Upstream's new runtime reads every data block during the plan, never
// defers one to the apply, keeps no result in the refreshed state, and its
// apply engine's DataRead is unimplemented, so the gap table refused every
// data block. The port is upstream's own pull request `#4740` ("engine:
// implement data resources", commit 4b689c442b on its engine-data-resource
// branch), cherry-picked and adapted rather than written anew:
//
//   - internal/resources' DataResourceType (data.go, data_read.go,
//     data_validate.go) is taken from that commit unchanged except for the
//     import path and the provider_meta field upstream has since removed;
//   - internal/engine/planning's fork_datasources.go is its plan_data.go: a
//     block whose configuration is wholly known and depends on no pending
//     change is read during the plan and its result kept in the refreshed
//     state; otherwise, outside a refresh-only plan, a Read change defers it
//     to the apply. Pending changes are found by the PendingChange marks
//     upstream's `#4738` already put on planned values;
//   - internal/engine/applying's fork_datasources.go is its DataRead.
//
// Two parts of that commit are left out to keep the port small: the
// execution graph's opDataRead keeps its planned-value operand (the port's
// read ignores it, as upstream's does by removing it), and the in-run-estates
// port keeps answering the terraform_estate_outputs blocks it covers.
//
// Data sources carry no ownership marker and no record in live mode: the
// live projection, the marker adjuster and the identity resolver look only
// at managed resources, on either runtime (internal/live/projection's
// TestDataSourcesCarryNoMarkerOrRecord).
//
// Every call site is commented "choudoufu fork port (#2414): remove when
// upstream #4058 and #4740 land". Removing the port is deleting this package,
// the two fork_datasources.go files, those call sites and the "data-sources"
// row's fork_port and switch in internal/live/newruntime/gaps.json; the
// resources files are replaced by upstream's own.
//
// Switched off (forkport.Enabled), the engine behaves as upstream's does and
// the gap table's preflight refuses data blocks again.
package datasources

import "github.com/intentius/choudoufu/internal/engine/forkport"

// Name is the port's name in the internal/engine/forkport registry.
const Name = "datasources"

// Enabled reports whether the port is on.
func Enabled() bool {
	return forkport.Enabled(Name)
}
