// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// This file (package residue, colocated with residue.go for the same
// go:embed reason: an embed directive can only name files in its own
// package's directory, and live/tag-verbs.json lives here) is issue #52's
// botocore side: internal/live/foreign/classify.go's adoptionHint used to
// consult a ten-row hand-written ec2Types table naming which admitted types
// this package knew how to write a `aws ec2 create-tags` command for, with
// a doc comment enumerating - by hand - the per-service tag verbs it
// refused to model (kms:TagResource, elasticloadbalancing:AddTags,
// sqs:TagQueue, ...). [TagVerbForType] replaces both: it joins a TF type to
// its CFN service the same way [Lookup] does (reusing the embedded
// mapping.json this file's sibling residue.go already parses), then looks
// the service up in the embedded, botocore-derived live/tag-verbs.json
// (tools/tagverbs-gen). A service the artifact never resolved a single
// tagging operation for - ambiguous (IAM, ACM, CloudWatch Logs), none (S3),
// or unresolved to a botocore directory at all - reports Known=true with an
// empty Operation and a Reason, honestly, rather than silently answering
// nothing.
package residue

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed tag-verbs.json
var tagVerbsJSONBytes []byte

// tagVerbRow mirrors tools/tagverbs-gen/artifact.go's Row: live/tag-verbs.json's
// per-service shape, re-read here rather than imported, since that package
// is main (the same duplication residue.go's own mappingRow/registryType
// carry for the sibling artifacts).
type tagVerbRow struct {
	Service           string   `json:"service"`
	BotocoreDirectory string   `json:"botocore_directory"`
	DirectoryFound    bool     `json:"directory_found"`
	Operation         string   `json:"operation"`
	Ambiguous         bool     `json:"ambiguous"`
	Candidates        []string `json:"candidates"`
	ResourceArg       string   `json:"resource_arg"`
	ResourceArgIsList bool     `json:"resource_arg_is_list"`
	TagsArg           string   `json:"tags_arg"`
	TagsShape         string   `json:"tags_shape"`
	TagKeyField       string   `json:"tag_key_field"`
	TagValueField     string   `json:"tag_value_field"`
	Composable        bool     `json:"composable"`
	Reason            string   `json:"reason"`
}

type tagVerbsArtifact struct {
	Rows []tagVerbRow `json:"rows"`
}

// TagVerb is what live/tag-verbs.json's botocore-derived join can say about
// a TF type's adoption command: which CLI subcommand tags its live
// resources and how to spell the resource-identifier and tag-pair flags.
// The zero value means nothing was found; check Known before reading
// anything else.
type TagVerb struct {
	// Service is the CFN service segment this verdict is about (e.g.
	// "EC2", "KMS") - informational, and part of a composed command's own
	// wording in a diagnostic.
	Service string

	// Known is whether live/tag-verbs.json carries a row for Service at
	// all - true whenever [TagVerbForType]'s caller should trust this
	// value's other fields over falling back to "the artifact is stale,
	// regenerate it" (that fallback is what false means).
	Known bool

	// CLIService is the AWS CLI's own top-level command name for this
	// service (e.g. "ec2", "kms", "elbv2") - botocore's own data directory
	// name, which the AWS CLI derives its command groups from directly.
	CLIService string

	// Operation is the botocore operation name that tags this service's
	// resources (e.g. "CreateTags", "TagResource"), empty when none was
	// found or more than one candidate was ambiguous - see Reason.
	Operation string

	// Ambiguous is true when the artifact found more than one candidate
	// tagging operation for Service with nothing to pick one by (IAM's
	// eight per-entity Tag<X> operations, ACM's AddTagsToCertificate and
	// TagResource both, CloudWatch Logs' TagLogGroup and TagResource
	// both).
	Ambiguous bool

	// Composable is whether every field below resolved cleanly enough for
	// a caller to build a full adoption command: exactly one scalar
	// resource argument, and a tags shape this package knows how to
	// render. False (with Reason explaining why) for a service with a
	// known Operation this artifact still could not safely compose for -
	// Route53's and SSM's two-part ResourceType+ResourceId identity, whose
	// ResourceType value is a per-type enum no generic join can supply.
	Composable bool

	// ResourceArg and TagsArg are the operation's own input member names,
	// raw as botocore spells them (e.g. "Resources", "KeyId", "Tags") -
	// [XformName] turns either into its AWS CLI flag spelling.
	ResourceArg string
	TagsArg     string

	// TagsShape is "list" (a list of a two-field key/value struct, in
	// which case TagKeyField and TagValueField name the struct's own two
	// members) or "map" (a flat string-to-string map, in which case both
	// are empty).
	TagsShape     string
	TagKeyField   string
	TagValueField string

	// Reason explains why Operation is empty, Ambiguous is true, or
	// Composable is false. Always empty when none of those applies.
	Reason string
}

