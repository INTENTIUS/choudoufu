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
func RunEstates(root string, m *Manifest, a *Artifact, opts RunOptions, commit string) (int, error) {
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
		r.LastRun = &LastRun{Commit: commit, Date: time.Now().UTC().Format(time.RFC3339), ExitCode: exit}
		if res.Spoken {
			if r.Stages == nil {
				r.Stages = map[string]string{}
			}
			for id, v := range res.Stages {
				r.Stages[id] = v
			}
			if len(res.Detail) > 0 {
				r.LastRun.Detail = res.Detail
			}
			r.Protocol = ProtocolGauntlet
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
