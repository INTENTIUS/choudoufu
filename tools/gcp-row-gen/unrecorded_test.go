// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestUnrecordedFrom pins the order of the rules that decide whether a
// create at a held identity collides (unrecorded.go).
func TestUnrecordedFrom(t *testing.T) {
	for name, tc := range map[string]struct {
		src          Source
		creates      bool
		unclassified bool
	}{
		"iam member":                {src: Source{Kind: "iam_member"}, creates: true},
		"adopts what it finds":      {src: Source{Kind: "resource", CreateFunc: "c", CreateAdopts: true, CreateNewCalls: []string{"Create"}}, creates: true},
		"post to a collection":      {src: Source{Kind: "resource", CreateFunc: "c", CreateMethods: []string{"POST"}}},
		"patch in place":            {src: Source{Kind: "resource", CreateFunc: "c", CreateMethods: []string{"GET", "PATCH"}}, creates: true},
		"patch that refuses":        {src: Source{Kind: "resource", CreateFunc: "c", CreateMethods: []string{"PATCH"}, CreateRefusesExisting: "existing object already found"}},
		"get then post":             {src: Source{Kind: "resource", CreateFunc: "c", CreateMethods: []string{"GET", "POST"}}},
		"custom set":                {src: Source{Kind: "resource", CreateFunc: "c", CreateMethods: []string{"POST"}, CreateCustom: "setAddons"}, creates: true},
		"custom setUp is a create":  {src: Source{Kind: "resource", CreateFunc: "c", CreateMethods: []string{"POST"}, CreateCustom: "setUpDataConnectorV2"}},
		"upsert":                    {src: Source{Kind: "resource", CreateFunc: "resourceXCreateOrUpdate"}, creates: true},
		"set call, nothing created": {src: Source{Kind: "resource", CreateFunc: "c", CreateSetCalls: []string{"setOrgPolicy"}}, creates: true},
		"insert call":               {src: Source{Kind: "resource", CreateFunc: "c", CreateNewCalls: []string{"Insert"}}},
		"nothing read":              {src: Source{Kind: "resource", CreateFunc: "c"}, unclassified: true},
		"no create in file":         {src: Source{Kind: "resource"}, unclassified: true},
	} {
		creates, _, unclassified := unrecordedFrom(tc.src)
		if creates != tc.creates || unclassified != tc.unclassified {
			t.Errorf("%s: creates %v unclassified %v, want %v %v", name, creates, unclassified, tc.creates, tc.unclassified)
		}
	}
}
