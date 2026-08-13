// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"time"

	"github.com/intentius/choudoufu/internal/live/projection"
)

// This file is issue #64's snapshot-guided leg: the estate-wide sweep's
// optional narrowing, behind Request.Guided. See the doc comment on
// Request.Guided in discovery.go for the design and the safety argument;
// this file is the mechanics.
//
// The one invariant every function here is written to honor: a problem
// reading or trusting the hint is never an error and never changes what the
// sweep does relative to Request.Guided being false. It only changes cost.
// TestGuided_equivalence is the proof.
//
// # The default-on policy
//
// This package's own zero value keeps Guided off - a direct caller of
// [Discover] that builds a bare Request gets today's full enumeration,
// unchanged, and every test in this file that does exactly that still
// passes for that reason. What changed is the fork's own commands
// (internal/command's statelessDiscover, the Request builder behind both
// "choudoufu live-plan" and a plain plan/apply under a "live" block): they
// now turn Guided on automatically whenever the estate's configuration
// already pays for a snapshot - a "live" block setting snapshot_path,
// snapshots = true, or both - and no explicit opt-out is set
// (TOFU_DISABLE_GUIDED_DISCOVERY, checked at the command layer). A
// configuration that never turned a snapshot on has no hint to guide
// anything and is completely unaffected: nothing here invents a hint source
// an operator never configured.
//
// That automatic path sets two things this package does not default for
// itself: GuidedMaxAge to defaultAutoGuidedMaxAge (7 days, in
// internal/command/live_plan.go) rather than this file's own
// defaultGuidedMaxAge, and GuidedVerifyAge to 24 hours, so that a hint which
// is still fresh enough to trust for narrowing but has gone a full day
// without a fresh sweep runs this pass as a verification sweep anyway - the
// safety valve that keeps "trust a week-old hint" from also meaning "drift
// can hide for a week". See GuidedVerifyAge below and
// internal/command/live_plan.go's statelessGuidedDiscovery for where these
// numbers are set and documented in full.

// defaultGuidedMaxAge is how old a snapshot hint may be before guided
// discovery treats it exactly like a missing one - see Request.GuidedMaxAge.
// It is deliberately generous: a day-old hint is still useful for deciding
// which types to skip, and Request.GuidedVerify (the periodic or flagged
// full sweep) is the mechanism that keeps a long-lived snapshot honest, not
// this threshold. A caller that wants a tighter bound sets GuidedMaxAge.
//
// This is the fallback used only when a caller sets Request.Guided directly
// with no GuidedMaxAge of its own - a direct API user, or this file's own
// tests. The fork's own commands set GuidedMaxAge explicitly instead (see
// the default-on policy note above), so this constant does not govern that
// path at all.
const defaultGuidedMaxAge = 24 * time.Hour

