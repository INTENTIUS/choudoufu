// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// checkChildLiveConfig rejects a live configuration - block or sidecar -
// declared inside a child module.
//
// Found by the wave-3 adversarial audit of #72: a child module carrying an
// estate.chdf.hcl sidecar (or a live block) decoded cleanly and was then
// read by nobody - every consumer reads the root module's Live only - so a
// vendored module that checked in its own estate had its resources silently
// absorbed into the caller's estate. Silent reinterpretation of an estate
// boundary is the misattribution class #104 and #123 exist to prevent, one
// level up.
//
// The estate is a property of the whole configuration, and the root module
// is where the whole configuration is visible - the same reasoning that
// keeps provider configuration at root (module-provider-block). A module
// that wants to BE an estate is a root module of its own run.
func checkChildLiveConfig(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if path.IsRoot() || mod.Live == nil {
		return
	}

	form := "live block"
	if mod.Live.Sidecar {
		form = configs.LiveSidecarFilename + " sidecar file"
	}

	*issues = append(*issues, Issue{
		Rule:      RuleChildLiveConfig,
		Construct: fmt.Sprintf("%s in module %s", form, path),
		Module:    path,
		Detail: fmt.Sprintf(
			"Module %s declares a %s, and live mode reads the ROOT module's live configuration only - this one "+
				"would be silently ignored, and the module's resources absorbed into the calling estate with "+
				"nothing said. An estate is a property of the whole configuration: declare the live "+
				"configuration at the root, and remove it from the module. A module that should be its own "+
				"estate is a root module of its own run.",
			path, form,
		),
		Subject: mod.Live.DeclRange,
	})
}
