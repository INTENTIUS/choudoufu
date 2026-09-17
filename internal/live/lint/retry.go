// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/retry"
)

// checkLiveRetry validates a live block's retry block: the attempt budget and
// the mode it is spent under. GitHub issues #1196 and #1148.
//
// The rules themselves live in [retry.Config.Validate], stated once, so this
// function decides nothing about what is valid - it decides only which
// argument each complaint should point at. That split matters here more than
// it looks: the clients that build an aws.Config from these settings call
// Validate too, and a second copy of the bounds would be a second place that
// has to agree with the first.
//
// A block that sets neither argument is not an error. It resolves to the
// aws-sdk-go-v2 defaults, which is what every configuration written before
// this block existed gets, and writing the defaults out longhand is a
// legitimate thing to do when an estate wants its retry behaviour recorded
// rather than inherited.
func checkLiveRetry(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if mod.Live == nil || mod.Live.Retry == nil {
		return
	}
	rt := mod.Live.Retry

	for _, problem := range retry.Build(rt).Validate() {
		// Point at the argument the complaint is about, falling back to the
		// block header when the complaint came from a default rather than
		// from something written out - which cannot happen today, since the
		// defaults are valid by construction, but would be a silent
		// mis-pointed diagnostic the day a default changed.
		subject := rt.DeclRange
		construct := "retry"
		switch {
		case isAboutMode(problem) && rt.ModeSet:
			subject = rt.ModeRange
			construct = fmt.Sprintf("retry.mode = %q", rt.Mode)
		case !isAboutMode(problem) && rt.MaxAttemptsSet:
			subject = rt.MaxAttemptsRange
			construct = fmt.Sprintf("retry.max_attempts = %d", rt.MaxAttempts)
		}
		*issues = append(*issues, Issue{
			Rule:      RuleRetry,
			Construct: construct,
			Module:    path,
			Detail:    problem + ".",
			Subject:   subject,
		})
	}
}

// isAboutMode reports whether a [retry.Config.Validate] sentence is about the
// mode argument rather than the attempt count.
//
// Matching on the sentence is deliberately crude, and it is confined to this
// function so that the alternative - Validate returning a typed field name
// alongside each sentence - stays available without changing its callers. The
// two argument names cannot collide: "mode" never appears in an attempt-count
// sentence and "max_attempts" never appears in a mode one, which
// TestRetryValidateSentencesArePointable pins.
func isAboutMode(problem string) bool {
	return len(problem) >= 4 && problem[:4] == "mode"
}