// guidedSweepUniverse decides which admitted, undeclared types this pass's
// estate-wide sweep actually lists.
//
// Request.Guided false, or any problem loading or trusting the hint,
// returns sweepTypes(req, decl) completely unmodified alongside the
// fallback reason (empty for the first case) - byte for byte the same slice
// shape an unguided caller has always gotten, which is what makes
// TestGuided_equivalence's comparison meaningful rather than coincidental.
//
// Otherwise (a fresh, well-formed hint, and neither Request.GuidedVerify nor
// GuidedVerifyAge's age check asking for a full sweep) the universe splits
// in two: a type the hint has no record of is always kept - there is no
// evidence to narrow against, so it is swept exactly as the cold path would
// - and a type the hint does have a record of is skipped this run, returned
// in skipped for [Result.GuidedSweepSkipped] rather than swept, on the
// documented trade this leg makes: a standing orphan of a hinted type may
// not resurface on every single routine plan, only at the next full or
// verification sweep. That trade is opt-in per Request (Guided defaults off
// in this package - see the default-on policy note above for what turns it
// on in practice) and bounded by the caller's own verification cadence
// (Request.GuidedVerify) or the hint's own age (Request.GuidedVerifyAge),
// never silent and never a wrong plan - it changes only when a real removal
// is proposed, never what gets destroyed once it is.
func guidedSweepUniverse(req Request, decl *declared) (universe, skipped []string, fallback string) {
	full := sweepTypes(req, decl)
	if !req.Guided {
		return full, nil, ""
	}

	hint, reason := loadGuidedHint(req)
	if hint == nil {
		return full, nil, reason
	}
	verify := req.GuidedVerify
	if !verify && req.GuidedVerifyAge > 0 && time.Since(hint.WrittenAt) > req.GuidedVerifyAge {
		// The hint is still fresh enough to be trusted (loadGuidedHint
		// already checked that against GuidedMaxAge), but old enough that
		// GuidedVerifyAge's own, shorter cadence says to re-verify rather
		// than narrow this run - see the default-on policy note at the top
		// of this file for why the fork's own commands set this.
		verify = true
	}
	if verify {
		return full, nil, ""
	}

	// full is already sorted (sweepTypes' own contract), and a subsequence
	// of a sorted slice is sorted, so neither half needs sorting again.
	for _, t := range full {
		if hint.Types[t] {
			skipped = append(skipped, t)
			continue
		}
		universe = append(universe, t)
	}
	return universe, skipped, ""
}

// loadGuidedHint reads req's configured hint source and applies the
// freshness rule guided discovery enforces before trusting a hint at all.
// Every non-nil second return means "fall back to full enumeration"; there
// is no other signal this function's caller needs to act on.
func loadGuidedHint(req Request) (*projection.Hint, string) {
	if req.SnapshotBranchDir == "" && req.SnapshotPath == "" {
		return nil, "guided discovery was requested but no snapshot source (SnapshotBranchDir or SnapshotPath) is configured; falling back to full enumeration"
	}

	hint, source, err := readGuidedHint(req)
	if hint == nil {
		return nil, fmt.Sprintf("could not read a snapshot hint from %s: %s; falling back to full enumeration", source, err)
	}

	maxAge := req.GuidedMaxAge
	if maxAge <= 0 {
		maxAge = defaultGuidedMaxAge
	}
	if hint.WrittenAt.IsZero() {
		return nil, fmt.Sprintf("the snapshot hint at %s carries no readable writtenAt timestamp, so its freshness cannot be established; falling back to full enumeration", source)
	}
	if age := time.Since(hint.WrittenAt); age > maxAge {
		return nil, fmt.Sprintf("the snapshot hint at %s is stale (%s old, over the %s limit); falling back to full enumeration", source, age.Round(time.Second), maxAge)
	}
	return hint, ""
}

// readGuidedHint tries the branch carrier and then the file carrier, the
// same priority projection.Manager.writeSnapshotCarriers gives the write
// side (branch primary, file the fallback, file alone when no branch is
// configured) so a guided reader and a snapshot writer pointed at the same
// two settings agree on which one is "the" snapshot.
func readGuidedHint(req Request) (hint *projection.Hint, source string, err error) {
	var branchErr error
	if req.SnapshotBranchDir != "" {
		source = fmt.Sprintf("branch tofu-snapshots/%s", req.Estate)
		hint, branchErr = projection.ReadHintBranch(req.SnapshotBranchDir, req.Estate)
		if branchErr == nil {
			return hint, source, nil
		}
	}
	if req.SnapshotPath != "" {
		source = req.SnapshotPath
		var fileErr error
		hint, fileErr = projection.ReadHintFile(req.SnapshotPath)
		if fileErr == nil {
			return hint, source, nil
		}
		if branchErr != nil {
			return nil, source, fmt.Errorf("branch: %s; file: %w", branchErr, fileErr)
		}
		return nil, source, fileErr
	}
	return nil, source, branchErr
}
