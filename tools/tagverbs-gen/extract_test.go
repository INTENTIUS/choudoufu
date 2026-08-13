// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Ground-truth tests over trimmed real botocore/botocore service-2.json
// excerpts, fetched at the pinned tag (1.43.70) and copied in by hand -
// every shape here matches what the real file at
// data/<service>/<version>/service-2.json actually declares, checked before
// being written down, the same discipline
// tools/importdocs-gen/groundtruth_test.go's doc comment holds itself to.
package main

import "testing"

// ec2ModelJSON is CreateTags from data/ec2/2016-11-15/service-2.json,
// trimmed to the operations and shapes classifyTagOps reads, alongside its
// UpdateCapacityManagerMonitoredTagKeys decoy (a real operation whose name
// also contains "Tag" but whose input carries no *Tags-suffixed member at
// all).
const ec2ModelJSON = `{
  "operations": {
    "CreateTags": {"input": {"shape": "CreateTagsRequest"}},
    "UpdateCapacityManagerMonitoredTagKeys": {"input": {"shape": "UpdateCapacityManagerMonitoredTagKeysRequest"}}
  },
  "shapes": {
    "CreateTagsRequest": {"type": "structure", "members": {
      "DryRun": {"shape": "Boolean"},
      "Resources": {"shape": "ResourceIdList"},
      "Tags": {"shape": "TagList"}
    }},
    "UpdateCapacityManagerMonitoredTagKeysRequest": {"type": "structure", "members": {
      "ActivateTagKeys": {"shape": "TagKeyList"},
      "DeactivateTagKeys": {"shape": "TagKeyList"},
      "DryRun": {"shape": "Boolean"},
      "ClientToken": {"shape": "String"}
    }},
    "ResourceIdList": {"type": "list", "member": {"shape": "TaggableResourceId"}},
    "TaggableResourceId": {"type": "string"},
    "TagList": {"type": "list", "member": {"shape": "Tag"}},
    "Tag": {"type": "structure", "members": {"Key": {"shape": "String"}, "Value": {"shape": "String"}}},
    "TagKeyList": {"type": "list", "member": {"shape": "String"}},
    "Boolean": {"type": "boolean"},
    "String": {"type": "string"}
  }
}`

func mustParse(t *testing.T, raw string) *serviceModel {
	t.Helper()
	m, err := parseServiceModel([]byte(raw))
	if err != nil {
		t.Fatalf("parseServiceModel: %v", err)
	}
	return m
}

// TestGroundTruth_EC2CreateTags: the decoy is filtered out (no *Tags-suffixed
// member), CreateTags survives with Resources as its sole extra member and a
// Key/Value list-shaped Tags - the exact shape the pinned
// TestClassifyAdoptionHintIsPasteable output depends on.
func TestGroundTruth_EC2CreateTags(t *testing.T) {
	m := mustParse(t, ec2ModelJSON)
	candidates := classifyTagOps(m)
	if len(candidates) != 1 || candidates[0].Name != "CreateTags" {
		t.Fatalf("candidates = %v, want exactly [CreateTags]", candidates)
	}
	c := candidates[0]
	if len(c.ExtraMembers) != 1 || c.ExtraMembers[0] != "Resources" {
		t.Fatalf("ExtraMembers = %v, want [Resources]", c.ExtraMembers)
	}

	row := classifyRow("EC2", "ec2", "2016-11-15", m)
	if !row.Composable {
		t.Fatalf("row not composable: %+v", row)
	}
	if row.Operation != "CreateTags" || row.ResourceArg != "Resources" || !row.ResourceArgIsList {
		t.Errorf("row = %+v", row)
	}
	if row.TagsShape != "list" || row.TagKeyField != "Key" || row.TagValueField != "Value" {
		t.Errorf("tags shape = %q key=%q value=%q, want list/Key/Value", row.TagsShape, row.TagKeyField, row.TagValueField)
	}
}

// kmsModelJSON is TagResource from data/kms/2014-11-01/service-2.json:
// KeyId + a TagKey/TagValue list, not EC2's Key/Value.
const kmsModelJSON = `{
  "operations": {"TagResource": {"input": {"shape": "TagResourceRequest"}}},
  "shapes": {
    "TagResourceRequest": {"type": "structure", "members": {
      "KeyId": {"shape": "KeyIdType"}, "Tags": {"shape": "TagList"}
    }},
    "KeyIdType": {"type": "string"},
    "TagList": {"type": "list", "member": {"shape": "Tag"}},
    "Tag": {"type": "structure", "members": {"TagKey": {"shape": "TagKeyType"}, "TagValue": {"shape": "TagValueType"}}},
    "TagKeyType": {"type": "string"},
    "TagValueType": {"type": "string"}
  }
}`

