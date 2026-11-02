// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package identity is choudoufu's per-type identity knowledge: for each
// Terraform resource type it covers, what names one instance of it - the
// configuration arguments an import ID is built from, the character that
// joins them, which parts the cloud assigns, and which parts are the
// account, region or subscription the run is against.
//
// It reads no configuration, starts no provider and calls no cloud, and it
// imports nothing outside the standard library. That is what lets a module
// other than choudoufu import it: plunge reads the same rows to adopt a
// Pulumi resource whose Terraform type a row covers (GitHub issue #2244,
// INTENTIUS/plunge#7). choudoufu's own resolver, which evaluates HCL to
// decide what each row's arguments hold for one instance, stays in
// internal/live/identity and reads every table here through aliases.
//
// # The tables
//
//   - [DefaultTable] and [GoogleTable] map a resource type to its
//     [TypeIdentity]: the [Component] list an import ID is composed from,
//     its separator, and whether the identity is server-assigned.
//     [LookupType] is the lookup across both.
//   - [MarkerlessTypes], [GoogleMarkerlessTypes], [NotImportableTypes],
//     [IDNotProvenWholeTypes], [DiscoverableFallbackTypes],
//     [ContentMatchTypes], [AzureServerMintedTypes] and
//     [AzureRecordCarriedTypes] are type sets for the cases a row alone does
//     not settle: a type that cannot carry an ownership marker, one with no
//     import at all, one whose id is not its whole import identity.
//   - [DocumentedImportIDs] is each type's import-ID grammar as its
//     documentation page states it, and [GoogleAssetImports],
//     [GoogleCompositeImports] and [GoogleIdentityImports] are the Google
//     provider's import formats.
//   - [ParentOf] and its siblings name the parent type a child's identity
//     is composed from.
//
// Most of these are generated: tools/row-gen -emit writes the AWS tables
// and tools/gcp-row-gen the Google ones, into this directory. A generated
// file says so in its header and is never edited by hand.
//
// # Reading a provider's identity schema
//
// A provider that serves resource identity schemas says, per type, which
// identity attributes it requires for import. [IdentityAttrs],
// [IdentityCandidates], [IsContextAttr] and [NotContext] are the rules that
// read those schemas: which attributes name one instance and which are
// context the provider fills in itself. They take the schemas through the
// [TypeSchema] and [SchemaSet] interfaces, so a caller can adapt whatever
// schema representation it holds. [Schema] and [Schemas] are plain
// implementations for a caller that builds them by hand.
//
// # Cloud Control identifiers
//
// [CloudControlTranslation] turns a Cloud Control identifier (and, for some
// types, its resource model) into the Terraform id the type is imported by,
// from the type's row or a written-out rule (GitHub issue #2286, moved from
// plunge). The one value it cannot read from the listing, the AWS account ID
// aws_budgets_budget_action's id starts with, comes in from the engine
// through an [AccountSource]; this package still calls no cloud.
//
// What stays in internal/live/identity, because it reads configuration or
// OpenTofu's provider types: resolution of instances, the config-side
// naming signal, schema-based row synthesis, and the table verification
// that reports against tfdiags.
//
// # Stability
//
// The exported names here are what plunge builds against. A change to a
// field's meaning, or a removal, is a breaking change for that module and
// is made in both repositories together. Adding a row, a table entry or a
// new field with a zero value that keeps today's behaviour is not.
package identity
