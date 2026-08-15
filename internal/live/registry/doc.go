// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package registry loads the two committed join artifacts - live/mapping.json
// (issue #43, the TF-to-CFN type join) and live/registry.json (issue #42,
// the CloudFormation Registry's per-type roster) - and answers the one
// question discovery's Cloud Control fallback (#47) needs of them: given a
// TF resource type with no native provider list resource, is there a mapped
// CFN type that Cloud Control can enumerate on its own, with no per-call
// parsing of either file?
//
// [Load] parses both files once into a [Roster]; every lookup after that is
// a map read. A caller that discovers many types in one run - a whole
// estate's admission table, in the sweep case - is expected to build one
// Roster and share it, not reopen the files per type.
//
// # What counts as mapped
//
// live/mapping.json's via column carries five values (see tools/mapping-gen):
// "name" and "alias" are a real CFN counterpart, curated or found by the
// name heuristic; "service-alias" is the same claim from mapping-gen's
// heuristic v2 (a service-level join whose ambiguous hits are never written
// into the artifact, so a row that carries it is an unambiguous match);
// "fold" says the TF type's live representation is folded into a parent CFN
// resource's own type, so there is no CFN type of its own to enumerate;
// "none" is no counterpart at all. Only the first three produce a
// [Roster.CloudControlType] hit - a fold parent is a different resource, and
// enumerating it would return the wrong type's population. (service-alias
// was missing from the accepted set until #124's aps cohort failed replan on
// aws_prometheus_workspace, a service-alias row the roster silently
// dropped.)
//
// # What counts as listable
//
// live/registry.json's handlers.list is CloudFormation's own claim that a
// ListResources call for the type will do something; handlers.list's
// list_required_input is what tools/registry-gen calls
// EnumerabilityParentInput - a list call that needs a parent's identifier
// this package's callers have no way to supply from a bare type name.
// [Roster.Listable] is therefore true only for handlers.list with an empty
// required-input list - CloudFormation's "free" enumerability - which is the
// same bar admission-path-2 discovery already clears for a provider's native
// list resource: no argument beyond an optional region.
package registry
