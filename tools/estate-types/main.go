// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command estate-types is issue #435's board-wide type index: which
// resource types each gauntlet estate (live/gauntlet.json) actually
// exercises, so a re-measurement or a proposed estate can be targeted or
// justified against real coverage instead of a guess.
//
// It reads no state file and runs no gauntlet stage. Each estate's types
// come from static configuration: internal/live/check.Load - the same
// module-graph loader tools/corpus-fetch and tools/corpus-gen already use -
// reads the .corpus directory (or directories) spec.go traces to that
// estate's crossing script, and a handful of estates whose script writes
// its own root wiring with a heredoc add a text scan of the script itself.
// See spec.go's estateSpecs for the per-estate recipe and the run.sh lines
// it was traced against.
//
//	just corpus-fetch                # populate .corpus first
//	go run ./tools/estate-types       # regenerate live/estate-types.json
//	go run ./tools/estate-types -check # exit 1 if the committed file is stale
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
)

func main() {
	check := flag.Bool("check", false, "exit 1 if live/estate-types.json is not what a fresh run would produce, without writing it")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}

	art, err := Generate(context.Background(), root)
	if err != nil {
		fatal(err)
	}

	if *check {
		committed, err := Read(root)
		if err != nil {
			fatal(fmt.Errorf("reading committed %s: %w", ArtifactPath, err))
		}
		if !reflect.DeepEqual(committed, art) {
			fmt.Fprintf(os.Stderr, "%s is stale; run \"go run ./tools/estate-types\" and commit the result\n", ArtifactPath)
			os.Exit(1)
		}
		fmt.Printf("%s is current (%d estates, %d distinct types)\n", ArtifactPath, art.Totals.Estates, art.Totals.DistinctTypes)
		return
	}

	if err := Write(root, art); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s: %d estates, %d distinct types, %d in no cohort\n",
		ArtifactPath, art.Totals.Estates, art.Totals.DistinctTypes, art.Totals.TypesInNoCohort)
}

func repoRoot() (string, error) {
	out, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not in a git checkout: %w", err)
	}
	return out, nil
}

// gitOutput runs git in dir ("" for the working directory) and returns its
// trimmed stdout. On failure the error carries git's own first line of
// stderr, not just the bare "exit status 128" that exec.Cmd.Output()'s
// ExitError formats as (#1220, copied from tools/gauntlet/main.go, #1149).
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // a fixed subcommand list, arguments are internal
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "estate-types: %v\n", err)
	os.Exit(1)
}
