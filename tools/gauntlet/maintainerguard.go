// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maintainerguard.go: the maintainer's own words on this ("this all has to
// be setup to be blocked off and only enable manually when i actually care
// to do that shit... when im ready for a manual run with regression fixes i
// will decide so my fucking self") became a rule after the 2026-09-11
// incident CLAUDE.md records: three real-AWS certification cycles and two
// corpus runs went out overnight on an inferred authorization, with nobody
// having decided to spend that money or that time. LIVECERT_I_UNDERSTAND_
// THIS_SPENDS_REAL_MONEY is not a guard against that, because an agent can
// set its own environment variable; this file's check is, because nothing
// in this repository - no generator, no test, no agent action permitted by
// CLAUDE.md - is allowed to create the file it reads.
//
// The file lives OUTSIDE the repository (~/.config/choudoufu/allow-heavy-runs)
// so a fresh clone, a branch switch, or a worktree can never carry it by
// accident, and its content is a single instant, not a boolean, so a
// forgotten enable expires on its own rather than staying green forever:
// "until <RFC3339 or YYYY-MM-DDTHH:MM>", local time. A run is allowed only
// while that instant is still in the future. `just allow-heavy-runs 2h`
// prints the exact command to write it; nothing here ever runs that
// command itself.
//
// This is the Go half of one rule expressed twice - live/live-cert/lib/live-
// cert.sh's livecert_require_maintainer_allow is the shell half, read by the
// two live-cert scripts that bypass this binary entirely. Both refuse with
// the same message shape, naming the allow file and the `just
// allow-heavy-runs` recipe.

// MaintainerAllowFile is where the maintainer's own hand-run command
// writes the allow file - never this repository, never anything this
// binary itself creates.
func MaintainerAllowFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("maintainer-allow: could not resolve $HOME: %w", err)
	}
	return filepath.Join(home, ".config", "choudoufu", "allow-heavy-runs"), nil
}

// inCI reports whether this process is running inside a CI workflow rather
// than on a maintainer's own machine. Either CI or GITHUB_ACTIONS being set
// is enough: a scheduled or dispatched GitHub Actions run IS the
// maintainer's decision, made once when the workflow was authored and
// pinned to a schedule or a manual dispatch, not something re-derived from
// a file in $HOME that would not even exist on the runner.
func inCI() bool {
	return os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("CI") != ""
}

// allowTimestampLayouts are the two forms the allow file's "until" value
// may take, tried in order. Both are parsed in the local zone: a bare
// YYYY-MM-DDTHH:MM carries no offset of its own, and even a full RFC3339
// value is a wall-clock instant the maintainer typed by hand, not a UTC
// contract, so a comparison against time.Now() (also local) is what "still
// in the future" means here.
var allowTimestampLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
}

func parseAllowTimestamp(s string) (time.Time, error) {
	var lastErr error
	for _, layout := range allowTimestampLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}

// maintainerAllowReason is the guard's decision function, pure and
// independent of the filesystem and the clock so it is directly unit
// testable: given whether the allow file exists, its first line, and the
// current instant, it returns "" when a heavy/paid run may proceed, or a
// short phrase naming why not.
func maintainerAllowReason(exists bool, firstLine string, now time.Time) string {
	if !exists {
		return "the allow file does not exist"
	}
	line := strings.TrimSpace(firstLine)
	rest, ok := strings.CutPrefix(line, "until ")
	rest = strings.TrimSpace(rest)
	if !ok || rest == "" {
		return fmt.Sprintf("its first line is not \"until <timestamp>\" (got: %q)", line)
	}
	until, err := parseAllowTimestamp(rest)
	if err != nil {
		return fmt.Sprintf("its timestamp %q could not be parsed as RFC3339 or YYYY-MM-DDTHH:MM", rest)
	}
	if !now.Before(until) {
		return fmt.Sprintf("it expired at %s", until.Format("2006-01-02T15:04:05"))
	}
	return ""
}

// CheckMaintainerAllow is the entry point `run` and `live-cert` both call
// before starting any container or making any cloud call. It returns nil
// in CI (see inCI) or when the allow file names a future instant, and an
// error - ready to print and exit non-zero - naming the allow file and the
// `just allow-heavy-runs` recipe otherwise.
func CheckMaintainerAllow() error {
	if inCI() {
		return nil
	}
	path, err := MaintainerAllowFile()
	if err != nil {
		return err
	}
	b, readErr := os.ReadFile(path)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("maintainer-allow: reading %s: %w", path, readErr)
	}
	var firstLine string
	if exists {
		firstLine = strings.SplitN(string(b), "\n", 2)[0]
	}
	reason := maintainerAllowReason(exists, firstLine, time.Now())
	if reason == "" {
		return nil
	}
	return fmt.Errorf("refusing: %s - %s; run `just allow-heavy-runs 2h` for the exact command to paste - nothing has been started", path, reason)
}
