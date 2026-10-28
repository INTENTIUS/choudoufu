// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package modcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sweep is the part of a tools/refusal-probe sweep file the static tier
// reads. The probe is a main package, so its types are mirrored here field
// by field, by JSON name.
type Sweep struct {
	ProbeVersion   int               `json:"probe_version"`
	Commit         string            `json:"commit"`
	CorpusProblems []string          `json:"corpus_problems"`
	Schemas        bool              `json:"schemas"`
	OnboardedForm  bool              `json:"onboarded_form"`
	SecondPass     bool              `json:"second_pass"`
	PinSource      string            `json:"pin_source"`
	PinVersion     string            `json:"pin_version"`
	Sources        []SweepSource     `json:"sources"`
	Layers         map[string]string `json:"layers"`
	Entries        []SweepEntry      `json:"entries"`
}

// SweepSource is one manifest source as the sweep expanded it.
type SweepSource struct {
	Glob     string `json:"glob"`
	Origin   string `json:"origin"`
	Entries  int    `json:"entries"`
	Pin      string `json:"pin"`
	Checkout string `json:"checkout"`
}

// SweepEntry is one measured configuration.
type SweepEntry struct {
	Name       string           `json:"name"`
	Origin     string           `json:"origin"`
	Providers  []SweepProvider  `json:"providers"`
	Onboarding *SweepOnboarding `json:"onboarding"`
	Onboarded  *SweepForm       `json:"onboarded"`
}

// SweepProvider is one provider an entry needed.
type SweepProvider struct {
	Provider string `json:"provider"`
	Present  bool   `json:"present"`
}

// SweepOnboarding is internal/live/onboard's conclusion for an entry.
type SweepOnboarding struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// SweepForm is one form's analysis.
type SweepForm struct {
	Blocked  bool           `json:"blocked"`
	Reads    int            `json:"reads"`
	Refusals map[string]int `json:"refusals"`
}

// probeVersion is the refusal-probe sweep format this reads.
const probeVersion = 2

// ReadSweep decodes a sweep file.
func ReadSweep(path string) (*Sweep, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Sweep
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &s, nil
}

// StaticOptions are the inputs a sweep does not carry.
type StaticOptions struct {
	// Date is the day the sweep ran, YYYY-MM-DD.
	Date string

	// Commit is the choudoufu commit the probe was built from. It is
	// required when the sweep recorded none (a -root outside a git
	// checkout), and must agree with the sweep when both are set.
	Commit string
}

