// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// Wave apply's exit codes. 3 is "apply <planfile>"'s own code for an
// approved plan the live system has moved under (internal/command's
// ExitApprovalRefused), extended here to a set; 4 is the set plan's code
// for "some root failed" (internal/live/setplan).
const (
	ExitApplied    = 0
	ExitError      = 1
	ExitSetMoved   = 3
	ExitRootFailed = 4
)

// Outcomes a root can have in a resume file.
const (
	OutcomeLanded  = "landed"
	OutcomeFailed  = "failed"
	OutcomeSkipped = "skipped"
)

// ResumeFormatVersion is the resume file's format version.
const ResumeFormatVersion = "1"

// Resume is the resume file: what each root of an approved set came to.
// A wave apply reads it to learn what earlier waves landed, writes it after
// every root, and a re-run with it applies only what has not landed.
type Resume struct {
	FormatVersion string `json:"format_version"`
	// SetDigest is the approved set this file belongs to. A file for
	// another set is refused.
	SetDigest string        `json:"set_digest"`
	Roots     []ResumeEntry `json:"roots"`
}

// ResumeEntry is one root's outcome.
type ResumeEntry struct {
	Root    string `json:"root"`
	Wave    int    `json:"wave"`
	Outcome string `json:"outcome"`
	// Reason says why a root failed or was skipped.
	Reason string `json:"reason,omitempty"`
}

// ReadResume reads path, or returns an empty Resume when it does not
// exist yet.
func ReadResume(path string) (*Resume, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Resume{FormatVersion: ResumeFormatVersion, Roots: []ResumeEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var r Resume
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s is not a resume file: %w", path, err)
	}
	if r.FormatVersion != ResumeFormatVersion {
		return nil, fmt.Errorf("%s has format version %q; this build reads %q", path, r.FormatVersion, ResumeFormatVersion)
	}
	if r.Roots == nil {
		r.Roots = []ResumeEntry{}
	}
	return &r, nil
}

// WriteResume writes r to path, through a temporary file so a reader never
// sees half of it.
func WriteResume(path string, r *Resume) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *Resume) outcome(root string) (ResumeEntry, bool) {
	for _, e := range r.Roots {
		if e.Root == root {
			return e, true
		}
	}
	return ResumeEntry{}, false
}

func (r *Resume) set(e ResumeEntry) {
	for i := range r.Roots {
		if r.Roots[i].Root == e.Root {
			r.Roots[i] = e
			return
		}
	}
	r.Roots = append(r.Roots, e)
	sort.Slice(r.Roots, func(i, j int) bool { return r.Roots[i].Root < r.Roots[j].Root })
}

// FreshPlan is one root planned again at apply time.
type FreshPlan struct {
	RootPlan
	// PlanFile is the fresh plan's saved file, which is what is applied.
	PlanFile string
}

// ApplyRunner plans and applies roots. internal/command's is real: a set
// plan of the wave's roots, then "apply PLANFILE" in each root. Tests
// substitute their own.
type ApplyRunner interface {
	// PlanRoots plans every root afresh and returns one entry per root,
	// keyed by root. A root that fails to plan is an entry whose Status is
	// not "planned".
	PlanRoots(ctx context.Context, roots []string) (map[string]FreshPlan, error)
	// Apply applies planFile in root. A non-nil error is the root failing.
	Apply(ctx context.Context, root, planFile string) error
}

// ApplyOptions are one wave apply's inputs.
type ApplyOptions struct {
	// Set is the approved set plan document.
	Set *SetDocument
	// Approved is the approved set digest. Set must hash to it.
	Approved string
	// Waves is the set's split (from [Build] over Set).
	Waves *Document
	// Wave is the wave to apply, from 1.
	Wave   int
	Resume *Resume
	// Save writes the resume file. It is called after every root's
	// outcome is decided.
	Save   func(*Resume) error
	Runner ApplyRunner
}

