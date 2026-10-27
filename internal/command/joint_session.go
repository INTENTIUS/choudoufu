// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
//	sensitive.key     the key of the hashes of sensitive planned values (#2277),
//	                  unless CHOUDOUFU_PLAN_HMAC_KEY holds it
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

	// Ref is the tool's name for the unit, which other units' wiring
	// names: the Terragrunt unit directory, or an Atmos stack and component.
	Ref string `json:"ref,omitempty"`
	// Wired are the inputs read from other units' outputs, which the
	// composed root takes from the joint plan in place of Vars (#2231).
	Wired map[string]jointWire `json:"wired,omitempty"`
	// Unsure are reads of other units' outputs not traced to an input.
	Unsure []jointUnsure `json:"unsure,omitempty"`
	// Workspace is the non-default workspace the tool selected for the
	// unit, under which its outputs are published after the joint apply.
	Workspace string `json:"workspace,omitempty"`
}

type jointVar struct {
	Type  json.RawMessage `json:"type"`
	Value json.RawMessage `json:"value"`
	// Sensitive is the unit's own variable declaring sensitive = true.
	Sensitive bool `json:"sensitive,omitempty"`
	// Withheld is set on another unit's sensitive input in a unit's plan
	// file, whose Value is then null: only the input's own unit's plan file
	// carries it, and an apply puts it back from there (#2270).
	Withheld bool `json:"withheld,omitempty"`
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

// jointKeyFile is the session's key for the hashes a plan file keeps of
// the sensitive values it drops (INTENTIUS/choudoufu#2277).
const jointKeyFile = "sensitive.key"

// jointKeyEnv names the environment variable that, when set, holds the key
// of the hashes in place of the session's key file (the #2277 follow-up
// ruling, "Key from a CI secret"). CI stores one secret, and a joint plan
// made in one job can be applied in another or on another machine. The
// value is hex or base64 of at least 32 bytes. It is never written to a
// plan file or the session directory, and never printed.
const jointKeyEnv = "CHOUDOUFU_PLAN_HMAC_KEY"

// jointKeyFromEnv is the bundle's KeyFrom when the key came from
// [jointKeyEnv].
const jointKeyFromEnv = "env"

// jointMinEnvKey is the shortest key [jointKeyEnv] may hold, in bytes.
const jointMinEnvKey = 32

// jointEnvKey returns the key [jointKeyEnv] holds. ok is false when the
// variable is unset or empty. An error names the variable and what is
// wrong with it, never its value.
func jointEnvKey() (key []byte, ok bool, err error) {
	v := strings.TrimSpace(os.Getenv(jointKeyEnv))
	if v == "" {
		return nil, false, nil
	}
	key, err = jointDecodeKey(v)
	if err != nil {
		return nil, true, err
	}
	return key, true, nil
}

// jointDecodeKey decodes a key written as hex or as base64 (standard or
// URL alphabet, padded or not), and refuses one shorter than
// [jointMinEnvKey] bytes.
func jointDecodeKey(v string) ([]byte, error) {
	var key []byte
	if b, err := hex.DecodeString(v); err == nil {
		key = b
	} else {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(v); err == nil {
				key = b
				break
			}
		}
		if key == nil {
			return nil, fmt.Errorf("%s is neither hex nor base64. Set it to hex or base64 of at least %d random bytes, for example the output of \"openssl rand -hex 32\"", jointKeyEnv, jointMinEnvKey)
		}
	}
	if len(key) < jointMinEnvKey {
		return nil, fmt.Errorf("%s decodes to %d bytes, and the key of the hashes of sensitive planned values must be at least %d bytes, so that a short secret cannot be guessed from a plan file. Set it to hex or base64 of at least %d random bytes, for example the output of \"openssl rand -hex 32\"", jointKeyEnv, len(key), jointMinEnvKey, jointMinEnvKey)
	}
	return key, nil
}

// jointKeyCheckLabel is what [jointKeyCheck] hashes.
const jointKeyCheckLabel = "choudoufu joint plan key check (#2277)"

// jointKeyCheck is an HMAC of a fixed label under key. A plan file keeps it
// so that an apply holding another key refuses with "the key differs"
// rather than naming every sensitive value as changed. It tells nothing
// about the key, which is at least 32 random bytes, or about any value.
func jointKeyCheck(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(jointKeyCheckLabel))
	return hex.EncodeToString(m.Sum(nil))
}

// sensitiveKey returns the key of this session's hashes, and where it came
// from: [jointKeyFromEnv] when [jointKeyEnv] is set, or "" for the
// session's key file.
//
// The key file is made on first use: 32 random bytes in a 0600 file of the
// session's 0700 directory. The key never goes into a plan file. A plan
// file keeps an HMAC of each sensitive value under this key rather than a
// plain hash, so that a low-entropy secret (a short password, a PIN) cannot
// be found from the file by hashing guesses: without the key a guess cannot
// be checked. The cost of the file is that only a machine that still has
// this session directory can check the values at apply; a key from
// [jointKeyEnv] lifts that.
func (s *jointSession) sensitiveKey() ([]byte, string, error) {
	if key, ok, err := jointEnvKey(); ok {
		return key, jointKeyFromEnv, err
	}
	if key, err := jointReadKeyFile(s.dir); err == nil {
		return key, "", nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(s.path(jointKeyFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			key, err := jointReadKeyFile(s.dir)
			return key, "", err
		}
		return nil, "", err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, "", err
	}
	if err := f.Close(); err != nil {
		return nil, "", err
	}
	return key, "", nil
}

// jointReadKey returns the key an apply checks a plan file's hashes with.
// When [jointKeyEnv] is set, that is its key, whatever planned. Otherwise
// it is the key file of the planning session at dir; a plan made with a
// key from [jointKeyEnv] (keyFrom) has no key file and says so. check, when
// the plan file keeps one, must match the key, or the key differs from the
// one that planned.
func jointReadKey(dir, keyFrom, check string) ([]byte, error) {
	key, ok, err := jointEnvKey()
	switch {
	case ok && err != nil:
		return nil, err
	case !ok && keyFrom == jointKeyFromEnv:
		return nil, fmt.Errorf("the plan was made with the key in %s, which is not set here", jointKeyEnv)
	case !ok:
		if key, err = jointReadKeyFile(dir); err != nil {
			return nil, err
		}
	}
	if check != "" && !hmac.Equal([]byte(jointKeyCheck(key)), []byte(check)) {
		if ok {
			return nil, fmt.Errorf("the key in %s is not the key the plan was made with", jointKeyEnv)
		}
		return nil, fmt.Errorf("the key of session %s is not the key the plan was made with", dir)
	}
	return key, nil
}

// jointReadKeyFile reads the key file of the session at dir.
func jointReadKeyFile(dir string) ([]byte, error) {
	if dir == "" {
		return nil, fmt.Errorf("the plan file names no session")
	}
	key, err := os.ReadFile(filepath.Join(dir, jointKeyFile))
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s is not a key", filepath.Join(dir, jointKeyFile))
	}
	return key, nil
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
