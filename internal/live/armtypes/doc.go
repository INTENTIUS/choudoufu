// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package armtypes is the Azure epic's (#1120) answer to "can this azapi
// resource carry a tag": a table of ARM resource types and API versions,
// generated from the type data azure/azapi embeds, so that the stamp never
// writes `tags` on an azapi resource whose ARM type cannot take them.
//
// # Where the answer comes from
//
// azapi ships Azure's bicep-types data in its own binary
// (internal/azure/generated in the provider repository: index.json, which
// maps "<type>@<api-version>" to a ResourceType, and the types.json files
// it references). The #1122 ruling (2026-10-19) makes that data the
// taggability source for azapi, because it is what the provider itself
// validates a body against and what it consults before it adds
// default_tags. A type is taggable at an API version when its request body
// declares a `tags` property without the ReadOnly flag. That is azapi's own
// test, canResourceHaveProperty in internal/services/resource.go at
// v2.13.0:
//
//	objectType, ok := (*resourceDef.Body.Type).(*aztypes.ObjectType)
//	...
//	if prop, ok := objectType.Properties[property]; ok {
//		if !prop.IsReadOnly() {
//			return true
//		}
//	}
//
// with one widening, recorded in the table's header: a body that is a
// DiscriminatedObjectType counts its baseProperties. canResourceHaveProperty
// returns false for every discriminated body, but it gates only azapi's
// default_tags. A `tags` argument the configuration sets is copied into the
// body unconditionally (azapi_resource.go, expandBody) and azapi's body
// validator accepts it through discriminated_object_type.go's
// baseProperties. So a discriminated body with a writable base `tags` does
// take tags, and the stamp may write them there. At v2.13.0 that is 13 of
// 33,979 type-versions.
//
// The same test answers whether `name` and `location` are writable, for
// the identity and readiness work that needs them.
//
// # Keyed by type and API version
//
// Taggability moves between API versions of one type (84 types at
// v2.13.0), so the key is both. The type compares case-insensitively and
// the version exactly, as azapi's own GetResourceDefinition does
// (internal/azure/loader.go: strings.EqualFold on the type, == on the
// version). A version azapi does not know is "unknown", never a guess
// from a neighbouring version: azapi itself has no schema for it either.
//
// # Regenerating
//
//	go run ./tools/armtypes-gen -azapi-src <azapi checkout at the pinned tag>
//
// The table records the azapi tag and commit it was generated from, and
// TestTableMatchesPinnedSource regenerates it when ARMTYPES_AZAPI_SRC names
// such a checkout.
package armtypes