func TestGroundTruth_KMSTagResource(t *testing.T) {
	m := mustParse(t, kmsModelJSON)
	row := classifyRow("KMS", "kms", "2014-11-01", m)
	if !row.Composable || row.ResourceArg != "KeyId" || row.ResourceArgIsList {
		t.Errorf("row = %+v", row)
	}
	if row.TagKeyField != "TagKey" || row.TagValueField != "TagValue" {
		t.Errorf("key/value fields = %q/%q, want TagKey/TagValue", row.TagKeyField, row.TagValueField)
	}
}

// route53ModelJSON is ChangeTagsForResource from
// data/route53/2013-04-01/service-2.json: a two-part ResourceType +
// ResourceId identity, plus AddTags/RemoveTagKeys - three extra members
// besides the tags-suffixed one once RemoveTagKeys (itself ending in "Tags")
// is set aside as a second candidate for the "*tags" suffix scan. This
// fixture pins that AddTags (not RemoveTagKeys) is the one picked, and that
// three non-composable extra members refuse composition rather than guess
// which one is the identifier.
const route53ModelJSON = `{
  "operations": {"ChangeTagsForResource": {"input": {"shape": "ChangeTagsForResourceRequest"}}},
  "shapes": {
    "ChangeTagsForResourceRequest": {"type": "structure", "members": {
      "ResourceType": {"shape": "TagResourceType"},
      "ResourceId": {"shape": "TagResourceId"},
      "AddTags": {"shape": "TagList"},
      "RemoveTagKeys": {"shape": "TagKeyList"}
    }},
    "TagResourceType": {"type": "string"},
    "TagResourceId": {"type": "string"},
    "TagList": {"type": "list", "member": {"shape": "Tag"}},
    "Tag": {"type": "structure", "members": {"Key": {"shape": "String"}, "Value": {"shape": "String"}}},
    "TagKeyList": {"type": "list", "member": {"shape": "String"}},
    "String": {"type": "string"}
  }
}`

func TestGroundTruth_Route53KnownButNotComposable(t *testing.T) {
	m := mustParse(t, route53ModelJSON)
	row := classifyRow("Route53", "route53", "2013-04-01", m)
	if row.Operation != "ChangeTagsForResource" {
		t.Fatalf("Operation = %q, want ChangeTagsForResource", row.Operation)
	}
	if row.Ambiguous {
		t.Errorf("row wrongly marked ambiguous: %+v", row)
	}
	if row.Composable {
		t.Errorf("row wrongly marked composable: %+v", row)
	}
	if row.TagsArg != "AddTags" {
		t.Errorf("TagsArg = %q, want AddTags (RemoveTagKeys sorts after it and is not itself a tags-bearing member)", row.TagsArg)
	}
	if row.Reason == "" {
		t.Error("Reason is empty for a non-composable row")
	}
}

// sqsModelJSON is TagQueue from data/sqs/2012-11-05/service-2.json: QueueUrl
// plus a flat map-shaped Tags, not a list.
const sqsModelJSON = `{
  "operations": {"TagQueue": {"input": {"shape": "TagQueueRequest"}}},
  "shapes": {
    "TagQueueRequest": {"type": "structure", "members": {
      "QueueUrl": {"shape": "String"}, "Tags": {"shape": "TagMap"}
    }},
    "TagMap": {"type": "map", "key": {"shape": "TagKey"}, "value": {"shape": "TagValue"}},
    "TagKey": {"type": "string"},
    "TagValue": {"type": "string"},
    "String": {"type": "string"}
  }
}`

func TestGroundTruth_SQSMapShapedTags(t *testing.T) {
	m := mustParse(t, sqsModelJSON)
	row := classifyRow("SQS", "sqs", "2012-11-05", m)
	if !row.Composable {
		t.Fatalf("row not composable: %+v", row)
	}
	if row.TagsShape != "map" {
		t.Errorf("TagsShape = %q, want map", row.TagsShape)
	}
	if row.TagKeyField != "" || row.TagValueField != "" {
		t.Errorf("a map shape should carry no key/value field names, got %q/%q", row.TagKeyField, row.TagValueField)
	}
	if row.ResourceArg != "QueueUrl" {
		t.Errorf("ResourceArg = %q, want QueueUrl", row.ResourceArg)
	}
}

