// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// prepareSumaformModules is corpus-sumaform-aws's one exception to a plain
// [estateSpec.ConfigDirs] read: .corpus/sumaform/modules/base and
// .corpus/sumaform/modules/server each carry relative symlinks -
// versions.tf -> ../backend/<name>/versions.tf (sumaform's own
// pick-a-backend convention its README documents as a manual "ln -sf
// ../backend_modules/<BACKEND>/ modules/backend" step) and, on base,
// variables.tf -> ../../backend_modules/null/base/variables.tf (the
// backend-neutral variable defaults every backend shares) - that dangle
// until both siblings exist, which nothing in a fresh .corpus/sumaform
// checkout ever creates, and which run.sh's own copy_estate() only creates
// inside its private $WORK copy, never inside .corpus.
//
// Symlinking straight at the original directories does not fix this: a
// relative symlink resolves against its OWN real parent directory on disk,
// no matter what path was followed to reach it, so a symlink layer on top
// changes nothing. The only fix is what run.sh itself does - copy base/
// and server/ somewhere private, then add a real "backend" sibling there
// pointing at backend_modules/aws (the one backend this estate selects,
// matching run.sh's own "ln -sf ../backend_modules/aws/ modules/backend").
// This never touches .corpus, which is shared across worktrees and never
// written to (see every crossing script's own header comment on the
// point).
//
// Returns the two directories to run check.Load against and a cleanup
// function the caller must run when done.
func prepareSumaformModules(root string) (base, server string, cleanup func(), err error) {
	sumaform := filepath.Join(root, ".corpus", "sumaform")
	tmp, err := os.MkdirTemp("", "estate-types-sumaform-")
	if err != nil {
		return "", "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	modulesDir := filepath.Join(tmp, "modules")
	if err := os.Mkdir(modulesDir, 0o755); err != nil {
		cleanup()
		return "", "", nil, err
	}

	for _, name := range []string{"base", "server"} {
		src := filepath.Join(sumaform, "modules", name)
		dst := filepath.Join(modulesDir, name)
		// -R without -L: a real copy that keeps versions.tf as the
		// relative symlink it is in .corpus (dangling until the "backend"
		// sibling below exists) rather than dereferencing it - see the doc
		// comment above for why a plain symlink to the original directory
		// does not work here.
		if out, err := exec.Command("cp", "-R", src, dst).CombinedOutput(); err != nil {
			cleanup()
			return "", "", nil, fmt.Errorf("copying %s: %w: %s", src, err, out)
		}
	}

	// modules/backend -> backend_modules/aws: the one backend this estate
	// selects (versions.tf's "../backend/<name>/versions.tf").
	if err := os.Symlink(filepath.Join(sumaform, "backend_modules", "aws"), filepath.Join(modulesDir, "backend")); err != nil {
		cleanup()
		return "", "", nil, err
	}
	// backend_modules -> the whole real tree, a peer of modules/ exactly as
	// run.sh's copy_estate() lays it out: base's variables.tf reaches
	// "../../backend_modules/null/..." directly, not through the "backend"
	// indirection above.
	if err := os.Symlink(filepath.Join(sumaform, "backend_modules"), filepath.Join(tmp, "backend_modules")); err != nil {
		cleanup()
		return "", "", nil, err
	}

	return filepath.Join(modulesDir, "base"), filepath.Join(modulesDir, "server"), cleanup, nil
}
