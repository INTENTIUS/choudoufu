// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"github.com/hashicorp/hcl/v2"
)

// ConfigFiles returns the configuration files [Parser.LoadConfigDir] would
// read in dir: the primary set and the override set, in the same order, with
// the same extension rules, the same ignored-file rules and the same
// .tf-shadowed-by-.tofu filtering.
//
// It exists so that a caller which must REWRITE a module's source text -
// internal/live/onboard, computing the edit that turns a state-backed module
// into a live one - reads exactly the file set the loader will read back.
// Duplicating that selection has failed in this repository before: an
// ownership check filtered on ".tf" while the loader also accepted ".tf.json"
// and ".tofu", so the guard was narrower than the thing it guarded and said
// nothing about the files it missed. A rewriter with that bug is worse than a
// narrow guard: it would delete a backend block from one spelling of a file
// and leave the module carrying the backend that spelling of the loader still
// finds, then report the module as edited.
//
// Symbol library files (.sym.hcl, OpenTofu v1.13.0's experimental symbol
// libraries) are returned with the primary set (#1778). They are part of the
// module's source text - [BuildConfig] reads them for every module through
// [Parser.LoadSymbolFilesInDir] - so a caller that must see every file the
// module is made of has to see them too, for the same narrower-than-the-loader
// reason as above. They cannot carry a backend or a live block, so a rewriter
// finds nothing to edit in one; returning them costs that caller a parse, and
// leaving them out would make the set narrower than what the loader reads.
//
// Test files are deliberately not returned. They are not part of the module
// and cannot carry a terraform block's backend.
func (p *Parser) ConfigFiles(dir string) (primary, override []string, diags hcl.Diagnostics) {
	var symbols []string
	primary, override, _, symbols, diags = p.dirFiles(dir, "")
	primary = append(primary, symbols...)
	return primary, override, diags
}