// s3ModelJSON is PutBucketTagging from data/s3/2006-03-01/service-2.json:
// tags travel through a "Tagging" wrapper member, which does not end in
// "tags" and so is never picked up as a tags-bearing member at all - S3 has
// no operation this tool's pattern recognizes as a tagging write.
const s3ModelJSON = `{
  "operations": {"PutBucketTagging": {"input": {"shape": "PutBucketTaggingRequest"}}},
  "shapes": {
    "PutBucketTaggingRequest": {"type": "structure", "members": {
      "Bucket": {"shape": "String"}, "Tagging": {"shape": "Tagging"}
    }},
    "Tagging": {"type": "structure", "members": {"TagSet": {"shape": "TagList"}}},
    "TagList": {"type": "list", "member": {"shape": "Tag"}},
    "Tag": {"type": "structure", "members": {"Key": {"shape": "String"}, "Value": {"shape": "String"}}},
    "String": {"type": "string"}
  }
}`

func TestGroundTruth_S3None(t *testing.T) {
	m := mustParse(t, s3ModelJSON)
	row := classifyRow("S3", "s3", "2006-03-01", m)
	if row.Operation != "" || row.Ambiguous || row.Composable {
		t.Errorf("row = %+v, want an empty/none verdict", row)
	}
	if row.Reason == "" {
		t.Error("Reason is empty for a none row")
	}
}

// iamModelJSON is a trimmed slice of data/iam/2010-05-08/service-2.json:
// three of its eight real per-entity Tag<X> operations, enough to exercise
// the ambiguous-candidate path without transcribing all eight.
const iamModelJSON = `{
  "operations": {
    "TagRole": {"input": {"shape": "TagRoleRequest"}},
    "TagUser": {"input": {"shape": "TagUserRequest"}},
    "TagInstanceProfile": {"input": {"shape": "TagInstanceProfileRequest"}},
    "ListRoleTags": {"input": {"shape": "ListRoleTagsRequest"}},
    "UntagRole": {"input": {"shape": "UntagRoleRequest"}}
  },
  "shapes": {
    "TagRoleRequest": {"type": "structure", "members": {"RoleName": {"shape": "String"}, "Tags": {"shape": "TagList"}}},
    "TagUserRequest": {"type": "structure", "members": {"UserName": {"shape": "String"}, "Tags": {"shape": "TagList"}}},
    "TagInstanceProfileRequest": {"type": "structure", "members": {"InstanceProfileName": {"shape": "String"}, "Tags": {"shape": "TagList"}}},
    "ListRoleTagsRequest": {"type": "structure", "members": {"RoleName": {"shape": "String"}}},
    "UntagRoleRequest": {"type": "structure", "members": {"RoleName": {"shape": "String"}, "TagKeys": {"shape": "TagKeyList"}}},
    "TagList": {"type": "list", "member": {"shape": "Tag"}},
    "Tag": {"type": "structure", "members": {"Key": {"shape": "String"}, "Value": {"shape": "String"}}},
    "TagKeyList": {"type": "list", "member": {"shape": "String"}},
    "String": {"type": "string"}
  }
}`

func TestGroundTruth_IAMAmbiguous(t *testing.T) {
	m := mustParse(t, iamModelJSON)
	row := classifyRow("IAM", "iam", "2010-05-08", m)
	if !row.Ambiguous {
		t.Fatalf("row not marked ambiguous: %+v", row)
	}
	if row.Operation != "" || row.Composable {
		t.Errorf("an ambiguous row must carry no single Operation and never be Composable: %+v", row)
	}
	want := []string{"TagInstanceProfile", "TagRole", "TagUser"}
	if len(row.Candidates) != len(want) {
		t.Fatalf("Candidates = %v, want %v", row.Candidates, want)
	}
	for i, w := range want {
		if row.Candidates[i] != w {
			t.Errorf("Candidates[%d] = %q, want %q (ListRoleTags/UntagRole must be excluded)", i, row.Candidates[i], w)
		}
	}
}
