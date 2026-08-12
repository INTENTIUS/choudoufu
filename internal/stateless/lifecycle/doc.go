// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package lifecycle holds five integration tests and nothing else: the
// lifecycle test itself (P4.1), the snapshot test (P4.2), the exactness
// test (P5.1), the crash-mid-apply test (the concurrency taxonomy's crash
// row, run rather than argued), and the existence-flavor receipt test
// (RA.6).
//
// Every other stateless package tests the piece it owns. This package tests
// the claims those pieces add up to, which no single package can: that an
// estate can be created, inspected, corrected and shrunk with plain
// "choudoufu apply" and plain "choudoufu plan", against a real provider
// talking to a real (emulated) cloud, with no state file existing at any
// point in between. It is a package rather than a file in another one
// because it belongs to no component - it runs the built binary and reads
// the cloud with the AWS CLI.
//
// The tests are gated on TF_ACC or TF_FLOCI_TEST (see
// internal/stateless/flocitest) and need Docker and the AWS CLI.
package lifecycle
