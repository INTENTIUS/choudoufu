// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package affected

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// change is one entry of the range's diff. Old is the path at the base
// revision and New the path at the head revision; an added file has no Old,
// a deleted one no New, and a rename has both.
type change struct {
	Old, New string
}

// git runs git in dir and returns its standard output.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// revParse resolves rev to a full commit hash.
func revParse(ctx context.Context, dir, rev string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q is not a commit in this repository", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveRange reads a range the way git log reads one: "A..B" is B against
// A, "A...B" is B against their merge base, and a single revision "A" is
// HEAD against A. An empty side of ".." or "..." is HEAD.
func resolveRange(ctx context.Context, dir, spec string) (base, head string, err error) {
	if strings.TrimSpace(spec) == "" {
		return "", "", errors.New("no range given")
	}
	orHead := func(s string) string {
		if s == "" {
			return "HEAD"
		}
		return s
	}
	switch {
	case strings.Contains(spec, "..."):
		a, b, _ := strings.Cut(spec, "...")
		if head, err = revParse(ctx, dir, orHead(b)); err != nil {
			return "", "", err
		}
		a2, err := revParse(ctx, dir, orHead(a))
		if err != nil {
			return "", "", err
		}
		out, err := git(ctx, dir, "merge-base", a2, head)
		if err != nil {
			return "", "", fmt.Errorf("%s and %s have no merge base", orHead(a), orHead(b))
		}
		return strings.TrimSpace(string(out)), head, nil
	case strings.Contains(spec, ".."):
		a, b, _ := strings.Cut(spec, "..")
		if base, err = revParse(ctx, dir, orHead(a)); err != nil {
			return "", "", err
		}
		if head, err = revParse(ctx, dir, orHead(b)); err != nil {
			return "", "", err
		}
		return base, head, nil
	default:
		if base, err = revParse(ctx, dir, spec); err != nil {
			return "", "", err
		}
		if head, err = revParse(ctx, dir, "HEAD"); err != nil {
			return "", "", err
		}
		return base, head, nil
	}
}

// diff lists the files that differ between base and head, with renames
// detected so a file moved out of a module is read at both of its paths.
func diff(ctx context.Context, dir, base, head string) ([]change, error) {
	out, err := git(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--name-status", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	var changes []change
	for i := 0; i < len(fields); {
		status := fields[i]
		if status == "" {
			i++
			continue
		}
		switch status[0] {
		case 'R', 'C':
			if i+2 >= len(fields) {
				return nil, fmt.Errorf("git diff: truncated %s entry", status)
			}
			if status[0] == 'R' {
				changes = append(changes, change{Old: fields[i+1], New: fields[i+2]})
			} else {
				// A copy leaves its source where it was.
				changes = append(changes, change{New: fields[i+2]})
			}
			i += 3
		default:
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("git diff: truncated %s entry", status)
			}
			p := fields[i+1]
			switch status[0] {
			case 'A':
				changes = append(changes, change{New: p})
			case 'D':
				changes = append(changes, change{Old: p})
			default:
				changes = append(changes, change{Old: p, New: p})
			}
			i += 2
		}
	}
	return changes, nil
}

// extract writes the tree of commit rev into dest, which must exist, so the
// configuration can be loaded as it stood at that revision without touching
// the working tree or the repository's own worktree list.
func extract(ctx context.Context, repo, rev, dest string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", rev)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(pipe)
	var werr error
	for werr == nil {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			werr = err
			break
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			werr = fmt.Errorf("git archive of %s holds %q, which is not a path inside the tree", rev, hdr.Name)
			break
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			werr = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			if werr = os.MkdirAll(filepath.Dir(target), 0o755); werr != nil {
				break
			}
			var f *os.File
			if f, werr = os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); werr != nil {
				break
			}
			_, werr = io.Copy(f, tr) //nolint:gosec // a git tree of the user's own repository
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
		case tar.TypeSymlink:
			if werr = os.MkdirAll(filepath.Dir(target), 0o755); werr != nil {
				break
			}
			werr = os.Symlink(hdr.Linkname, target)
		}
	}
	if werr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("extracting %s: %w", rev, werr)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s: %s", rev, strings.TrimSpace(stderr.String()))
	}
	return nil
}
