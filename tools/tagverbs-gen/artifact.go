// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Row is one CFN service segment's tagging-operation verdict.
type Row struct {
	// Service is the CFN type-name segment (AWS::Lambda::Function ->
	// "Lambda") this row is about - live/mapping.json's join key, the same
	// one tools/row-gen/classify.go's serviceOf produces.
	Service string `json:"service"`

	// BotocoreDirectory and BotocoreVersion name the resolved botocore
	// service model this row was extracted from - data/<directory>/
	// <version>/service-2.json at the pinned tag. Both empty when
	// DirectoryFound is false.
	BotocoreDirectory string `json:"botocore_directory,omitempty"`
	BotocoreVersion   string `json:"botocore_version,omitempty"`

	// DirectoryFound is whether resolveDirectory joined Service to a real
	// botocore directory at all - false means this row's Reason explains a
	// join failure, not a tagging-operation verdict, and every field below
	// is zero.
	DirectoryFound bool `json:"directory_found"`

	// Operation is the botocore operation name that tags this service's
	// resources (e.g. "CreateTags", "TagResource", "ChangeTagsForResource"),
	// empty when none was found or more than one candidate was ambiguous.
	Operation string `json:"operation,omitempty"`

	// Ambiguous is true when more than one operation in this service's
	// model looked tag-ish with a resource+tags shaped input - IAM's eight
	// per-entity Tag<X> operations, ACM's AddTagsToCertificate and
	// TagResource both, CloudWatch Logs' TagLogGroup and TagResource both.
	// Operation is empty whenever this is true; Candidates names what was
	// found instead.
	Ambiguous bool `json:"ambiguous"`

	// Candidates lists every tag-ish operation classifyTagOps found for
	// this service, sorted - one name when Operation is set, more than one
	// when Ambiguous, none when the service has no tagging write at all
	// (S3, which tags through a Tagging wrapper, not a *Tags member).
	Candidates []string `json:"candidates,omitempty"`

	// ResourceArg is the operation's own input member naming the resource
	// to tag (e.g. "Resources", "KeyId", "ResourceArn"), raw as botocore
	// spells it - the composer applies its own casing transform. Empty
	// unless Operation is set and resolveResourceArg found a plain scalar
	// identifier.
	ResourceArg string `json:"resource_arg,omitempty"`

	// ResourceArgIsList is whether ResourceArg's own shape is a list
	// (EC2's Resources, ELBv2's ResourceArns) rather than a bare string
	// (KMS's KeyId) - informational; either shape takes exactly one import
	// ID from this tool's caller.
	ResourceArgIsList bool `json:"resource_arg_is_list,omitempty"`

	// TagsArg is the operation's own input member carrying the tags to
	// write (e.g. "Tags", "AddTags", "tags"), raw as botocore spells it.
	TagsArg string `json:"tags_arg,omitempty"`

	// TagsShape is "list" (a list of a two-field key/value struct) or
	// "map" (a flat string-to-string map), whichever resolveTagsShape
	// found - empty when Operation is set but the tags member's own shape
	// did not resolve to either.
	TagsShape string `json:"tags_shape,omitempty"`

	// TagKeyField and TagValueField are the two-field struct's own member
	// names (Key/Value, TagKey/TagValue, key/value) when TagsShape is
	// "list"; both empty for "map" or when TagsShape is empty.
	TagKeyField   string `json:"tag_key_field,omitempty"`
	TagValueField string `json:"tag_value_field,omitempty"`

	// Composable is whether every field above resolved cleanly enough for
	// a generic CLI command to be built from them alone: exactly one
	// resource argument with a plain scalar shape, and a tags shape this
	// tool knows how to render. False for a service with a known Operation
	// this tool still cannot safely compose for (Route53's and SSM's two-
	// part ResourceType+ResourceId, where ResourceType's value is a
	// per-type enum this artifact has no way to know) - Reason explains
	// why.
	Composable bool `json:"composable"`

	// Reason is the human-readable account of why Operation is empty,
	// Ambiguous is true, or Composable is false - always present in one of
	// those cases, always empty when none of them applies.
	Reason string `json:"reason,omitempty"`
}

// Counts are the sweep-wide totals a reviewer reads off the header without
// scanning every row.
type Counts struct {
	// Services is how many distinct CFN services live/mapping.json's mapped
	// set named.
	Services int `json:"services"`
	// DirectoryFound / NoDirectory split Services by whether resolveDirectory
	// joined the CFN segment to a real botocore directory at all.
	DirectoryFound int `json:"directory_found"`
	NoDirectory    int `json:"no_directory"`
	// Single / Ambiguous / None split DirectoryFound by classifyTagOps'
	// candidate count: exactly one, more than one, or zero.
	Single    int `json:"single"`
	Ambiguous int `json:"ambiguous"`
	None      int `json:"none"`
	// Composable is how many Single rows resolveResourceArg and
	// resolveTagsShape both resolved cleanly - the rows an adoption hint
	// can actually build a full command from.
	Composable int `json:"composable"`
}