// StaticTier builds the static measurement and rows from a sweep, against
// the manifest's terraform-aws-modules sources at root.
//
// It refuses a sweep that cannot stand for the static tier: one taken
// without schemas, without the onboarded form or without the second pass;
// one whose corpus was partial for any terraform-aws-modules source; one
// taken on a dirty tree; and one whose sources disagree with the manifest's
// pins. Each of those would publish a row measuring something other than
// what the row says.
func StaticTier(root string, s *Sweep, opts StaticOptions) (*StaticMeasurement, []StaticRow, error) {
	switch {
	case s.ProbeVersion != probeVersion:
		return nil, nil, fmt.Errorf("sweep has probe_version %d; this reads %d", s.ProbeVersion, probeVersion)
	case !s.Schemas:
		return nil, nil, fmt.Errorf("sweep was taken without -schemas; a schema-less sweep cannot see the stamp layer and is not the static tier")
	case !s.OnboardedForm:
		return nil, nil, fmt.Errorf("sweep was taken without -onboarded; the static tier measures the onboarded form (#2292 ruling B)")
	case !s.SecondPass:
		return nil, nil, fmt.Errorf("sweep was taken with -second-pass=false; that sweep carries the false refusals #2297 removed")
	case s.PinVersion == "":
		return nil, nil, fmt.Errorf("sweep records no -provider-version")
	case opts.Date == "":
		return nil, nil, fmt.Errorf("no measurement date")
	}
	commit := s.Commit
	switch {
	case strings.HasSuffix(commit, "+dirty"):
		return nil, nil, fmt.Errorf("sweep was taken on a dirty tree (%s); no commit names what it measured", commit)
	case commit == "" && opts.Commit == "":
		return nil, nil, fmt.Errorf("sweep records no commit (its -root was not a git checkout); pass the commit the probe was built from")
	case commit == "":
		commit = opts.Commit
	case opts.Commit != "" && opts.Commit != commit:
		return nil, nil, fmt.Errorf("sweep was taken at %s, not %s", commit, opts.Commit)
	}

	sources, err := ManifestSources(root)
	if err != nil {
		return nil, nil, err
	}
	swept := map[string]SweepSource{}
	for _, ss := range s.Sources {
		swept[ss.Glob] = ss
	}
	for _, p := range s.CorpusProblems {
		for _, src := range sources {
			if strings.Contains(p, src.Glob) {
				return nil, nil, fmt.Errorf("sweep's corpus was partial for %s: %s", src.Glob, p)
			}
		}
	}

	docs := refusalDocs()
	var rows []StaticRow
	for i := range sources {
		src := &sources[i]
		ss, ok := swept[src.Glob]
		if !ok {
			return nil, nil, fmt.Errorf("sweep did not expand manifest source %s", src.Glob)
		}
		if ss.Pin != src.Commit || ss.Checkout != src.Commit {
			return nil, nil, fmt.Errorf("sweep measured %s at checkout %q (pin %q); the manifest pins %s", src.Glob, ss.Checkout, ss.Pin, src.Commit)
		}
		prefix := strings.TrimSuffix(filepath.ToSlash(filepath.Dir(src.Glob)), "/") + "/"
		repoDir := strings.TrimSuffix(prefix, "examples/")
		for _, e := range s.Entries {
			if e.Origin != Origin || !globMatch(src.Glob, e.Name) {
				continue
			}
			row, err := staticRow(*src, repoDir, e, s.Layers, docs)
			if err != nil {
				return nil, nil, err
			}
			row.Date, row.Commit, row.AWSProvider = opts.Date, commit, s.PinVersion
			rows = append(rows, row)
			src.Examples++
		}
		if src.Examples != ss.Entries {
			return nil, nil, fmt.Errorf("sweep expanded %s to %d entries but holds %d of them; was it run with an -entry filter that cut a module short?", src.Glob, ss.Entries, src.Examples)
		}
		if src.Examples == 0 {
			return nil, nil, fmt.Errorf("sweep holds no entry for %s", src.Glob)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Module != rows[j].Module {
			return rows[i].Module < rows[j].Module
		}
		return rows[i].Example < rows[j].Example
	})

	m := &StaticMeasurement{
		Date:        opts.Date,
		Commit:      commit,
		Instrument:  "tools/refusal-probe -schemas -onboarded",
		Form:        "onboarded",
		Schemas:     s.Schemas,
		SecondPass:  s.SecondPass,
		AWSProvider: s.PinVersion,
		Sources:     sources,
	}
	return m, rows, nil
}

// staticRow renders one entry's onboarded form.
func staticRow(src Source, repoDir string, e SweepEntry, layers map[string]string, docs map[[2]string]string) (StaticRow, error) {
	row := StaticRow{
		Tier:     TierStatic,
		Module:   src.Module,
		Version:  src.Version,
		Example:  strings.TrimPrefix(e.Name, repoDir),
		Refusals: []Refusal{},
	}
	for _, p := range e.Providers {
		if !p.Present {
			row.MissingProviders = append(row.MissingProviders, p.Provider)
		}
	}
	sort.Strings(row.MissingProviders)

	if e.Onboarded == nil {
		if e.Onboarding == nil {
			return row, fmt.Errorf("%s has no onboarded form; the sweep was not taken with -onboarded", e.Name)
		}
		row.Verdict = VerdictUnmeasured
		row.Reason = e.Onboarding.Reason
		if row.Reason == "" {
			row.Reason = e.Onboarding.Status
		}
		return row, nil
	}

	row.Reads = e.Onboarded.Reads
	for id, n := range e.Onboarded.Refusals {
		layer := layers[id]
		if isEligibleRead(layer, id) {
			if n != row.Reads {
				return row, fmt.Errorf("%s: the eligible read has %d sites under refusals and %d under reads", e.Name, n, row.Reads)
			}
			continue
		}
		ref := docs[[2]string{layer, id}]
		row.Refusals = append(row.Refusals, Refusal{
			ID:      id,
			Layer:   layer,
			Sites:   n,
			DocsRef: ref,
			DocsURL: DocsURL(ref),
		})
	}
	sortRefusals(row.Refusals)

	switch {
	case e.Onboarded.Blocked && len(row.Refusals) > 0:
		row.Verdict = VerdictRefused
	case !e.Onboarded.Blocked && len(row.Refusals) == 0:
		row.Verdict = VerdictClear
	default:
		// The probe's verdict and its refusal list disagree, so one of them
		// is not what this row would say.
		return row, fmt.Errorf("%s: onboarded blocked=%v with %d refusal(s) other than the eligible read", e.Name, e.Onboarded.Blocked, len(row.Refusals))
	}
	return row, nil
}

// globMatch reports whether name matches a manifest glob, both slash paths
// relative to the root.
func globMatch(glob, name string) bool {
	ok, err := filepath.Match(filepath.FromSlash(glob), filepath.FromSlash(name))
	return err == nil && ok
}
