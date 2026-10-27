// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command modcompat-gen writes live/module-compat.json (GitHub issue #2292).
//
// With -sweep it rebuilds the static tier from a tools/refusal-probe sweep
// taken with -schemas -onboarded, and refreshes the gauntlet tier too:
//
//	go run ./tools/refusal-probe -schemas -onboarded -out sweep.json
//	go run ./tools/modcompat-gen -sweep sweep.json
//
// Without -sweep it keeps the committed static tier and refreshes only the
// gauntlet tier, which is what `go run ./tools/gauntlet render` also does.
//
// The probe needs provider schemas, which it installs from the public
// registry; it reaches no cloud and no emulator. The sweep file is input,
// never committed: the rows it yields are the artifact.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/intentius/choudoufu/tools/modcompat"
)

func main() {
	root := flag.String("root", ".", "repository root")
	sweep := flag.String("sweep", "", "a refusal-probe -schemas -onboarded sweep file; rebuilds the static tier")
	commit := flag.String("commit", "", "the choudoufu commit the probe was built from, required when the sweep recorded none (its -root was not a git checkout)")
	date := flag.String("date", time.Now().UTC().Format("2006-01-02"), "the day the sweep ran")
	flag.Parse()

	if err := run(*root, *sweep, *commit, *date); err != nil {
		fmt.Fprintln(os.Stderr, "modcompat-gen:", err)
		os.Exit(1)
	}
}

func run(root, sweepPath, commit, date string) error {
	f, err := modcompat.Read(root)
	if err != nil {
		return err
	}
	if sweepPath != "" {
		s, err := modcompat.ReadSweep(sweepPath)
		if err != nil {
			return err
		}
		m, rows, err := modcompat.StaticTier(root, s, modcompat.StaticOptions{Date: date, Commit: commit})
		if err != nil {
			return err
		}
		f.StaticMeasurement, f.Static = m, rows
	}
	g, err := modcompat.GauntletRows(root)
	if err != nil {
		return err
	}
	f.Gauntlet = g
	wrote, err := modcompat.Write(root, f)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range f.Static {
		counts[r.Verdict]++
	}
	fmt.Printf("static: %d rows (clear %d, refused %d, unmeasured %d)\n", len(f.Static), counts[modcompat.VerdictClear], counts[modcompat.VerdictRefused], counts[modcompat.VerdictUnmeasured])
	fmt.Printf("gauntlet: %d current rows\n", len(f.Gauntlet))
	if wrote {
		fmt.Println("rewrote", modcompat.Rel)
	} else {
		fmt.Println(modcompat.Rel, "unchanged")
	}
	return nil
}
