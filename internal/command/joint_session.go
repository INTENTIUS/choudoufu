// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A joint session is the directory the per-unit invocations of one
// Terragrunt or Atmos run share (GitHub issue #2213). It lives under the
// temporary directory, named for the tool process, and holds:
//
//	units/<id>.json   what each unit told us: directory, inputs, outputs
//	state.json        the latest joint plan and the joint apply, if any
//	root/             the composed root written from the units
//	joint.plan        the plan file of that root
//	lock              held while the root is written, planned or applied
type jointSession struct {
	dir string
}

// jointUnit is one unit's registration.
type jointUnit struct {
	ID      string              `json:"id"`
	Key     string              `json:"key"`
	Dir     string              `json:"dir"`
	Seq     int                 `json:"seq"`
	Vars    map[string]jointVar `json:"vars"`
	Outputs []jointOutputDecl   `json:"outputs"`
	// Out is the plan file the unit's "plan -out" asked for. Every plan of
	// the session rewrites each unit's file, so all of them hold the last
	// joint plan.
	Out string `json:"out,omitempty"`
}

type jointVar struct {
	Type  json.RawMessage `json:"type"`
	Value json.RawMessage `json:"value"`
}

type jointOutputDecl struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// jointState is what the session knows of its joint plan and apply.
type jointState struct {
	Serial int      `json:"serial"`
	Hash   string   `json:"hash"`
	OK     bool     `json:"ok"`
	Err    string   `json:"err,omitempty"`
	Units  []string `json:"units"`

	Applied  bool   `json:"applied,omitempty"`
	ApplyOK  bool   `json:"apply_ok,omitempty"`
	ApplyBy  string `json:"apply_by,omitempty"`
	ApplyErr string `json:"apply_err,omitempty"`

	// Root is the directory of the composed root, when it is not root/: a
	// session rebuilt from a plan file lays the configuration out from the
	// file's snapshot.
	Root string `json:"root,omitempty"`
}

// jointStub is what a unit's "plan -out" file holds in place of a plan.
// Session points at the session that wrote it, on the machine that wrote
// it; Bundle carries the joint plan itself, so that the file can be applied
// once that session is gone (another CI job, another machine).
type jointStub struct {
	Magic   int      `json:"choudoufu_joint_plan"`
	Session string   `json:"session"`
	Unit    string   `json:"unit"`
	Serial  int      `json:"serial"`
	Units   []string `json:"units"`

	// Run is the id of the tool run that planned (the session's name), and
	// Tool its kind.
	Run    string       `json:"run,omitempty"`
	Tool   string       `json:"tool,omitempty"`
	Bundle *jointBundle `json:"bundle,omitempty"`
}

// rootDir is the directory of the session's composed root.
func (s *jointSession) rootDir(st jointState) string {
	if st.Root != "" {
		return st.Root
	}
	return s.path("root")
}

func jointSessionsRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("choudoufu-joint-%d", os.Getuid()))
}

func openJointSession(id string) (*jointSession, error) {
	root := jointSessionsRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	jointSweep(root)
	s := &jointSession{dir: filepath.Join(root, jointSafe(id))}
	if err := os.MkdirAll(filepath.Join(s.dir, "units"), 0o700); err != nil {
		return nil, err
	}
	return s, nil
}

// jointSweep removes sessions a day old, so the temporary directory does not
// collect a provider cache per CI run.
func jointSweep(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

func (s *jointSession) path(parts ...string) string {
	return filepath.Join(append([]string{s.dir}, parts...)...)
}

// jointTimeout is how long an invocation waits for the session's lock.
func jointTimeout() time.Duration {
	if v := os.Getenv("CHOUDOUFU_JOINT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Minute
}

// lock takes the session's lock, waiting up to the timeout. A lock whose
// holder has exited is taken over.
func (s *jointSession) lock() (func(), error) {
	path := s.path("lock")
	deadline := time.Now().Add(jointTimeout())
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if b, rerr := os.ReadFile(path); rerr == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && !jointAlive(pid) {
				_ = os.Remove(path)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for another unit of this run to finish planning or applying the joint run (lock %s); set CHOUDOUFU_JOINT_TIMEOUT to wait longer", jointTimeout(), path)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func jointAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func (s *jointSession) loadState() jointState {
	var st jointState
	if b, err := os.ReadFile(s.path("state.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (s *jointSession) saveState(st jointState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(s.path("state.json"), b, 0o600)
}

func (s *jointSession) units() []jointUnit {
	entries, _ := os.ReadDir(s.path("units"))
	var out []jointUnit
	for _, e := range entries {
		b, err := os.ReadFile(s.path("units", e.Name()))
		if err != nil {
			continue
		}
		var u jointUnit
		if json.Unmarshal(b, &u) == nil {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// register stores u, keeping the id and place of a unit already registered
// under the same key. The caller holds the lock.
func (s *jointSession) register(u jointUnit, name string) jointUnit {
	existing := s.units()
	maxSeq := 0
	taken := map[string]bool{}
	for _, e := range existing {
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
		taken[e.ID] = true
		if e.Key == u.Key {
			u.ID, u.Seq = e.ID, e.Seq
			if u.Out == "" {
				u.Out = e.Out
			}
		}
	}
	if u.ID == "" {
		u.ID = name
		for n := 2; taken[u.ID]; n++ {
			u.ID = fmt.Sprintf("%s_%d", name, n)
		}
		u.Seq = maxSeq + 1
	}
	b, _ := json.MarshalIndent(u, "", "  ")
	_ = os.WriteFile(s.path("units", u.ID+".json"), b, 0o600)
	return u
}

// claimPrinted reports whether this is the first invocation to ask to print
// the joint plan of the given serial, so that it is printed once.
func (s *jointSession) claimPrinted(serial int) bool {
	f, err := os.OpenFile(s.path(fmt.Sprintf("printed-%d", serial)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
