// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LogDir holds one stdout+stderr capture per estate run. Gitignored.
const LogDir = "live/gauntlet/logs"

// RunOptions controls a run.
type RunOptions struct {
	Names  []string // estates to run; empty means the selected set
	Set    string   // "core", "all"; used when Names is empty
	Env    []string // extra KEY=VALUE for every script
	Stdout io.Writer
}

// RunEstates executes each selected estate's script, parses the protocol,
// and updates the artifact in memory. It returns the number of scripts that
// exited non-zero. The caller saves and renders.
//
// emulator is stamped onto every row's LastRun exactly as commit already is:
// it is the pin the caller read from live/floci-image right before calling
// this (main.go's cmdRun), the same file each script itself reads to launch
// its own emulator (FLOCI_IMAGE defaults to `cat live/floci-image`, e.g.
// live/e2e/corpus-vpc-complete/run.sh:262) - so what gets recorded is what
// that run actually used, not a value borrowed from configuration at some
// later render. It is never read back out of a.Emulator here, on purpose:
// a.Emulator is what the NEXT run will use, which is a different fact than
// what THIS run used, even though the two are equal at the instant this
// function is called.
func RunEstates(root string, m *Manifest, a *Artifact, opts RunOptions, commit, emulator string) (int, error) {
	var selected []Estate
	if len(opts.Names) > 0 {
		for _, n := range opts.Names {
			e, ok := m.ByName(n)
			if !ok {
				return 0, fmt.Errorf("estate %q is not in %s", n, ManifestPath)
			}
			selected = append(selected, e)
		}
	} else {
		for _, e := range m.Estates {
			if opts.Set == "core" && e.Set != SetCore {
				continue
			}
			selected = append(selected, e)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, LogDir), 0o755); err != nil {
		return 0, err
	}
	failures := 0
	for _, e := range selected {
		r, _ := a.Result(e.Name)
		r.Name = e.Name
		res, exit, err := runOne(root, e, opts)
		if err != nil {
			return failures, err
		}
		if exit != 0 {
			failures++
		}
		// A stage's detail text is carried forward across runs the same
		// way its verdict already is (the loop just below): a script that
		// now fails or aborts at an EARLIER stage than a prior run reached
		// still only reports that earlier stage's line, and a bare
		// `r.LastRun.Detail = res.Detail` here would silently drop every
		// later stage's detail from the artifact even though its verdict
		// in r.Stages is untouched - found re-verifying
		// corpus-giantswarm-crossplane after the record-orphan-read sweep
		// (610511fb73): day2_rename regressed from pass to fail, the
		// script now exits before reaching day2_remove, and a
		// replace-not-merge Detail map turned day2_remove's rich,
		// previously-recorded wall text into an empty string although its
		// stage verdict correctly stayed "fail". Merging preserves stale
		// detail for a stage this run never reached, exactly like Stages
		// already does for the verdict itself.
		prevDetail := map[string]string{}
		if r.LastRun != nil {
			for k, v := range r.LastRun.Detail {
				prevDetail[k] = v
			}
		}
		r.LastRun = &LastRun{Commit: commit, Date: time.Now().UTC().Format(time.RFC3339), Emulator: emulator, ExitCode: exit}
		if res.Spoken {
			if r.Stages == nil {
				r.Stages = map[string]string{}
			}
			for id, v := range res.Stages {
				r.Stages[id] = v
			}
			for id, v := range res.Detail {
				prevDetail[id] = v
			}
			if len(prevDetail) > 0 {
				r.LastRun.Detail = prevDetail
			}
			r.Protocol = ProtocolGauntlet
			if len(res.Stages) == 0 {
				// Spoke the protocol (printed GAUNTLET protocol=1) but died
				// before a single GAUNTLET stage= line - e.g. failed the
				// step-0 tool/corpus check. r.Stages above is unchanged from
				// the prior run in this case (the merge loop ran zero
				// times), so every existing verdict, including a full pass,
				// is being carried forward untouched. The legacy branch
				// below already warns this loudly for res.Spoken == false;
				// this case is otherwise silent, so warn here too.
				fmt.Fprintf(opts.Stdout, "%s: script spoke the gauntlet protocol but reported no stage verdicts this run; verdicts left as recorded, exit code %d noted\n", e.Name, exit)
			}
			for _, u := range res.Unknown {
				fmt.Fprintf(opts.Stdout, "%s: reported unknown stage %q; add it to tools/gauntlet/stages.go or fix the script\n", e.Name, u)
			}
		} else {
			// Legacy script: verdicts stay as imported; only the run is recorded.
			if r.Protocol == "" {
				r.Protocol = ProtocolLegacy
			}
			fmt.Fprintf(opts.Stdout, "%s: script does not speak the gauntlet protocol (source live/e2e/lib/gauntlet.sh); verdicts left as recorded, exit code %d noted\n", e.Name, exit)
		}
		a.SetResult(r)
		fmt.Fprintf(opts.Stdout, "%s: exit %d, %s\n", e.Name, exit, summarize(r.Stages))
	}
	return failures, nil
}

func runOne(root string, e Estate, opts RunOptions) (*ProtocolResult, int, error) {
	script := filepath.Join(root, e.ScriptPath())
	if _, err := os.Stat(script); err != nil {
		return nil, 0, fmt.Errorf("estate %q: %w", e.Name, err)
	}
	logPath := filepath.Join(root, LogDir, e.Name+".log")
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, 0, err
	}
	defer logf.Close()

	var captured bytes.Buffer
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Stdout = io.MultiWriter(&captured, logf)
	cmd.Stderr = logf
	fmt.Fprintf(opts.Stdout, "%s: running %s (log: %s)\n", e.Name, e.ScriptPath(), filepath.Join(LogDir, e.Name+".log"))
	runErr := cmd.Run()
	exit := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return nil, 0, fmt.Errorf("estate %q: %w", e.Name, runErr)
		}
	}
	res, err := ParseProtocol(&captured)
	if err != nil {
		return nil, exit, fmt.Errorf("estate %q: %w", e.Name, err)
	}
	return res, exit, nil
}

func summarize(stages map[string]string) string {
	var parts []string
	for _, s := range Stages() {
		if s.Status != StatusActive {
			continue
		}
		parts = append(parts, s.ID+"="+stages[s.ID])
	}
	return strings.Join(parts, " ")
}
