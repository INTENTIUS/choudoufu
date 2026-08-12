// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package listclient is stateless mode's client for the provider list
// protocol: the ListResource server-streaming RPC and the list resource
// schemas that parameterize it.
//
// Listing is how stateless mode recovers the identity of resources whose
// identity is not in configuration. Admission path 2 (marker) lists a type
// filtered by ownership tags and reads the identity off each result;
// admission path 4 (list and content match) lists a type and binds by
// content. Both need the same three things from a provider, and this
// package is the whole surface for getting them:
//
//   - which types the provider can list, from [ListSchemas];
//   - what a list call for a given type accepts as configuration, from
//     [TypeSchema.BuildConfig], which is where filtering is expressed for
//     providers that support it;
//   - the results, from [List] or [Stream], each carrying the resource
//     identity typed by that type's identity schema.
//
// # Not every provider lists
//
// Listing arrived in plugin protocol 5.10 and 6.10. A provider built before
// that answers ListResource with gRPC status Unimplemented; a provider built
// after it may still list only some of its types. Neither is an error in the
// world, only an error for the caller that wanted to list, so both surface
// as ordinary error diagnostics naming the type. Nothing in this package
// panics on a provider that cannot list, and callers that want to check
// before asking can use [Schemas.Supports].
//
// # Filtering is the provider's business
//
// The list configuration schema is entirely provider-defined: the AWS
// provider offers EC2-style filter blocks for most of its listable types,
// other providers may offer nothing but a region. This package encodes
// whatever the caller builds against whatever schema the provider serves and
// makes no attempt to translate a general filter concept across providers.
// A caller that needs a filter the provider does not implement has to filter
// the results itself.
//
// # Where this sits
//
// The protocol client proper lives on the concrete gRPC provider handles as
// ListResource and ListResourceStream methods (internal/plugin for protocol
// 5, internal/plugin6 for protocol 6). This package is the ergonomic layer
// over them: schema lookup, configuration construction, and result decoding
// in the shape discovery wants.
package listclient
