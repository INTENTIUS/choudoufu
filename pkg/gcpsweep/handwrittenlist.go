// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"strings"

	"github.com/intentius/choudoufu/pkg/identity"
)

// The covered types with no Asset name in pkg/identity (INTENTIUS/choudoufu#2274,
// paired with INTENTIUS/plunge#19, whose gcplist/handwritten.go lists the same
// three): Cloud SQL instances and GKE clusters, whose identity rows are
// written without an Asset template, and google_assured_workloads_workload,
// whose list is under the organization its own required argument names.
// Each is listed like a derived type (its path filled from the block, as
// derivedScope fills it), so `organization` and `location` come from the
// block's own arguments. None of the three services publishes a list
// consistency model: each binds, consistency unmeasured (the ruling on
// INTENTIUS/choudoufu#2264).
//
// A listed object is named by its documented Asset name
// (https://docs.cloud.google.com/asset-inventory/docs/asset-names), so a
// Cloud Asset Inventory hit and a list hit for the same object are the same
// name, and its import ID is that name's path, which is the type's
// ImportSyntax in pkg/identity ([HandwrittenImportID]).
var handwrittenLists = map[string]TypeList{
	"google_sql_database_instance": {
		Service: "sqladmin.googleapis.com", AssetHost: "cloudsql.googleapis.com",
		AssetType: "sqladmin.googleapis.com/Instance",
		Path:      "projects/{{project}}/instances", Field: "items",
		Permission: "cloudsql.instances.list",
		Endpoint: Endpoint{Arg: "sql_custom_endpoint", Env: "GOOGLE_SQL_CUSTOM_ENDPOINT",
			Default: "https://sqladmin.googleapis.com/sql/v1beta4/"},
		Import: "projects/{{project}}/instances/{{name}}",
	},
	// locations/- lists every location's clusters, so the list does not
	// depend on whether the block's location defaulted to the provider's
	// region or zone; each cluster names its own location.
	"google_container_cluster": {
		Service: "container.googleapis.com", AssetType: "container.googleapis.com/Cluster",
		Path: "projects/{{project}}/locations/-/clusters", Field: "clusters", NameByLocation: true,
		Permission: "container.clusters.list",
		Endpoint: Endpoint{Arg: "container_custom_endpoint", Env: "GOOGLE_CONTAINER_CUSTOM_ENDPOINT",
			Default: "https://container.googleapis.com/v1/"},
		Import: "projects/{{project}}/locations/{{location}}/clusters/{{name}}",
	},
	"google_assured_workloads_workload": {
		Service: "assuredworkloads.googleapis.com", AssetType: "assuredworkloads.googleapis.com/Workload",
		Path: "organizations/{{organization}}/locations/{{location}}/workloads", Field: "workloads",
		Permission: "assuredworkloads.workload.list",
		Endpoint: Endpoint{Arg: "assured_workloads_custom_endpoint", Env: "GOOGLE_ASSURED_WORKLOADS_CUSTOM_ENDPOINT",
			Default: "https://{{location}}-assuredworkloads.googleapis.com/v1/"},
		Import: "organizations/{{organization}}/locations/{{location}}/workloads/{{name}}",
	},
}

// HandwrittenImportID is the import ID of a [handwrittenLists] type's object
// from its Asset name: the name's path, when the name has the type's Asset
// host and the path the shape of its ImportSyntax. ok is false for any other
// type or name.
func HandwrittenImportID(typeName, assetName string) (string, bool) {
	t, ok := handwrittenLists[typeName]
	if !ok {
		return "", false
	}
	host := t.Service
	if t.AssetHost != "" {
		host = t.AssetHost
	}
	path, ok := strings.CutPrefix(assetName, "//"+host+"/")
	if !ok {
		return "", false
	}
	want := strings.Split(t.Import, "/")
	got := strings.Split(path, "/")
	if len(want) != len(got) {
		return "", false
	}
	for i, w := range want {
		if got[i] == "" || (!isPlaceholder(w) && got[i] != w) {
			return "", false
		}
	}
	return path, true
}

// importIDOf is a listed or searched object's import ID for t: pkg/identity's
// for a type it has an Asset name for, else the hand-written rule.
func importIDOf(t TypeList, assetName string) (string, bool) {
	if id, ok := identity.GoogleAssetImportID(t.Type, assetName); ok {
		return id, true
	}
	return HandwrittenImportID(t.Type, assetName)
}
