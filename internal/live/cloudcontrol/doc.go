// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package cloudcontrol is a client for AWS's Cloud Control API: the
// transport the registry-backed discovery plane (#40) reads live resources
// through. It mirrors the semantics of chant's proven implementation
// (chant/lexicons/aws/src/api/read-client.ts's Cloud Control half), ported
// to Go rather than translated line for line.
//
// Cloud Control speaks AWS JSON 1.0: a POST with an X-Amz-Target header
// naming the operation (CloudApiService.ListResources,
// CloudApiService.GetResource) and a JSON body, against
// https://cloudcontrolapi.<region>.amazonaws.com or an endpoint override
// (floci: http://localhost:4566). [Client] wraps both operations,
// paginating ListResources to exhaustion, and reports failures as
// [*APIError] so a caller classifies them by the API's own error code
// rather than by parsing prose.
//
// # Signing
//
// Requests are signed with SigV4 when credentials resolve, using
// aws-sdk-go-v2's default credential chain unless a [Client] is given its
// own [aws.CredentialsProvider]. Two cases stay unsigned, deliberately:
//
//   - No credentials resolve. The request goes out carrying only the
//     region-scope placeholder described below.
//   - An endpoint override is set and [Config.SignEndpointOverride] is
//     false. Floci does not verify signatures, and signing against it would
//     mean every local run suddenly needs credentials to read what it just
//     deployed. SignEndpointOverride opts back in for an override that is
//     itself real AWS — a VPC endpoint, a signing proxy.
//
// Every unsigned request still carries a region-scope placeholder in its
// Authorization header: an emulator has one host for every region, so
// without it a multi-region estate reads as one region silently, the same
// failure mode chant's regionScope comment documents. The placeholder is
// not a signature — real AWS rejects it — it is only what an emulator
// reads the region out of the way a real SDK would carry it.
package cloudcontrol
