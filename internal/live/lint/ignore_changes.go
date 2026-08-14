// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// checkIgnoreChanges rejects a lifecycle block that tells the run to discard
// changes to the tags the ownership markers live in.
//
// This is GitHub issue #103, and it is the quietest failure the live path
// had. The stamp pass writes tofu-estate and tofu-address into the
// resource's tags argument, the plan renders that as an in-place update, and
// then the core throws the change away because the author asked for tags to
// be ignored. Nothing warns. The resource is applied unmarked, the next
// run's discovery cannot find it, and every run after that proposes creating
// a duplicate of something that already exists.
//
// `ignore_changes = [tags]` is a common idiom, usually added for exactly the
// reason that makes it dangerous here: something outside Terraform writes
// tags on this resource. Under live markers, this tool is that something.
//
// Two shapes are refused and one is deliberately not:
//
//   - `ignore_changes = all` covers tags along with everything else.
//   - `ignore_changes = [tags]`, and `tags["tofu-estate"]` or any other
//     marker key named directly.
//   - `tags["Owner"]`, or any other non-marker key, is left alone. Ignoring
//     one tag this tool does not write is an ordinary thing to want, and
//     refusing it would be a refusal with no reason behind it.
//
// tags_all is not refused either. It is the provider's computed union of
// tags and the provider-level default_tags, so ignoring it does not stop the
// markers being written into tags, and the update still happens.
func checkIgnoreChanges(resource *configs.Resource, addr string, path addrs.Module, issues *[]Issue) {
	managed := resource.Managed
	if managed == nil {
		return
	}

	if managed.IgnoreAllChanges {
		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreChanges,
			Construct: fmt.Sprintf("lifecycle { ignore_changes = all } on %s", addr),
			Module:    path,
			Detail: fmt.Sprintf(
				"%s ignores every change, which includes the %s and %s tags this mode writes to record ownership. "+
					"They would be planned and then discarded, so the resource would be applied with no markers on it: "+
					"the next run could not discover it, and would propose creating a second copy. "+
					"Narrow ignore_changes to the arguments you actually mean, leaving tags out of it.",
				addr, markers.TagEstate, markers.TagAddress,
			),
			Subject: resource.DeclRange,
		})
		return
	}

	for _, traversal := range managed.IgnoreChanges {
		key, whole, ok := ignoredTagKey(traversal)
		if !ok {
			continue
		}
		if !whole && !isMarkerTag(key) {
			continue
		}

		construct := fmt.Sprintf("lifecycle { ignore_changes = [tags] } on %s", addr)
		detail := fmt.Sprintf(
			"%s ignores changes to its whole tags argument, and the %s and %s tags this mode writes to record ownership live there. "+
				"They would be planned and then discarded, so the resource would be applied with no markers on it: "+
				"the next run could not discover it, and would propose creating a second copy. "+
				"Ignore the individual tag keys something outside this configuration writes - ignore_changes = [tags[\"Owner\"]] - "+
				"rather than the whole argument.",
			addr, markers.TagEstate, markers.TagAddress,
		)
		if !whole {
			construct = fmt.Sprintf("lifecycle { ignore_changes = [tags[%q]] } on %s", key, addr)
			detail = fmt.Sprintf(
				"%s ignores changes to the %q tag, which is one of the ownership markers this mode writes. "+
					"It would be planned and then discarded, so the resource would be applied without it and the next "+
					"run could not discover it. Ownership markers are not an argument a configuration manages; "+
					"remove this entry.",
				addr, key,
			)
		}

		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreChanges,
			Construct: construct,
			Module:    path,
			Detail:    detail,
			Subject:   traversalRange(traversal, resource.DeclRange),
		})
	}
}

// ignoredTagKey reads one ignore_changes traversal.
//
// It returns whole=true for a bare `tags`, and whole=false with the key for
// `tags["k"]` or `tags.k`. ok is false for a traversal that is not rooted at
// tags at all, and for `tags[<non-literal>]`, which cannot be read here -
// that last case is reported as covering the whole argument, because a key
// this pass cannot evaluate might be a marker key.
func ignoredTagKey(traversal hcl.Traversal) (key string, whole, ok bool) {
	if len(traversal) == 0 {
		return "", false, false
	}
	root, isAttr := traversal[0].(hcl.TraverseAttr)
	if !isAttr || root.Name != "tags" {
		return "", false, false
	}
	if len(traversal) == 1 {
		return "", true, true
	}

	switch step := traversal[1].(type) {
	case hcl.TraverseIndex:
		if step.Key.Type() == cty.String && !step.Key.IsNull() {
			return step.Key.AsString(), false, true
		}
		// An index this pass cannot read as a string. Treat it as the
		// whole argument rather than as nothing: a key it cannot evaluate
		// may well be a marker key, and the quiet answer is the one this
		// rule exists to prevent.
		return "", true, true
	case hcl.TraverseAttr:
		return step.Name, false, true
	default:
		return "", true, true
	}
}

// isMarkerTag reports whether a tag key is one this mode writes: the three
// markers, plus the tofu-address continuations an overlong address is split
// across (live/MARKERS.md).
func isMarkerTag(key string) bool {
	switch key {
	case markers.TagEstate, markers.TagAddress, markers.TagSlot:
		return true
	}
	return strings.HasPrefix(key, markers.TagAddress+"-")
}

// traversalRange points the diagnostic at the ignore_changes entry itself
// where the traversal carries a range, and at the resource block otherwise.
func traversalRange(traversal hcl.Traversal, fallback hcl.Range) hcl.Range {
	if rng := traversal.SourceRange(); rng.Filename != "" {
		return rng
	}
	return fallback
}
