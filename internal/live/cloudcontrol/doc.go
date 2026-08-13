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
//
// # Retries
//
// Every call ([Client.ListResources]'s per-page calls, [Client.GetResource],
// [Client.GetResources]) retries under one policy, stated here because
// issue #64 asked for a stated policy rather than an implicit one:
//
//   - Only a ThrottlingException response is retried. Every other failure -
//     a different *[APIError] code (ResourceNotFoundException,
//     ValidationException, UnsupportedOperation, ...), a transport error, a
//     response this client could not parse - returns to the caller straight
//     from the first attempt. Retrying a validation error or a
//     resource-not-found result would not change the outcome; it would only
//     delay reporting it.
//   - Exponential backoff with full jitter: attempt N's delay (before
//     attempt N+1) is drawn uniformly from [0, min(RetryMaxDelay,
//     RetryBaseDelay*2^(N-1))). Full jitter - not a fixed curve - is AWS's
//     own documented recommendation for avoiding synchronized retry storms,
//     which matters here because discovery issues its GetResource calls
//     concurrently: a fixed curve would have every one of them retry in
//     lockstep against the same throttled API.
//   - Bounded attempts: [Config.MaxAttempts] (default 5, counting the first
//     try) caps the total, so a call that keeps getting throttled fails
//     rather than retrying forever.
//   - Context-respecting: a retry's sleep ([retrySleep]) selects on
//     ctx.Done() alongside its timer, so a canceled or deadline-exceeded
//     context stops the retry loop immediately - mid-sleep, if that is where
//     it is - rather than finishing out the backoff curve first.
//
// [Config.RetryBaseDelay] (default 200ms) and [Config.RetryMaxDelay]
// (default 5s) tune the curve; [Config.RetrySleep] overrides the sleep
// function itself, which is how a unit test gets a deterministic, instant
// backoff instead of a real one (internal/live/cloudcontrol/retry_test.go).
//
// # Resource Groups Tagging API
//
// [NewTagging] builds a Client for a second service that speaks the same
// AWS JSON RPC shape against a different host, target namespace and
// protocol version: tagging.<region>.amazonaws.com,
// ResourceGroupsTaggingAPI_20170126, Content-Type application/x-amz-json-1.1
// rather than Cloud Control's 1.0 (see tagging.go's taggingContentType -
// floci enforces the distinction even though X-Amz-Target alone already
// names the operation). GetResources ([Client.GetResources]) is the
// estate-wide sweep primitive issue #47 evaluated: one paginated call
// returns every ARN carrying an estate's tofu-estate tag, in place of a
// ListResources call per admitted type. Issue #51 wires it into
// internal/live/discovery's sweep behind Request.TaggingSweep, joining each
// returned ARN back to a (TF type, identifier) pair
// (internal/live/discovery/tagging.go).
package cloudcontrol
