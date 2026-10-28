// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// scenario-estate renders a stock estate of -scale copies of a scenario
// tree (default live/e2e/estate) for the smoke scenarios and tests that
// need an estate to run against (choudoufu#2303). It replaces
// tools/terralith-gen there; the terralith stays for the scale ladder only.
// See internal/live/scenarioestate for what a render writes.
//
//	go run ./tools/scenario-estate -scale 2 -prefix tl -out /tmp/estate
//	go run ./tools/scenario-estate -scale 2 -count   # print the summary only
//
// It prints one JSON summary line on stdout: tree, scale, resources,
// per_copy and taggable.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/intentius/choudoufu/internal/live/scenarioestate"
)

func main() {
	scale := flag.Int("scale", 1, "how many copies of the scenario tree to write")
	prefix := flag.String("prefix", "sc", "replaces the tree's own estate name in every client-chosen name, as <prefix>-<copy>, so two renders coexist in one account")
	out := flag.String("out", "", "output directory, created empty (required unless -count)")
	tree := flag.String("tree", "", "scenario tree directory (default "+scenarioestate.DefaultTree+" under the working directory)")
	count := flag.Bool("count", false, "print what a render at -scale declares and write nothing")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	var s scenarioestate.Summary
	if *count {
		s, err = scenarioestate.Count(root, *tree, *scale)
	} else {
		if *out == "" {
			fail(fmt.Errorf("-out is required"))
		}
		s, err = scenarioestate.Render(scenarioestate.Options{Tree: *tree, RepoRoot: root, Scale: *scale, Prefix: *prefix, Out: *out})
	}
	if err != nil {
		fail(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		fail(err)
	}
	fmt.Println(string(b))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "scenario-estate: %v\n", err)
	os.Exit(1)
}
