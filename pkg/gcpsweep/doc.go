// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package gcpsweep is the GCP estate sweep's client (GitHub issue #1119,
// the GCP platform epic): which live resources in a set of projects carry
// this estate's tofu-estate label, so that one whose block the
// configuration no longer declares can be proposed for removal, exactly as
// the AWS tagging sweep and the Kubernetes label list do on their
// substrates.
//
// # What a sweep is here
//
// GCP has a cross-service, label-filtered search, which Kubernetes lacks
// and AWS has in the Resource Groups Tagging API: Cloud Asset Inventory's
// searchAllResources (https://cloud.google.com/asset-inventory/docs/reference/rest/v1/TopLevel/searchAllResources).
// One query per project, labels.tofu-estate=ESTATE, paged until the
// response carries no nextPageToken, returns every indexed resource of
// every asset type that carries the label. The scope is a project, never
// a folder or an organization: the GCP fence is ruled project-scoped for
// now (GitHub issue #1121), and a project is what a provider block names.
//
// The query's label operator is "=", an exact match. ":" is a token
// match, under which labels.tofu-estate:prod would also find a resource
// labelled prod-east, so [Client.Search] also drops any result whose own
// label is not exactly the estate. A query the service cannot parse is an
// INVALID_ARGUMENT and fails the search; it is never read as "nothing
// found".
//
// # Failure is never an empty answer
//
// Every way a project's search can fail is an error, and four of them are
// typed so that the discovery leg can name the fix rather than print a
// raw HTTP body: [ServiceDisabledError] (enable cloudasset.googleapis.com
// on the project), [PermissionDeniedError] (grant the permission it names,
// cloudasset.assets.searchAllResources), [PartialError] (a page after the
// first failed, so the pages already read are discarded rather than
// returned as the whole answer), and [UncoveredProjectError] (the sweep was
// asked about a project no configured credential is set to search). A 200
// with no results is the one empty answer, and it means the index holds
// nothing labelled for the estate in that project.
//
// # Credentials
//
// [ProviderAttrs] reads the hashicorp/google provider block the way the
// provider itself resolves it, and [Attrs.Auth] names which source
// a client will authenticate with: access_token, then credentials, then
// Application Default Credentials, each optionally impersonating
// impersonate_service_account. The provider's environment fallbacks apply
// to any argument the block leaves unset ([Attrs.WithEnv]).
//
// # Per-type lists
//
// The index lags a create, so it cannot show the second of two objects two
// concurrent applies made of a type whose identity the API assigns.
// [Client.ListType] lists each such labelled type ([CoveredTypes]) through
// its own service's list method instead, under the provider's base path
// for the product, and keeps what carries the estate's label exactly. It
// fails the way [Client.Search] does, with the type's own service and
// permission named (typelist.go).
//
// # The fakes' shapes
//
// testdata/README.md writes down the exact request [Client.Search] sends
// and every response shape this package's tests serve, with the Google
// document each was taken from. It is the specification the emulator's
// Asset Inventory surface (lex00/floci-gcp#7) is to be checked against.
package gcpsweep
