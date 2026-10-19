// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/intentius/choudoufu/tools/statpages"
)

func main() {
	render := flag.Bool("render", false,
		"re-render live/SUBSTRATES.md from the committed live/substrates.json instead of regenerating the artifact")
	flag.Parse()

	if *render {
		if err := runRender(); err != nil {
			fmt.Fprintf(os.Stderr, "substrates-gen: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "substrates-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	artifact, err := Build(root)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", OutputJSONRel, err)
	}
	data = append(data, '\n')
	path := filepath.Join(root, filepath.FromSlash(OutputJSONRel))
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return fmt.Errorf("writing %s: %w", OutputJSONRel, err)
	}
	fmt.Printf("%s: %d rows\n", OutputJSONRel, len(artifact.Rows))
	return nil
}

// runRender re-renders live/SUBSTRATES.md from the already-committed
// live/substrates.json, the same split tools/readiness-gen's -render mode
// uses: it needs no provider, no network, and does not recompute the
// artifact. It used to copy the artifact to site/data/substrates.json for
// the Hugo site too; that copy went with the Hugo site (#2092).
func runRender() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if wrote, err := statpages.Write(root, statpages.Substrates); err != nil {
		return err
	} else if wrote {
		fmt.Printf("%s: rewrote\n", statpages.Substrates.Path)
	}
	return nil
}