// Moved is a root whose fresh plan does not match the approved one.
type Moved struct {
	Root     string `json:"root"`
	Approved string `json:"approved_digest"`
	Fresh    string `json:"fresh_digest"`
}

// ApplyResult is what a wave apply did.
type ApplyResult struct {
	ExitCode int `json:"exit_code"`
	Wave     int `json:"wave"`
	// Error is the refusal, for exit 1 and exit 3.
	Error string `json:"error,omitempty"`
	// Moved are the roots whose plans moved (exit 3); nothing was applied.
	Moved []Moved `json:"moved,omitempty"`
	// Outcomes are this run's outcomes for the wave's roots, including
	// those already landed before it ran.
	Outcomes []ResumeEntry `json:"outcomes"`
	// Applied are the roots this run applied, in order.
	Applied []string `json:"applied"`
}

// Apply applies one wave of an approved set. See the package
// documentation's "Wave apply" for the rules.
// GitHub issue #1898. Span names for the steps of a wave apply, and the
// choudoufu.refused.step each gate records when it refuses.
const (
	TraceNameDigest    = "Wave set digest"
	TraceNameResume    = "Wave resume check"
	TraceNameFreshPlan = "Wave fresh plan"
	TraceNameApplyRoot = "Wave apply root"

	RefusedStepDigest    = "digest"
	RefusedStepResume    = "resume"
	RefusedStepFreshPlan = "fresh-plan"
)

