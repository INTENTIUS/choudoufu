// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"

	"github.com/intentius/choudoufu/internal/configs"
)

// EstatesRead is the producer estates cfg's static module tree reads
// through the cross-estate pattern [Reference] describes (a data source
// filtered on a literal tag:tofu-estate value), sorted and deduplicated.
//
// It exists for live-affected (GitHub issue #1751), which builds its own
// module tree per root and only needs the estate names. It is the same
// detection live-check reports as references; internal/live/waves (#1754)
// carries a stricter reader that refuses a non-literal filter, and
// live-affected switches to it when that lands.
func EstatesRead(cfg *configs.Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range crossEstateReferences(cfg) {
		if !seen[ref.Estate] {
			seen[ref.Estate] = true
			out = append(out, ref.Estate)
		}
	}
	sort.Strings(out)
	return out
}
