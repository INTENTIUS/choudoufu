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