func Apply(ctx context.Context, o ApplyOptions) (*ApplyResult, error) {
	res := &ApplyResult{Wave: o.Wave, Outcomes: []ResumeEntry{}, Applied: []string{}}
	refuse := func(code int, format string, args ...any) (*ApplyResult, error) {
		res.ExitCode = code
		res.Error = fmt.Sprintf(format, args...)
		return res, nil
	}
	if o.Wave < 1 || o.Wave > len(o.Waves.Waves) {
		return refuse(ExitError, "wave %d does not exist: this set splits into %d waves", o.Wave, len(o.Waves.Waves))
	}

	// GitHub issue #1898: each gate is its own span, so a refused set shows
	// in the trace at the step that refused it.
	_, digestSpan := tracing.Tracer().Start(ctx, TraceNameDigest)
	approvedRoots, setDigest, err := DocumentDigests(o.Set)
	if err != nil {
		tracing.SetSpanError(digestSpan, err)
		digestSpan.End()
		return refuse(ExitError, "%s", err)
	}
	digestSpan.SetAttributes(traceattrs.String(traceattrs.AttrSetDigest, setDigest))
	if setDigest != o.Approved {
		tracing.MarkRefused(digestSpan, RefusedStepDigest, "the set plan document does not hash to the approved digest")
		digestSpan.End()
		return refuse(ExitSetMoved, "the set plan document hashes to %s, not the approved %s: it is not the set that was approved, so nothing was applied", setDigest, o.Approved)
	}
	digestSpan.End()
	_, resumeSpan := tracing.Tracer().Start(ctx, TraceNameResume,
		tracing.SpanAttributes(traceattrs.Int64("choudoufu.wave.resume_entries", int64(len(o.Resume.Roots)))),
	)
	if o.Resume.SetDigest != "" && o.Resume.SetDigest != o.Approved {
		tracing.MarkRefused(resumeSpan, RefusedStepResume, "the resume file belongs to another set digest")
		resumeSpan.End()
		return refuse(ExitError, "the resume file belongs to the set %s, not the approved %s; use a new resume file for a new approval", o.Resume.SetDigest, o.Approved)
	}
	resumeSpan.End()
	o.Resume.SetDigest = o.Approved
	o.Resume.FormatVersion = ResumeFormatVersion

	byRoot := make(map[string]RootPlan, len(o.Set.Roots))
	for _, r := range o.Set.Roots {
		byRoot[r.Root] = r
	}
	waveOf := map[string]int{}
	for _, w := range o.Waves.Waves {
		for _, r := range w.Roots {
			waveOf[r] = w.Number
		}
	}
	producers := map[string][]string{}
	for _, e := range o.Waves.Edges {
		producers[e.Reader] = append(producers[e.Reader], e.Producer)
	}
	wave := o.Waves.Waves[o.Wave-1]

	decided := map[string]ResumeEntry{}
	decide := func(e ResumeEntry) error {
		e.Wave = o.Wave
		decided[e.Root] = e
		o.Resume.set(e)
		return o.Save(o.Resume)
	}
	// landedOrWhy reports whether producer p has landed, and if not why,
	// from this run's outcomes first and the resume file second.
	landedOrWhy := func(p string) (bool, string) {
		if e, ok := decided[p]; ok {
			if e.Outcome == OutcomeLanded {
				return true, ""
			}
			return false, fmt.Sprintf("%s (wave %d) %s", p, waveOf[p], e.Outcome)
		}
		if e, ok := o.Resume.outcome(p); ok {
			if e.Outcome == OutcomeLanded {
				return true, ""
			}
			return false, fmt.Sprintf("%s (wave %d) %s", p, waveOf[p], e.Outcome)
		}
		return false, fmt.Sprintf("%s (wave %d) has not been applied", p, waveOf[p])
	}

	// What is left: roots not already landed. Of those, a root whose
	// producer in an earlier wave has not landed is skipped before
	// anything is planned, and a root with no approved plan has failed.
	var left []string
	var preDecided []ResumeEntry
	for _, r := range wave.Roots {
		if e, ok := o.Resume.outcome(r); ok && e.Outcome == OutcomeLanded {
			res.Outcomes = append(res.Outcomes, e)
			continue
		}
		if ap := byRoot[r]; ap.Status != StatusPlanned {
			preDecided = append(preDecided, ResumeEntry{Root: r, Outcome: OutcomeFailed, Reason: "the approved set plan has no plan for this root: " + firstLine(ap.Error)})
			continue
		}
		var why []string
		for _, p := range producers[r] {
			if waveOf[p] >= o.Wave {
				continue
			}
			if ok, w := landedOrWhy(p); !ok {
				why = append(why, w)
			}
		}
		if len(why) > 0 {
			preDecided = append(preDecided, ResumeEntry{Root: r, Outcome: OutcomeSkipped, Reason: "it reads " + strings.Join(why, ", ")})
			continue
		}
		left = append(left, r)
	}

	// Every root left is planned afresh, and nothing is applied unless
	// every fresh plan that planned matches its approved plan.
	planCtx, planSpan := tracing.Tracer().Start(ctx, TraceNameFreshPlan,
		tracing.SpanAttributes(traceattrs.Int64(traceattrs.AttrRoots, int64(len(left)))),
	)
	defer planSpan.End() // ended earlier on every path below; End is idempotent
	fresh := map[string]FreshPlan{}
	if len(left) > 0 {
		fresh, err = o.Runner.PlanRoots(planCtx, left)
		if err != nil {
			tracing.SetSpanError(planSpan, err)
			return refuse(ExitError, "planning the wave afresh: %s", err)
		}
	}
	var planFailed []ResumeEntry
	for _, r := range left {
		f, ok := fresh[r]
		if !ok {
			return refuse(ExitError, "the fresh plan returned nothing for %s", r)
		}
		if f.Status != StatusPlanned {
			planFailed = append(planFailed, ResumeEntry{Root: r, Outcome: OutcomeFailed, Reason: "its fresh plan failed: " + firstLine(f.Error)})
			continue
		}
		f.Root = r
		f.Estate = byRoot[r].Estate
		d, err := RootDigest(f.RootPlan)
		if err != nil {
			return refuse(ExitError, "%s", err)
		}
		if d != approvedRoots[r] {
			res.Moved = append(res.Moved, Moved{Root: r, Approved: approvedRoots[r], Fresh: d})
		}
	}
	if len(res.Moved) > 0 {
		planSpan.SetAttributes(traceattrs.Int64(traceattrs.AttrMoved, int64(len(res.Moved))))
		tracing.MarkRefused(planSpan, RefusedStepFreshPlan, "a fresh plan no longer matches its approved plan")
		names := make([]string, 0, len(res.Moved))
		for _, m := range res.Moved {
			names = append(names, m.Root)
		}
		return refuse(ExitSetMoved, "the fresh plan of %s no longer matches the approved plan, so nothing in wave %d was applied: plan the set again and approve the new digest", strings.Join(names, ", "), o.Wave)
	}

	planSpan.End()

	for _, e := range append(preDecided, planFailed...) {
		if err := decide(e); err != nil {
			return nil, err
		}
	}

	// Apply in an order that puts every producer in this wave first; a
	// root whose producer did not land is skipped.
	order, err := waveOrder(left, producers, waveOf, o.Wave)
	if err != nil {
		return refuse(ExitError, "%s", err)
	}
	for _, r := range order {
		if _, done := decided[r]; done {
			continue
		}
		var why []string
		for _, p := range producers[r] {
			if waveOf[p] != o.Wave {
				continue
			}
			if ok, w := landedOrWhy(p); !ok {
				why = append(why, w)
			}
		}
		if len(why) > 0 {
			if err := decide(ResumeEntry{Root: r, Outcome: OutcomeSkipped, Reason: "it reads " + strings.Join(why, ", ")}); err != nil {
				return nil, err
			}
			continue
		}
		res.Applied = append(res.Applied, r)
		applyCtx, applySpan := tracing.Tracer().Start(ctx, TraceNameApplyRoot,
			tracing.SpanAttributes(
				traceattrs.String(traceattrs.AttrRoot, r),
				traceattrs.String(traceattrs.AttrEstate, byRoot[r].Estate),
			),
		)
		if err := o.Runner.Apply(applyCtx, r, fresh[r].PlanFile); err != nil {
			applySpan.SetAttributes(traceattrs.String(traceattrs.AttrOutcome, string(OutcomeFailed)))
			// The error carries the child's stderr; only the outcome goes on the span.
			tracing.SetSpanError(applySpan, "apply failed")
			applySpan.End()
			if err := decide(ResumeEntry{Root: r, Outcome: OutcomeFailed, Reason: "its apply failed: " + firstLine(err.Error())}); err != nil {
				return nil, err
			}
			continue
		}
		applySpan.SetAttributes(traceattrs.String(traceattrs.AttrOutcome, string(OutcomeLanded)))
		applySpan.End()
		if err := decide(ResumeEntry{Root: r, Outcome: OutcomeLanded}); err != nil {
			return nil, err
		}
	}

	for _, r := range wave.Roots {
		if e, ok := decided[r]; ok {
			res.Outcomes = append(res.Outcomes, e)
		}
	}
	sort.Slice(res.Outcomes, func(i, j int) bool { return res.Outcomes[i].Root < res.Outcomes[j].Root })
	res.ExitCode = ExitApplied
	for _, e := range res.Outcomes {
		if e.Outcome != OutcomeLanded {
			res.ExitCode = ExitRootFailed
		}
	}
	return res, nil
}

// waveOrder orders roots so each comes after its producers in the same
// wave, ties broken by name.
func waveOrder(roots []string, producers map[string][]string, waveOf map[string]int, wave int) ([]string, error) {
	in := map[string]bool{}
	for _, r := range roots {
		in[r] = true
	}
	var out []string
	done := map[string]bool{}
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	for len(out) < len(sorted) {
		progressed := false
		for _, r := range sorted {
			if done[r] {
				continue
			}
			ready := true
			for _, p := range producers[r] {
				if waveOf[p] == wave && in[p] && !done[p] {
					ready = false
				}
			}
			if ready {
				done[r] = true
				out = append(out, r)
				progressed = true
			}
		}
		if !progressed {
			return nil, fmt.Errorf("wave %d's roots read each other in a cycle", wave)
		}
	}
	return out, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
