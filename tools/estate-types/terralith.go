// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// terralithScaleGenScale is the scale prepareTerralith generates at, and it
// is deliberately the same one live/e2e/terralith-scale/run.sh crosses at
// (its own `SCALE="${SCALE:-1}"`).
//
// The scale changes how MANY instances of each type the estate holds and
// changes no type at all: every count in tools/terralith-gen's composition
// is linear in it, and the block set is fixed. This index answers "which
// types does this estate exercise", so scale 1 is the cheapest generation
// that yields the complete answer, and generating at 4 would produce a
// byte-for-byte identical Types list several times slower.
const terralithScaleGenScale = "1"

// prepareTerralith is terralith-scale's exception to a plain
// [estateSpec.ConfigDirs] read, and it is a different exception from
// corpus-sumaform-aws's next door.
//
// Sumaform's configuration exists in .corpus and merely cannot be read
// where it lies. This estate's configuration does not exist anywhere until
// something makes it: terralith-scale is the only gauntlet estate whose
// subject is a GENERATOR (tools/terralith-gen, issue #564) rather than a
// checked-in or fetched directory. Its crossing script's first act is to
// run that generator into a scratch directory, and there is deliberately no
// committed copy of the output - #564's "the same estate at 4x is -scale 4,
// not a new artifact".
//
// So neither of the two mechanisms this tool already has can see it.
// ConfigDirs has no directory to name. ScanScript finds nothing either: the
// script declares no `resource` block of its own, it shells out to the
// generator, and the resource blocks live in Go string templates in
// tools/terralith-gen/gen.go where no HCL reader will find them. A text
// scan of the Go source would be the wrong answer anyway - it would read
// the templates rather than the estate, and would keep reporting types that
// a change to the composition had stopped emitting.
//
// The honest read is to generate the estate the way the crossing script
// does and load what comes out, which is what this does. It runs the
// generator through `go run` into a private temp directory, with -fmt-bin
// "" so the read never depends on a terraform/tofu binary being installed,
// and returns the directory for check.Load plus its cleanup. Nothing is
// written inside the checkout.
//
// This keeps the tool's stated method true: the types still come from
// static configuration, read by check.Load, with no gauntlet run, no
// docker and no cloud calls. The only difference is that this estate's
// static configuration is produced on the spot, from the same generator at
// the same scale the crossing script uses, rather than read off disk.
func prepareTerralith(ctx context.Context, root string) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "estate-types-terralith-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	out := filepath.Join(tmp, "terralith")
	cmd := exec.CommandContext(ctx, "go", "run", "./tools/terralith-gen",
		"-scale", terralithScaleGenScale, "-out", out, "-fmt-bin", "")
	cmd.Dir = root
	// `env -u PWD`, for the same reason the justfile and every crossing
	// script spell it: a worktree reached through a symlink makes the Go
	// toolchain resolve packages against the symlinked PWD rather than
	// cmd.Dir, and fail to find them. Unset, not emptied - an empty PWD is
	// still a PWD.
	cmd.Env = withoutPWD(os.Environ())
	if combined, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("generating the terralith estate: %w\n%s", err, combined)
	}
	return out, cleanup, nil
}

// withoutPWD returns env with any PWD entry removed.
func withoutPWD(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "PWD=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