var (
	tagVerbsOnce      sync.Once
	tagVerbsByService map[string]tagVerbRow
)

func loadTagVerbs() map[string]tagVerbRow {
	tagVerbsOnce.Do(func() {
		var art tagVerbsArtifact
		if err := json.Unmarshal(tagVerbsJSONBytes, &art); err != nil {
			panic(fmt.Sprintf("residue: decoding embedded tag-verbs.json: %v", err))
		}
		tagVerbsByService = make(map[string]tagVerbRow, len(art.Rows))
		for _, r := range art.Rows {
			tagVerbsByService[r.Service] = r
		}
	})
	return tagVerbsByService
}

// TagVerbForType resolves tfType's CFN service (the same mapping.json join
// [Lookup] draws on) and looks it up in the embedded tag-verbs.json. ok is
// true only when tfType maps to a CFN type at all - false for a type
// mapping.json has no row for, or whose row's via is "none" or "fold" (no
// CFN service of its own to look a tag verb up for). A true ok says nothing
// about whether a usable verb was found; check the returned TagVerb's own
// Known/Operation/Composable fields for that.
func TagVerbForType(tfType string) (TagVerb, bool) {
	s := load()
	row, found := s.byTFType[tfType]
	if !found || row.CFNType == nil || *row.CFNType == "" {
		return TagVerb{}, false
	}
	service, ok := tagVerbServiceOf(*row.CFNType)
	if !ok {
		return TagVerb{}, false
	}

	verbs := loadTagVerbs()
	tv, known := verbs[service]
	out := TagVerb{Service: service, Known: known}
	if !known {
		return out, true
	}
	// botocore's own data directory name doubles as the AWS CLI's
	// top-level command for the service (elbv2, stepfunctions, acm, ... -
	// the CLI derives its command groups directly from this name), which
	// is why the artifact records it once as BotocoreDirectory rather than
	// a second, redundant CLI-service field.
	out.CLIService = tv.BotocoreDirectory
	out.Operation = tv.Operation
	out.Ambiguous = tv.Ambiguous
	out.Composable = tv.Composable
	out.ResourceArg = tv.ResourceArg
	out.TagsArg = tv.TagsArg
	out.TagsShape = tv.TagsShape
	out.TagKeyField = tv.TagKeyField
	out.TagValueField = tv.TagValueField
	out.Reason = tv.Reason
	return out, true
}

// tagVerbServiceOf pulls the CFN namespace segment out of a CFN type name
// (AWS::Lambda::Function -> "Lambda", true) - the same extraction
// tools/row-gen/classify.go's serviceOf and tools/tagverbs-gen/mapping.go's
// serviceOf perform, duplicated here for the same reason residue.go
// duplicates mappingRow rather than importing a main package.
func tagVerbServiceOf(cfnType string) (string, bool) {
	parts := strings.Split(cfnType, "::")
	if len(parts) < 2 || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// xformNameRe1 and xformNameRe2 are botocore's own xform_name algorithm
// (botocore/xform_name.py, via its C-derived regexes): insert a hyphen
// before a capitalized word run, then before a single capital following a
// lowercase letter or digit, before lowercasing everything. This is what
// turns a shape member name like "ResourceARN" into the AWS CLI's actual
// "--resource-arn" flag rather than a naive per-letter split
// ("--resource-a-r-n") - the CLI's own flag names are generated by this
// exact transform over the operation's input member names.
var (
	xformNameRe1 = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
	xformNameRe2 = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// XformName renders a botocore shape member name (PascalCase or
// camelCase, as the service model happens to spell it) as its AWS CLI flag
// name, without the leading "--".
func XformName(name string) string {
	s := xformNameRe1.ReplaceAllString(name, "$1-$2")
	s = xformNameRe2.ReplaceAllString(s, "$1-$2")
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c - 'A' + 'a'
		}
		out = append(out, c)
	}
	return string(out)
}
