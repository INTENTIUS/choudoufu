// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

// repo wraps git invocations against one repository. Blob reads go through
// a persistent `git cat-file --batch` process, and parsed line sets are
// cached by blob SHA so the same blob is never normalized twice across the
// ~150 merges of a full scan.
type repo struct {
	dir      string
	minLen   int
	batchCmd *exec.Cmd
	batchIn  io.WriteCloser
	batchOut *bufio.Reader
	// blob sha -> parsed lines, or nil for generated/binary blobs.
	lineCache map[string]map[string]string
	// blob sha -> whitespace-split token stream of the whole body.
	tokenCache map[string][]string
	// commit sha -> directories (with trailing slash) carrying a GENERATED.md.
	genDirCache map[string][]string
}

func openRepo(dir string, minLen int) (*repo, error) {
	r := &repo{
		dir:         dir,
		minLen:      minLen,
		lineCache:   map[string]map[string]string{},
		tokenCache:  map[string][]string{},
		genDirCache: map[string][]string{},
	}
	cmd := exec.Command("git", "-C", dir, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting cat-file --batch: %w", err)
	}
	r.batchCmd = cmd
	r.batchIn = in
	r.batchOut = bufio.NewReaderSize(out, 1<<20)
	return r, nil
}

func (r *repo) close() {
	r.batchIn.Close()
	r.batchCmd.Wait()
}