// Artifact is the committed file: live/tag-verbs.json.
type Artifact struct {
	// Source and SourceVersion pin the release every row's model was
	// fetched at, the same vocabulary tools/importdocs-gen's Provider/
	// ProviderVersion and tools/mapping-gen's mapping.json use.
	Source        string `json:"source"`
	SourceVersion string `json:"source_version"`

	// GeneratedBy names the tool so a reader of the JSON alone knows where
	// rows come from and how to refresh them.
	GeneratedBy string `json:"generated_by"`

	// Accepted is the ISO date a human ran the generator with -accept and
	// ratified the rows below - tools/importdocs-gen's Accepted vocabulary.
	// Empty unless -accept was passed on the run that produced this file.
	Accepted string `json:"accepted,omitempty"`

	Counts Counts `json:"counts"`

	// Rows has one entry per CFN service live/mapping.json's mapped set
	// named, sorted by Service.
	Rows []Row `json:"rows"`
}

// classifyRow builds one service's row from its already-fetched, already-
// parsed model.
func classifyRow(cfnService, dir, version string, model *serviceModel) Row {
	base := Row{
		Service:           cfnService,
		BotocoreDirectory: dir,
		BotocoreVersion:   version,
		DirectoryFound:    true,
	}

	candidates := classifyTagOps(model)
	switch {
	case len(candidates) == 0:
		base.Reason = "no operation in this service's model looks like a tagging write (a tag-ish, non-removal name whose input carries both a resource identifier and a *tags-suffixed member)"
		return base

	case len(candidates) > 1:
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.Name
		}
		base.Ambiguous = true
		base.Candidates = names
		base.Reason = fmt.Sprintf("more than one operation looks tag-ish with no way to pick one: %s", strings.Join(names, ", "))
		return base
	}

	c := candidates[0]
	base.Operation = c.Name
	base.Candidates = []string{c.Name}

	op := model.Operations[c.Name]
	inputShape := model.Shapes[op.Input.Shape]

	base.TagsArg = c.TagsMember
	if tagsShapeName, ok := inputShape.Members[c.TagsMember]; ok {
		if kind, key, value, resolved := resolveTagsShape(model, tagsShapeName.Shape); resolved {
			base.TagsShape, base.TagKeyField, base.TagValueField = kind, key, value
		}
	}

	resourceOK := false
	if len(c.ExtraMembers) == 1 {
		base.ResourceArg = c.ExtraMembers[0]
		if memberShape, ok := inputShape.Members[base.ResourceArg]; ok {
			if isList, resolved := resolveResourceArg(model, memberShape.Shape); resolved {
				base.ResourceArgIsList = isList
				resourceOK = true
			}
		}
	}

	base.Composable = resourceOK && base.TagsShape != ""
	if !base.Composable {
		switch {
		case len(c.ExtraMembers) != 1:
			base.Reason = fmt.Sprintf("the operation takes %d identifying arguments besides %s (%s), not the single scalar this tool can compose a command from",
				len(c.ExtraMembers), c.TagsMember, strings.Join(c.ExtraMembers, ", "))
		case !resourceOK:
			base.Reason = fmt.Sprintf("%s's own shape is not a plain scalar identifier or a list of one", base.ResourceArg)
		case base.TagsShape == "":
			base.Reason = fmt.Sprintf("%s's own shape is neither a two-field list nor a flat map this tool knows how to render", c.TagsMember)
		}
	}
	return base
}

// buildArtifact computes the header counts over already-sorted rows.
func buildArtifact(rows []Row) Artifact {
	art := Artifact{
		Source:        fmt.Sprintf("github.com/%s/%s", botocoreOwner, botocoreRepo),
		SourceVersion: botocoreTag,
		GeneratedBy:   "tools/tagverbs-gen (go run ./tools/tagverbs-gen)",
		Rows:          rows,
	}
	art.Counts.Services = len(rows)
	for _, r := range rows {
		if !r.DirectoryFound {
			art.Counts.NoDirectory++
			continue
		}
		art.Counts.DirectoryFound++
		switch {
		case r.Ambiguous:
			art.Counts.Ambiguous++
		case r.Operation == "":
			art.Counts.None++
		default:
			art.Counts.Single++
			if r.Composable {
				art.Counts.Composable++
			}
		}
	}
	return art
}

// marshal renders the artifact deterministically: two-space indent,
// trailing newline, no HTML escaping - the same shape every other live/*
// generator's marshal uses.
func (a Artifact) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
