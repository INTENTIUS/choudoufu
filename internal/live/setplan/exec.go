// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Environment variables the children are given. See the package
// documentation for why each one is there.
const (
	EnvPluginCacheDir    = "TF_PLUGIN_CACHE_DIR"
	EnvCacheMayBreakLock = "TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE"
	EnvDataDir           = "TF_DATA_DIR"
)

// Exec is the real [Runner]: every stage is a child process of Bin with
// -chdir=ROOT.
type Exec struct {
	// Bin is the choudoufu executable, normally os.Executable().
	Bin string
	// Env is the children's whole environment. Build it with [ChildEnv].
	Env []string
	// EstateOf reads a root's live block. It is a function rather than a
	// child process because it is a parse, not a run: the command layer
	// owns the configuration loader.
	EstateOf func(dir string) (estate string, ok bool, err error)
}

// ChildEnv is parent with the shared plugin cache set and TF_DATA_DIR
// removed. TF_DATA_DIR names one data directory for a whole process; every
// root sharing one would install every root's modules over each other, so
// the command refuses it before this is called, and this drops it again in
// case a caller did not.
func ChildEnv(parent []string, pluginCacheDir string) []string {
	out := make([]string, 0, len(parent)+2)
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case EnvPluginCacheDir, EnvCacheMayBreakLock, EnvDataDir:
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		EnvPluginCacheDir+"="+pluginCacheDir,
		EnvCacheMayBreakLock+"=1",
	)
}

func (e Exec) Estate(_ context.Context, dir string) (string, bool, error) {
	return e.EstateOf(dir)
}

func (e Exec) Init(ctx context.Context, dir string, log io.Writer) error {
	_, _, err := e.run(ctx, dir, log, "init", "-input=false", "-no-color")
	return err
}

func (e Exec) Plan(ctx context.Context, dir, planFile string, log io.Writer) (bool, error) {
	code, _, err := e.run(ctx, dir, log, "plan", "-input=false", "-no-color", "-detailed-exitcode", "-out="+planFile)
	if err != nil && code == 2 {
		// -detailed-exitcode's "succeeded, with changes".
		return true, nil
	}
	return false, err
}

func (e Exec) Show(ctx context.Context, dir, planFile string, log io.Writer) (json.RawMessage, error) {
	_, stdout, err := e.runQuiet(ctx, dir, log, "show", "-json", "-no-color", planFile)
	if err != nil {
		return nil, err
	}
	out := bytes.TrimSpace(stdout)
	if !json.Valid(out) {
		return nil, fmt.Errorf("show -json printed something that is not one JSON value (%d bytes); see the log", len(out))
	}
	return json.RawMessage(out), nil
}

// run runs one stage with its output in the root's log and returns its exit
// code. A non-zero exit is an error whose text is the stage's own stderr,
// trimmed, because that is where every diagnostic goes.
func (e Exec) run(ctx context.Context, dir string, log io.Writer, args ...string) (int, []byte, error) {
	var stdout, stderr bytes.Buffer
	return e.exec(ctx, dir, log, io.MultiWriter(log, &stdout), &stdout, &stderr, args...)
}

// runQuiet is run for a stage whose stdout is data rather than prose: it is
// kept out of the log, which says how many bytes it was instead.
func (e Exec) runQuiet(ctx context.Context, dir string, log io.Writer, args ...string) (int, []byte, error) {
	var stdout, stderr bytes.Buffer
	code, out, err := e.exec(ctx, dir, log, &stdout, &stdout, &stderr, args...)
	fmt.Fprintf(log, "(%d bytes of stdout kept as data)\n", len(out))
	return code, out, err
}

func (e Exec) exec(ctx context.Context, dir string, log, stdoutW io.Writer, stdout, stderr *bytes.Buffer, args ...string) (int, []byte, error) {
	argv := append([]string{"-chdir=" + dir}, args...)
	fmt.Fprintf(log, "$ choudoufu %s\n", strings.Join(argv, " "))
	cmd := exec.CommandContext(ctx, e.Bin, argv...)
	cmd.Env = e.Env
	cmd.Stdin = nil
	cmd.Stdout = stdoutW
	cmd.Stderr = io.MultiWriter(log, stderr)
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return -1, stdout.Bytes(), fmt.Errorf("%s did not run: %w", args[0], err)
		}
		code = exitErr.ExitCode()
	}
	fmt.Fprintf(log, "(exit %d)\n", code)
	if code != 0 {
		return code, stdout.Bytes(), &StageError{Stage: args[0], Code: code, Stderr: tail(stderr.String(), 4000)}
	}
	return 0, stdout.Bytes(), nil
}

// StageError is a stage that exited non-zero.
type StageError struct {
	Stage  string
	Code   int
	Stderr string
}

func (e *StageError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("%s exited %d with nothing on stderr; see the log", e.Stage, e.Code)
	}
	return fmt.Sprintf("%s exited %d: %s", e.Stage, e.Code, msg)
}

// tail keeps the last n bytes of s, which is where a failing run's final
// diagnostic is.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
