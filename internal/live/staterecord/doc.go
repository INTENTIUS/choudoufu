// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package staterecord is a small, versioned key/value [Store] with
// first-class conditional writes: Get, PutIfVersion, PutIfAbsent, Delete,
// List — nothing else. It backs the micro-state records issue #73's
// charter describes (record-less residue: null_resource, terraform_data,
// time_*, non-sensitive random_* run through the stock provider lifecycle
// against an in-memory state hydrated from and CAS-persisted to one small
// record per resource), but this package itself knows nothing about that.
// It has no notion of an estate, a resource, redaction, or anything else
// choudoufu-specific — keys are opaque strings, payloads are opaque
// bytes, and every choudoufu concept (what a key names, what goes in a
// payload, which resources get one) lives entirely in the caller.
//
// # Why that separation is the point
//
// This package is meant to be upstream-adoptable verbatim: proposable to
// OpenTofu as a lightweight state backend on its own merits, independent
// of choudoufu ever existing. Concretely, that shapes three decisions:
//
//   - The [Store] interface follows upstream's own backend conventions — a
//     clean Get/Put-with-condition/Delete surface, no fork-specific types
//     anywhere in its signatures, workspace-agnostic naming (a "key", not
//     a "workspace" or a "resource address").
//   - Conditional-write/CAS is a first-class interface concept, not
//     something bolted onto a plain Put as an optional flag. Upstream's
//     own s3-locking-with-conditional-writes RFC (20250211) already shows
//     appetite for exactly this primitive as a first-class one.
//   - The package directory holds only store implementations and their
//     tests — nothing that imports estate configuration, redaction rules,
//     or resource-selection logic. A third store (issue #73's ruling:
//     "design the interface so a third store is a new file, not a
//     refactor") is one new file implementing [Store], never a change to
//     this one.
//
// # The interface contract, precisely
//
//   - Keys are opaque strings. Every implementation accepts a reasonably
//     portable subset — this package itself only rejects the empty
//     string, a NUL byte, and a ".." path segment (see validateKey) —
//     but each store's own backend (a filesystem, an S3 object key)
//     may reject a key its own naming rules forbid;
//     that surfaces as an ordinary error, not a [Store]-defined one.
//   - Payloads are opaque []byte. No implementation inspects, parses, or
//     redacts a payload's content; that is the caller's job, every time,
//     before a payload reaches this package and after one leaves it.
//   - Versions are opaque strings with exactly one universal meaning: ""
//     denotes "no record exists here." No implementation ever assigns ""
//     as a live record's version, so a caller can treat it as a stable
//     sentinel without inspecting which store it is talking to. Beyond
//     that, a version's shape is entirely implementation-defined — a
//     content hash, an S3 ETag — and
//     [Store] callers are expected to hold it opaque too: compare it for
//     equality, pass it to PutIfVersion/Delete, never parse it.
//   - Every conditional operation that fails on a version mismatch
//     reports exactly one error type: *[VersionConflictError], naming
//     both the version the caller expected and the version the store
//     actually found (or "" for "no record"). A caller never has to
//     distinguish "conflict" from "some other failure" by parsing prose.
//   - "Conditional" means real compare-and-swap with no read-compare-write
//     race window, on every store: [LocalStore] and [S3Store] both give
//     it. That is a requirement of the interface and not a property two
//     implementations happen to share. See "The store that was retired".
//
// # The three implementations
//
// [LocalStore] (a directory of files, the zero-configuration default — solo
// development, tests, air-gapped runs, mirroring plain local state's own
// "just works" shape), [S3Store] (S3 conditional writes, for anything
// more than one operator shares) and [KubernetesStore] (Secrets in one
// cluster namespace, resourceVersion as the conditional write, for an estate
// that runs on Kubernetes and has no AWS account to put a bucket in). All
// three implement the identical [Store] interface; a caller choosing between
// them is choosing an operational tradeoff, never a different programming
// model.
//
// # The five things the Kubernetes store had to settle
//
// GitHub issue #1392 named five, and each was measured on kind before it was
// written down. They are here rather than in the type's own doc because each
// is a decision about the SHAPE of a record on a substrate, which is what a
// fourth store would have to answer again.
//
//  1. A key becomes an object NAME by hashing. The Secret is named
//     "tofu-record-" and the key's SHA-256; the key itself is in the
//     choudoufu.intentius.io/record-key annotation, which is where List reads
//     the keys it returns. A record key carries "/" and base64url runs and
//     the conformance suite's own chunked key is 500 characters, against the
//     253 an object name holds, so no encoding fits. An annotation is capped
//     at 256 KiB in total against the 1,024 bytes of the longest key the S3
//     store accepts, so the annotation is not close to a limit. A Get whose
//     object holds a different key is refused ([KeyCollisionError]) rather
//     than answered.
//  2. Isolation is the NAMESPACE, one per estate, defaulting to
//     "tofu-records-<estate>". RBAC has no predicate on a label and admission
//     is never consulted for a get or a list, so nothing but the namespace
//     can fence a read. The store does not create it: an absent namespace is
//     refused by name ([NamespaceMissingError]) with the kubectl line, which
//     it has to be, because a list in a namespace that does not exist answers
//     EMPTY and an empty listing reads as an empty estate.
//  3. The estate is a LABEL and the address is an ANNOTATION. tofu-estate has
//     to be a label because live/kubernetes/estate-boundary.yaml selects on
//     it, which is what fences a write to a record object with no policy
//     added. A label value caps at 63 characters and a resource address does
//     not (#1016), so the address cannot be one. Same split, same reason, as
//     the object tags #1337 put on S3 objects.
//  4. A record over a Secret's one MiB is refused by name
//     ([RecordTooLargeError]), before the request and measured after
//     compression, because that is the number the API server measures.
//  5. Anyone who can "get secrets" in the records namespace reads every
//     recorded value, which is the same bargain s3:GetObject on the bucket
//     makes for the other remote store.
//
// One consequence outside this package: a record Secret carries the estate's
// tofu-estate label, and the Kubernetes sweep reads that label as "this
// object is in the estate". Objects in the estate that no block declares are
// orphans a plan proposes to DESTROY, so internal/live/kubesweep excludes the
// store's own objects by name (RecordStoreObject). Measured on kind: without
// that exclusion an ordinary second plan proposed destroying all five of the
// estate's own record Secrets.
//
// # The store that was retired
//
// Until GitHub issue #1346 there was a third, on AWS Systems Manager
// Parameter Store, and it was the default recommendation for a team. It was
// retired as a RECORD store for three reasons. Standard parameters cap at
// 10,000 per account and region, against the customer's own quota. Past
// that, every parameter bills monthly on the advanced tier. And it has no
// general conditional write: it could create-if-absent and nothing
// else, so every update and delete was a read-compare-write with a race
// window, where this package's whole consistency story is a per-key
// conditional write.
//
// No migration was written, because no estate was on it when it was
// retired. That is the reason, and it is recorded so nobody later assumes a
// migration path was designed and lost.
//
// This says nothing about Parameter Store for SECRET values. Keeping secret
// material out of the bucket, in SSM, is planned (#1244 section 3) and not
// built; nothing in this package does it today.
package staterecord
