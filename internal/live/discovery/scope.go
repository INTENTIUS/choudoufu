// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"github.com/intentius/choudoufu/internal/addrs"
)

// inScope reports whether one instance address is inside this run's
// -target / -exclude filtering - see [Request.Scope], which is nil for
// every untargeted run and answers true for everything when it is.
//
// The question is asked of the BLOCK, not the instance key, because that
// is the granularity [identity.Scope] has: it comes from
// [tofu.Context.TargetedResources], whose answer is a set of
// [addrs.ConfigResource]. An instance of an in-scope block is in scope.
func (req Request) inScope(addr addrs.AbsResourceInstance) bool {
	if req.Scope == nil {
		return true
	}
	return req.Scope(addr.ConfigResource())
}

// inScope is the same question for [ReconcileRequest], which is a separate
// entry point from [Discover] and so carries its own scope field - GitHub
// issue #1257, filed by #1203's audit.
//
// The answer here is nearly always false whenever a scope is present at
// all, and that is a property of what reconciliation looks for rather than
// a defect in this predicate. A reconciliation candidate carries no estate
// marker and no configuration block by definition, and
// [statelessTargetScope] builds the scope from the configuration's own plan
// graph, so no vertex exists for [syntheticReconcileAddr]'s minted address.
// It is still written as a per-candidate scope check rather than a bare
// `req.Scope != nil`, for two reasons: it is the one mechanism the rest of
// the live path asks this question through, and it keeps the threshold
// guard coupled to the population it guards - see
// [ReconcileResult.Proposable].
func (req ReconcileRequest) inScope(addr addrs.AbsResourceInstance) bool {
	if req.Scope == nil {
		return true
	}
	return req.Scope(addr.ConfigResource())
}
