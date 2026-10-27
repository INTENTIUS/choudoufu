// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Tools are the optional neighbours an investigation reaches: chant, for
// the template catalog and path templates, and behold, for Cypher
// templates. Both are found on PATH unless named; neither is required.
type Tools struct {
	// Chant and Behold are binaries to use instead of the ones on PATH.
	// "none" means absent even when one is on PATH.
	Chant  string
	Behold string

	// TemplatesFile is a templates.json to read instead of chant's.
	TemplatesFile string

	// Dir is the directory chant search and behold mcp run in: the chant
	// project or workspace the question is about. Empty is the current
	// directory.
	Dir string

	// Env is extra environment for the children, after the process's own.
	Env []string
}

// ChantPath is the chant binary, or "" when there is none.
func (t *Tools) ChantPath() string { return lookTool(t.Chant, "chant") }

// BeholdPath is the behold binary, or "" when there is none.
func (t *Tools) BeholdPath() string { return lookTool(t.Behold, "behold") }

func lookTool(given, name string) string {
	switch given {
	case "none":
		return ""
	case "":
		p, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		return p
	default:
		if p, err := exec.LookPath(given); err == nil {
			return p
		}
		return ""
	}
}

// investigationChild is the whole set of child processes the investigation
// path may start: chant's read-only search, and behold's MCP (whose only
// tool this path calls is ask; see [RunTemplate]). Anything else is refused
// before it is started, so no investigation can reach an apply, an Op, an
// approval or a write through a neighbour. The MCP server's chant Ops
// pass-through is a separate path ([ChantOps]) and never comes through
// here.
func investigationChild(tool string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("investigate starts no %s without arguments", tool)
	}
	switch {
	case tool == "chant" && args[0] == "search":
		for _, a := range args[1:] {
			switch a {
			case "--check-live", "--check-snapshot", "--fail-on-drift":
				// Read-only too, but they turn an answer into a gate, and
				// an investigation is an answer.
				return fmt.Errorf("investigate does not pass %s to chant search", a)
			}
		}
		return nil
	case tool == "behold" && args[0] == "mcp":
		return nil
	}
	return fmt.Errorf("investigate never runs %s %s: an investigation only reads, through chant search and behold mcp's ask", tool, strings.Join(args, " "))
}

func (t *Tools) dir() string {
	if t.Dir == "" {
		return "."
	}
	return t.Dir
}

func (t *Tools) env() []string {
	return append(os.Environ(), t.Env...)
}

// runChant runs chant with args in Dir and returns its stdout. A chant that
// exits non-zero with a JSON document on stdout (a refusal under --json)
// returns that document and no error, so the caller reads the refusal.
func (t *Tools) runChant(ctx context.Context, args ...string) ([]byte, error) {
	chant := t.ChantPath()
	if chant == "" {
		return nil, fmt.Errorf("no chant binary on PATH")
	}
	if err := investigationChild("chant", args); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, chant, args...)
	cmd.Dir = t.dir()
	cmd.Env = t.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := bytes.TrimSpace(stdout.Bytes())
	if err != nil {
		if len(out) > 0 && out[0] == '{' {
			return out, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("chant %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// ChantProject reports whether dir is inside a chant project: a
// chant.config.ts or chant.config.json in dir or above it, up to the first
// .git or package.json without one (chant's own findProjectConfig walk).
func ChantProject(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		for _, f := range []string{"chant.config.ts", "chant.config.json"} {
			if _, err := os.Stat(filepath.Join(abs, f)); err == nil {
				return true
			}
		}
		for _, f := range []string{".git", "package.json"} {
			if _, err := os.Stat(filepath.Join(abs, f)); err == nil {
				return false
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
}
