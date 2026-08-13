// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import "context"

// The Resource Groups Tagging API's own SigV4 service/host segment and
// X-Amz-Target namespace, for [NewTagging]. Same AWS JSON 1.0 shape as
// Cloud Control (doc.go), different service: tagging.<region>.amazonaws.com
// and ResourceGroupsTaggingAPI_20170126.<Operation>.
const (
	taggingService      = "tagging"
	taggingTargetPrefix = "ResourceGroupsTaggingAPI_20170126"
)

// NewTagging builds a Client for the Resource Groups Tagging API's
// GetResources operation (issue #47's estate-wide sweep primitive) instead
// of Cloud Control's ListResources/GetResource, reusing the same
// endpoint-override and signing rules [New] and doc.go document. Only
// [Client.GetResources] is meaningful on the result; ListResources and
// GetResource would send Cloud Control's operations against the tagging
// host and get nowhere.
func NewTagging(cfg Config) *Client {
	c := newClient(cfg)
	c.service = taggingService
	c.opTargetPrefix = taggingTargetPrefix
	return c
}

// TagFilter is one GetResources tag-filter term: match anything carrying
// Key, optionally narrowed to one of Values (an empty Values matches the key
// with any value, or no value at all).
type TagFilter struct {
	Key    string
	Values []string
}

// TaggedResource is one GetResources hit: a resource ARN and the tags it
// carries.
//
// What this type deliberately does not do is turn ResourceARN into a
// (resource type, identifier) pair discovery could bind against. An ARN's
// service and resource segments would need to be joined against
// live/mapping.json and live/registry.json the same way
// internal/live/discovery's per-type Cloud Control path already does with a
// CFN type name in hand, and that join is scoped out of the #47 batch - see
// the TODO on [Client.GetResources]. This type carries the raw material
// (the ARN, the tags) for whichever caller takes that on.
type TaggedResource struct {
	ResourceARN string
	Tags        map[string]string
}

// wireGetResourcesResponse is GetResources' response exactly as the
// Resource Groups Tagging API's JSON 1.0 shapes it.
type wireGetResourcesResponse struct {
	ResourceTagMappingList []wireResourceTagMapping `json:"ResourceTagMappingList"`
	PaginationToken        string                   `json:"PaginationToken"`
}

type wireResourceTagMapping struct {
	ResourceARN string    `json:"ResourceARN"`
	Tags        []wireTag `json:"Tags"`
}

type wireTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// GetResources enumerates every resource carrying tagFilters, across every
// resource type unless resourceTypeFilters narrows it, paginating
// PaginationToken to exhaustion - the Resource Groups Tagging API's
// estate-wide sweep: one paginated call returns every ARN carrying an
// estate's tofu-estate marker, rather than a Cloud Control ListResources
// call per admitted type (internal/live/discovery's sweepTypes).
//
// TODO(#47): nothing in this package or in internal/live/discovery calls
// this yet. Turning a TaggedResource's ARN into the (resource type,
// identifier) pair the marker-discovery bind step needs - parsing the ARN's
// service and resource segments and joining them against
// live/mapping.json/live/registry.json - is the piece #47 scoped out as a
// stretch goal; see the issue comment recording that decision. The client
// call itself (this function, tested against a fake server in
// tagging_test.go) is the part #47 commits to.
func (c *Client) GetResources(ctx context.Context, resourceTypeFilters []string, tagFilters []TagFilter) ([]TaggedResource, error) {
	var out []TaggedResource
	var token string
	for {
		payload := struct {
			ResourceTypeFilters []string           `json:"ResourceTypeFilters,omitempty"`
			TagFilters          []wireTagFilterOut `json:"TagFilters,omitempty"`
			PaginationToken     string             `json:"PaginationToken,omitempty"`
		}{
			ResourceTypeFilters: resourceTypeFilters,
			TagFilters:          toWireTagFilters(tagFilters),
			PaginationToken:     token,
		}

		var resp wireGetResourcesResponse
		if err := c.call(ctx, "GetResources", payload, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.ResourceTagMappingList {
			out = append(out, TaggedResource{
				ResourceARN: m.ResourceARN,
				Tags:        tagsOf(m.Tags),
			})
		}
		if resp.PaginationToken == "" {
			return out, nil
		}
		token = resp.PaginationToken
	}
}

// wireTagFilterOut is TagFilter exactly as GetResources' request wants it on
// the wire.
type wireTagFilterOut struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values,omitempty"`
}

func toWireTagFilters(filters []TagFilter) []wireTagFilterOut {
	if len(filters) == 0 {
		return nil
	}
	out := make([]wireTagFilterOut, len(filters))
	for i, f := range filters {
		out[i] = wireTagFilterOut{Key: f.Key, Values: f.Values}
	}
	return out
}

func tagsOf(wire []wireTag) map[string]string {
	if len(wire) == 0 {
		return nil
	}
	out := make(map[string]string, len(wire))
	for _, t := range wire {
		out[t.Key] = t.Value
	}
	return out
}