// git runs a git subcommand and returns its stdout.
func (r *repo) git(args ...string) (string, error) {
	full := append([]string{"-C", r.dir}, args...)
	cmd := exec.Command("git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// gitOK is git() for commands where a non-zero exit is a legitimate answer
// (e.g. `git grep` with no matches). It returns stdout and whether the
// command exited zero.
func (r *repo) gitOK(args ...string) (string, bool) {
	out, err := r.git(args...)
	return out, err == nil
}

// object fetches one object through the batch process. A missing object
// returns sha == "" with no error.
func (r *repo) object(spec string) (sha string, body []byte, err error) {
	if _, err = fmt.Fprintf(r.batchIn, "%s\n", spec); err != nil {
		return "", nil, err
	}
	header, err := r.batchOut.ReadString('\n')
	if err != nil {
		return "", nil, fmt.Errorf("cat-file read for %q: %w", spec, err)
	}
	fields := strings.Fields(strings.TrimSuffix(header, "\n"))
	if n := len(fields); n >= 2 && (fields[n-1] == "missing" || fields[n-1] == "ambiguous") {
		return "", nil, nil
	}
	if len(fields) != 3 {
		return "", nil, fmt.Errorf("cat-file: unexpected header %q for %q", header, spec)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", nil, fmt.Errorf("cat-file: bad size in %q: %w", header, err)
	}
	body = make([]byte, size+1) // trailing newline after the payload
	if _, err = io.ReadFull(r.batchOut, body); err != nil {
		return "", nil, fmt.Errorf("cat-file body for %q: %w", spec, err)
	}
	return fields[0], body[:size], nil
}

// linesOf returns the normalized line set of the blob at spec (rev:path or
// a bare sha) as normalized -> one original spelling. Missing blobs return
// an empty set; generated and binary blobs return nil, meaning "carries no
// attributable hand-written lines".
func (r *repo) linesOf(spec string) (map[string]string, error) {
	sha, body, err := r.object(spec)
	if err != nil {
		return nil, err
	}
	if sha == "" {
		return map[string]string{}, nil
	}
	if cached, ok := r.lineCache[sha]; ok {
		return cached, nil
	}
	var set map[string]string
	if !isBinary(body) && !isGeneratedBlob(body) {
		set = parseLines(body, r.minLen)
	}
	r.lineCache[sha] = set
	return set, nil
}

// tokensOf returns the whole blob body as a whitespace-split token stream,
// for reflow-tolerant containment checks: content that survived a merge
// with only its line-wrap points moved is still the same token run.
// Binary blobs return nil; missing blobs return an empty stream.
func (r *repo) tokensOf(spec string) ([]string, error) {
	sha, body, err := r.object(spec)
	if err != nil {
		return nil, err
	}
	if sha == "" {
		return []string{}, nil
	}
	if cached, ok := r.tokenCache[sha]; ok {
		return cached, nil
	}
	var toks []string
	if !isBinary(body) {
		toks = strings.Fields(string(body))
	}
	r.tokenCache[sha] = toks
	return toks, nil
}

func isBinary(body []byte) bool {
	probe := body
	if len(probe) > 8000 {
		probe = probe[:8000]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// isGeneratedBlob reports whether the blob's header declares it generated.
func isGeneratedBlob(body []byte) bool {
	probe := body
	if len(probe) > 2048 {
		probe = probe[:2048]
	}
	return bytes.Contains(probe, []byte("DO NOT EDIT")) ||
		bytes.Contains(probe, []byte("Code generated"))
}

// parseLines builds the normalized set, excluding generated span regions
// (lines between markers containing "-gen:begin" and "-gen:end", markers
// included: the span interior is regenerable, not authored).
func parseLines(body []byte, minLen int) map[string]string {
	set := map[string]string{}
	inSpan := false
	for _, raw := range strings.Split(string(body), "\n") {
		if strings.Contains(raw, "-gen:begin") {
			inSpan = true
		}
		if inSpan {
			if strings.Contains(raw, "-gen:end") {
				inSpan = false
			}
			continue
		}
		if n, ok := normLine(raw, minLen); ok {
			if _, dup := set[n]; !dup {
				set[n] = raw
			}
		}
	}
	return set
}

// normLine collapses whitespace runs to single spaces so indentation and
// alignment churn does not register as loss, and drops lines that are too
// short or letterless to attribute to anyone.
func normLine(raw string, minLen int) (string, bool) {
	f := strings.Fields(raw)
	if len(f) == 0 {
		return "", false
	}
	s := strings.Join(f, " ")
	if len(s) < minLen {
		return "", false
	}
	for _, c := range s {
		if unicode.IsLetter(c) {
			return s, true
		}
	}
	return "", false
}

// diffEntry is one -z name-status record from `git diff -M a b`.
type diffEntry struct {
	status byte   // A, M, D, R, C, T
	from   string // path in a ("" for A)
	to     string // path in b ("" for D)
}

func (r *repo) diffNameStatus(a, b string) ([]diffEntry, error) {
	out, err := r.git("diff", "-M", "--name-status", "-z", a, b)
	if err != nil {
		return nil, err
	}
	toks := strings.Split(out, "\x00")
	var entries []diffEntry
	for i := 0; i < len(toks); {
		st := toks[i]
		if st == "" {
			break
		}
		e := diffEntry{status: st[0]}
		switch e.status {
		case 'R', 'C':
			if i+2 >= len(toks) {
				return nil, fmt.Errorf("truncated rename record in diff %s..%s", a, b)
			}
			e.from, e.to = toks[i+1], toks[i+2]
			i += 3
		case 'D':
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("truncated diff record in %s..%s", a, b)
			}
			e.from, e.to = toks[i+1], ""
			i += 2
		case 'A':
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("truncated diff record in %s..%s", a, b)
			}
			e.from, e.to = "", toks[i+1]
			i += 2
		default: // M, T
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("truncated diff record in %s..%s", a, b)
			}
			e.from, e.to = toks[i+1], toks[i+1]
			i += 2
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// generatedDirs lists directory prefixes (with trailing slash) that carry a
// GENERATED.md in the given commit's tree: everything under them is
// generator output whatever its own header says.
func (r *repo) generatedDirs(commit string) ([]string, error) {
	if dirs, ok := r.genDirCache[commit]; ok {
		return dirs, nil
	}
	out, err := r.git("ls-tree", "-r", "--name-only", "-z", commit)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "GENERATED.md" || strings.HasSuffix(p, "/GENERATED.md") {
			dirs = append(dirs, strings.TrimSuffix(p, "GENERATED.md"))
		}
	}
	r.genDirCache[commit] = dirs
	return dirs, nil
}
