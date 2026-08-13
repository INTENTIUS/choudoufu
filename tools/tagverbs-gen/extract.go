// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// shapeRef is a botocore shape reference: {"shape": "SomeShapeName"}, the
// form every operation input/output and every shape member uses to point at
// another shape.
type shapeRef struct {
	Shape string `json:"shape"`
}

// shape is the slice of one botocore shape's declaration this tool reads:
// its own type, its members (a structure), or its element/key/value
// (a list or a map) - service-2.json's shapes carry a great deal more
// (documentation, enums, error traits), all ignored here.
type shape struct {
	Type    string              `json:"type"`
	Members map[string]shapeRef `json:"members"`
	Member  *shapeRef           `json:"member"` // list element shape
	Key     *shapeRef           `json:"key"`    // map key shape
	Value   *shapeRef           `json:"value"`  // map value shape
}

// operation is the slice of one botocore operation's declaration this tool
// reads: its input shape, when it has one.
type operation struct {
	Input *shapeRef `json:"input"`
}

// serviceModel is the slice of one service-2.json this tool reads.
type serviceModel struct {
	Operations map[string]operation `json:"operations"`
	Shapes     map[string]shape     `json:"shapes"`
}

// parseServiceModel decodes one fetched service-2.json.
func parseServiceModel(data []byte) (*serviceModel, error) {
	var m serviceModel
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decoding the service model: %w", err)
	}
	return &m, nil
}

// tagOpNameRe matches an operation name that looks tag-ish at all - the
// coarse first filter; excludeOpNameRe and the input-shape check below do
// the actual discrimination.
var tagOpNameRe = regexp.MustCompile(`(?i)tag`)

// excludeOpNameRe rules out the read and removal verbs that also contain
// "tag" in their name: listing/reading existing tags is not the write this
// tool is looking for, and untagging is the opposite operation.
var excludeOpNameRe = regexp.MustCompile(`(?i)^(untag|list|get|describe|delete|remove|put)`)

// tagsMemberDenyList is member names never treated as the "extra" resource
// argument even when they survive every other filter - request-shaping
// flags with no bearing on identity or tags.
var tagsMemberDenyList = map[string]bool{
	"dryrun":      true,
	"clienttoken": true,
}

// tagCandidate is one operation classifyTagOps found tag-ish, with its
// input shape's members already split into the tags-bearing one and
// whatever else remains.
type tagCandidate struct {
	Name         string
	TagsMember   string
	ExtraMembers []string // sorted, deny-listed members already excluded
	InputMembers int
}

// classifyTagOps finds every operation in model whose name looks tag-ish,
// is not a read/removal verb, and whose input shape carries a member ending
// in "tags" (case-insensitive - covers "Tags", "tags", and route53's
// "AddTags") alongside at least one other member: the resource identifier
// the tags are written onto. Returned sorted by operation name, for a
// deterministic single-candidate pick and a deterministic ambiguous list.
func classifyTagOps(model *serviceModel) []tagCandidate {
	var out []tagCandidate
	for name, op := range model.Operations {
		if !tagOpNameRe.MatchString(name) || excludeOpNameRe.MatchString(name) {
			continue
		}
		if op.Input == nil {
			continue
		}
		inputShape, ok := model.Shapes[op.Input.Shape]
		if !ok || len(inputShape.Members) < 2 {
			continue
		}

		memberNames := make([]string, 0, len(inputShape.Members))
		for m := range inputShape.Members {
			memberNames = append(memberNames, m)
		}
		sort.Strings(memberNames)

		tagsMember := ""
		for _, m := range memberNames {
			if strings.HasSuffix(strings.ToLower(m), "tags") {
				tagsMember = m
				break
			}
		}
		if tagsMember == "" {
			continue
		}

		var extra []string
		for _, m := range memberNames {
			if m == tagsMember || tagsMemberDenyList[strings.ToLower(m)] {
				continue
			}
			extra = append(extra, m)
		}

		out = append(out, tagCandidate{
			Name:         name,
			TagsMember:   tagsMember,
			ExtraMembers: extra,
			InputMembers: len(memberNames),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// resolveTagsShape inspects the tags-bearing member's own shape and reports
// how it is composed: "list" (a list of a two-field struct - Key/Value,
// TagKey/TagValue, key/value all sort the field carrying the tag's own key
// before the one carrying its value, alphabetically, in every case this
// tool has observed) or "map" (a flat string-to-string map, SQS's TagQueue
// and Lambda's TagResource). Any other shape (S3's Tagging wrapper is
// neither) reports ok=false - nothing here is guessed.
func resolveTagsShape(model *serviceModel, tagsMemberShapeName string) (shapeKind, keyField, valueField string, ok bool) {
	s, found := model.Shapes[tagsMemberShapeName]
	if !found {
		return "", "", "", false
	}
	switch s.Type {
	case "list":
		if s.Member == nil {
			return "", "", "", false
		}
		elem, found := model.Shapes[s.Member.Shape]
		if !found || len(elem.Members) != 2 {
			return "", "", "", false
		}
		fields := make([]string, 0, 2)
		for m := range elem.Members {
			fields = append(fields, m)
		}
		sort.Strings(fields)
		return "list", fields[0], fields[1], true
	case "map":
		return "map", "", "", true
	default:
		return "", "", "", false
	}
}

// resolveResourceArg inspects a candidate's sole extra member and reports
// whether it is a plain scalar identifier a caller can pass one import ID
// through: either a string shape directly, or a list whose element shape is
// a string (EC2's Resources, ELBv2's ResourceArns - both APIs that accept
// several resources per call but which this tool only ever hands one ID,
// the same way the hand-written ec2Types hint always did).
func resolveResourceArg(model *serviceModel, memberShapeName string) (isList bool, ok bool) {
	s, found := model.Shapes[memberShapeName]
	if !found {
		return false, false
	}
	switch s.Type {
	case "string":
		return false, true
	case "list":
		if s.Member == nil {
			return false, false
		}
		elem, found := model.Shapes[s.Member.Shape]
		if !found || elem.Type != "string" {
			return false, false
		}
		return true, true
	default:
		return false, false
	}
}
