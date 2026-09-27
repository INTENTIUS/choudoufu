// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// DowngradeForNodeResolution turns every per-instance identity-resolution
// refusal in diags into a warning, for GitHub issue #388's plan-node seam
// (the foundation-order ruling (#388), ruling 3) and #364's own landing
// comment on unit B: "the static resolver cannot do per-address store IO
// without an import cycle," which is why this downgrade - and the resolver
// that gets a chance once it runs - live at the node instead of here.
//
// It is deliberately narrow. Only a diagnostic tagged [InstanceFailure] -
// resolveInstance's own per-instance failure marker, attached to every
// error this package raises about ONE resource instance's identity, never
// to a configuration-wide problem - is downgraded; every other diagnostic
// (a malformed reference, a bad address, anything with no single instance
// to blame) passes through unchanged and keeps the run fatal exactly as it
// is today. That is what makes the downgrade safe rather than a blanket
// "ignore every error under the flag": an ordinary configuration mistake
// unrelated to identity still stops the run, and only the resolutions the
// static evaluator gave up on are handed to the node instead.
//
// The instance itself is untouched here - it is simply absent from
// [Result.All], the same "could not be classified" contract [Resolve]'s own
// doc comment states - so downgrading its diagnostic changes nothing about
// what the pre-walk projection does with it. What changes is only whether
// the RUN, as a whole, treats that absence as fatal (today) or as a gap the
// plan-node seam's resolver gets to fill (under the flag): see
// internal/command/live_mode.go's PriorState, the caller.
func DowngradeForNodeResolution(diags tfdiags.Diagnostics) tfdiags.Diagnostics {
	if len(diags) == 0 {
		return diags
	}
	out := make(tfdiags.Diagnostics, len(diags))
	for i, d := range diags {
		if d.Severity() == tfdiags.Error && tfdiags.ExtraInfo[InstanceFailure](d).Addr != "" {
			out[i] = warningOverride{Diagnostic: d}
			continue
		}
		out[i] = d
	}
	return out
}

// warningOverride wraps a [tfdiags.Diagnostic] and reports [tfdiags.Warning]
// regardless of what the wrapped diagnostic itself carries, while leaving
// everything else about it - its Description, Source, FromExpr and its
// ExtraInfo chain (so [InstanceFailure] itself, and anything wrapped inside
// it, is still readable through the ordinary unwrap chain) - untouched.
type warningOverride struct {
	tfdiags.Diagnostic
}

func (warningOverride) Severity() tfdiags.Severity { return tfdiags.Warning }

// InstanceRefusals is every per-instance identity refusal in diags, keyed
// by the [InstanceFailure] address it is tagged with: exactly the
// diagnostics [DowngradeForNodeResolution] would turn into warnings. A
// caller reads it BEFORE downgrading, so the plan-node seam can tell an
// instance the static evaluator gave up on apart from one it never had to
// resolve (a greenfield parent-derived instance, say), and hand that fact
// to the node resolver. GitHub issue #1539: on a marker surface that
// carries no address, the node refuses such an instance rather than
// planning a create, because no live listing can bind it back to the
// block. Nil when nothing was refused.
func InstanceRefusals(diags tfdiags.Diagnostics) map[string]tfdiags.Diagnostics {
	var out map[string]tfdiags.Diagnostics
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		addr := tfdiags.ExtraInfo[InstanceFailure](d).Addr
		if addr == "" {
			continue
		}
		if out == nil {
			out = make(map[string]tfdiags.Diagnostics)
		}
		out[addr] = out[addr].Append(d)
	}
	return out
}
