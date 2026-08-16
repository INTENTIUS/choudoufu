// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestArgumentReferenceNamesAnyDepthReachesNestedBlocks pins the one
// difference from argumentReferenceNames, and pins that the narrower
// function is unchanged: a nested block's arguments are collected here and
// still absent there.
func TestArgumentReferenceNamesAnyDepthReachesNestedBlocks(t *testing.T) {
	doc := "## Argument Reference\n\n" +
		"* `domain_arn` - (Required) ARN of the domain.\n" +
		"* `vpc_options` - (Required) Options block.\n\n" +
		"### vpc_options\n\n" +
		"* `subnet_ids` - (Required) Subnets.\n\n" +
		"#### deeper\n\n" +
		"* `availability_zones` - (Optional) Zones.\n\n" +
		"## Attribute Reference\n\n" +
		"* `id` - The unique identifier.\n"

	anyDepth := argumentReferenceNamesAnyDepth(doc)
	for _, want := range []string{"domain_arn", "vpc_options", "subnet_ids", "availability_zones"} {
		if !containsString(anyDepth, want) {
			t.Errorf("argumentReferenceNamesAnyDepth = %v, missing %q", anyDepth, want)
		}
	}
	if containsString(anyDepth, "id") {
		t.Errorf("argumentReferenceNamesAnyDepth = %v; it must stop at the next ## heading, not run into "+
			"the Attribute Reference", anyDepth)
	}
	if top := argumentReferenceNames(doc); containsString(top, "subnet_ids") {
		t.Errorf("argumentReferenceNames = %v; the top-level reader must be unchanged by the wider sibling", top)
	}
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// TestSoleIDPartAttribution runs the whole sole-segment reader over
// documents built from the shapes the 6.59.0 cache actually carries. Each
// case names a real page, but nothing here reads a type name: the reader
// takes the type as an argument like every other function in this file.
func TestSoleIDPartAttribution(t *testing.T) {
	colon := ":"
	for _, tc := range []struct {
		name    string
		tfType  string
		sep     *string
		example string
		section string
		args    []ArgumentRefEntry
		anyArgs []string
		attrs   []string
		descs   map[string]string
		want    *IDPart
	}{
		{
			name:    "bare id, description names the resource's own identifier",
			tfType:  "aws_opensearch_vpc_endpoint",
			example: "endpoint-id",
			section: "Import OpenSearch VPC endpoints using the `id`. For example:",
			args:    []ArgumentRefEntry{{Name: "domain_arn", Required: true}},
			attrs:   []string{"id", "endpoint"},
			descs:   map[string]string{"id": "The unique identifier of the endpoint."},
			want:    &IDPart{Token: "id", Source: idPartSourceOwnID},
		},
		{
			name:    "plain prose naming the resource's own identifier",
			tfType:  "aws_opensearch_outbound_connection",
			example: "connection-id",
			section: "Import AWS Opensearch Outbound Connections using the Outbound Connection ID. For example:",
			args:    []ArgumentRefEntry{{Name: "connection_alias", Required: true}},
			attrs:   []string{"id"},
			want:    &IDPart{Token: "the Outbound Connection ID", Source: idPartSourceOwnID},
		},
		{
			name:    "bare id, description names a cloud property",
			tfType:  "aws_backup_global_settings",
			example: "123456789012",
			section: "Import Backup Global Settings using the `id`. For example:",
			attrs:   []string{"id"},
			descs:   map[string]string{"id": "The AWS Account ID."},
			want:    nil,
		},
		{
			name:    "bare id, description names something that is not this resource",
			tfType:  "aws_redshift_snapshot_copy",
			example: "cluster-id",
			section: "Import Redshift Snapshot Copy using the `id`. For example:",
			attrs:   []string{"id"},
			descs:   map[string]string{"id": "Identifier of the source cluster."},
			want:    nil,
		},
		{
			name:    "qualified id names somebody else's id",
			tfType:  "aws_vpc_peering_connection_options",
			example: "pcx-1234",
			section: "Import VPC Peering Connection Options using the VPC peering `id`. For example:",
			args:    []ArgumentRefEntry{{Name: "vpc_peering_connection_id", Required: true}},
			attrs:   []string{"id"},
			descs:   map[string]string{"id": "The ID of the VPC Peering Connection Options."},
			want:    nil,
		},
		{
			name:    "the doc quotes the literal it wants pasted",
			tfType:  "aws_spot_datafeed_subscription",
			example: "spot-datafeed-subscription",
			section: "Import a Spot Datafeed Subscription using the word `spot-datafeed-subscription`. For example:",
			args:    []ArgumentRefEntry{{Name: "bucket", Required: true}},
			want:    nil,
		},
		{
			name:    "an exported value the doc says is always the same constant",
			tfType:  "aws_config_retention_configuration",
			example: "default",
			section: "Import Config Retention Configurations using the `name`. For example:",
			args:    []ArgumentRefEntry{{Name: "retention_period_in_days", Required: true}},
			attrs:   []string{"name"},
			descs:   map[string]string{"name": "The name of the retention configuration object. The object is always named **default**."},
			want:    nil,
		},
		{
			name:    "a nested block's argument is configuration, not the server",
			tfType:  "aws_eks_identity_provider_config",
			example: "example",
			section: "Import using the `identity_provider_config_name`. For example:",
			args:    []ArgumentRefEntry{{Name: "oidc", Required: true}},
			anyArgs: []string{"oidc", "identity_provider_config_name"},
			attrs:   []string{"identity_provider_config_name"},
			want:    &IDPart{Token: "identity_provider_config_name", Source: idPartSourceArgument},
		},
		{
			name:    "a resolved separator means the doc already said the ID has parts",
			tfType:  "aws_connect_queue",
			sep:     &colon,
			example: "instanceid:queueid",
			section: "Import using the `queue_id`. For example:",
			attrs:   []string{"queue_id"},
			want:    nil,
		},
		{
			name:    "a separator character in the example says the same thing",
			tfType:  "aws_connect_queue",
			example: "instanceid:queueid",
			section: "Import using the `queue_id`. For example:",
			attrs:   []string{"queue_id"},
			want:    nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := soleIDPart(tc.section, tc.tfType, tc.sep, tc.example, tc.args, tc.anyArgs, tc.attrs, tc.descs)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("soleIDPart = %+v, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("soleIDPart = nil, want %+v", *tc.want)
			case tc.want != nil && (got.Token != tc.want.Token || got.Source != tc.want.Source):
				t.Fatalf("soleIDPart = %+v, want %+v", *got, *tc.want)
			}
		})
	}
}

// TestAttributeDescriptionsStopsAtTheSectionBoundary is the boundary rule
// this file inherits from attributeReferenceNames: a nested block's own
// exports are not the resource's top-level ones, and the next "## " heading
// ends the section.
func TestAttributeDescriptionsStopsAtTheSectionBoundary(t *testing.T) {
	doc := "## Attribute Reference\n\n" +
		"* `id` - The widget ID.\n\n" +
		"### nested\n\n" +
		"* `inner` - Something else.\n\n" +
		"## Timeouts\n\n" +
		"* `create` - (Default `60m`)\n"
	got := attributeDescriptions(doc)
	if got["id"] != "The widget ID." {
		t.Errorf("attributeDescriptions[id] = %q, want %q", got["id"], "The widget ID.")
	}
	for _, unwanted := range []string{"inner", "create"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("attributeDescriptions carries %q; the span must end at the first sub-heading and the "+
				"next ## heading", unwanted)
		}
	}
}
