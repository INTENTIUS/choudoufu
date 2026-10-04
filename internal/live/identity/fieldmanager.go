// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// The block and attribute a kubernetes_manifest resource names its
// server-side-apply field manager in. internal/live/liveimport reads the
// same pair off a migrated state's object value; [ManifestFieldManager]
// reads it off the configuration, because a live run has no state to
// read.
const (
	ManifestFieldManagerBlock = "field_manager"
	ManifestFieldManagerName  = "name"
)

// ManifestFieldManager reads the `field_manager { name = ... }` the
// kubernetes_manifest block declaring addr declares, or "" when it
// declares none - which every caller reads as
// kubesweep.DefaultFieldManager, the provider's own default.
//
// Two callers need the same answer. internal/command's owned-keys rail
// (GitHub issue #1211) asks managedFields about this manager, and
// managedFields is keyed by manager NAME, so asking about "Terraform" on
// an object a block wrote under "my-pipeline" finds no entry. live-mv's
// manifest rename (internal/live/mv/manifest.go, #1720) writes the
// address annotation under this manager, because the marker patch hands
// the annotation to that manager's Apply entry and the provider's next
// apply of the block runs under it; handed to "Terraform" instead, the
// next rename conflicts.
//
// A block whose name is not statically resolvable is an ERROR rather
// than a fallback to the default: the default would be a guess, and a
// wrong guess is exactly the misattribution each caller exists to avoid.
func ManifestFieldManager(ctx context.Context, config *configs.Config, addr addrs.AbsResourceInstance) (string, error) {
	modCfg, ok := ConfigForModule(config, addr.Module)
	if !ok || modCfg.Module == nil {
		return "", nil
	}
	rc := modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	if rc == nil || rc.Config == nil {
		return "", nil
	}
	content, _, _ := rc.Config.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: ManifestFieldManagerBlock}},
	})
	eval := modCfg.Module.StaticEvaluator
	ident := configs.StaticIdentifier{
		Module:    addr.Module.Module(),
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}
	for _, blk := range content.Blocks {
		if blk == nil || blk.Body == nil {
			continue
		}
		inner, _, _ := blk.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: ManifestFieldManagerName}},
		})
		attr, ok := inner.Attributes[ManifestFieldManagerName]
		if !ok || attr == nil {
			continue
		}
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() && eval != nil {
			// A literal needs no evaluator; a name from a variable or a
			// local does, and the static evaluator is the same one every
			// other identity-bearing argument on this block is read
			// through.
			val, diags = eval.Evaluate(ctx, attr.Expr, ident)
		}
		if diags.HasErrors() {
			return "", fmt.Errorf("the field_manager name declared by %s cannot be resolved without applying (%s), so this run cannot tell which field manager the object is written under", rc.Addr(), diags.Error())
		}
		if val.IsNull() || !val.IsKnown() {
			return "", fmt.Errorf("the field_manager name declared by %s is not known until apply, so this run cannot tell which field manager the object is written under", rc.Addr())
		}
		val, _ = val.Unmark()
		if val.Type() != cty.String {
			return "", fmt.Errorf("the field_manager name declared by %s is a %s rather than a string", rc.Addr(), val.Type().FriendlyName())
		}
		if s := val.AsString(); s != "" {
			return s, nil
		}
	}
	return "", nil
}
