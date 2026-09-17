// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// livecertlock.go: issue #1150. Two live-cert runs of the same estate do not
// collide in the ACCOUNT - every resource they create is named from a
// per-run PREFIX, and verify_empty and the sweep are scoped to it, which was
// designed deliberately (live/live-cert/terralith-scale.sh). They collide in
// the EVIDENCE, which was not:
//
//   - both write live/gauntlet/logs/live-cert-<estate>.log, and RunLiveCert
//     opens it with os.Create. The second run TRUNCATES the first's log while
//     the first's file descriptor keeps writing at its old offset, so the
//     file ends up part one run, part NUL padding, part the other. Measured
//     before this file existed: two overlapping runs produced a 295,759-byte
//     log of which 280,961 bytes were NUL, carrying lines from both run pids
//     and none of the first run's first four thousand lines.
//   - both write live/gauntlet.json at the end, last writer wins.
//
// It is not theoretical. On 2026-09-15 a pre-restart screen session was
// running the identical scale-50 command 166 seconds ahead of a new one.
// Nothing surfaced the overlap; their combined IAM roles blew the account's
// 1,000-role quota; and the run reported a cold_deploy FAILURE. A real-AWS
// verdict, hours of paid runtime to produce, wrong, and wrong in the
// direction of blaming the product for what another copy of itself had done.
//
// So: one live-cert per estate at a time, enforced by a lock file the
// refusal names in full. The lock is advisory in the operating-system sense
// and deliberate in every other: it is a plain key=value file a human can
// cat, sitting beside the log it protects, and a run that refuses prints
// what it found rather than exiting quietly.
//
// What this does NOT do, stated so nobody reads more into it:
//
//   - it does not interlock with a direct `bash live/live-cert/<estate>.sh`
//     invocation, only with runs started through `gauntlet live-cert`. A
//     direct invocation writes no log here and no artifact, so it corrupts
//     no evidence - but it can still consume the same account quota, which
//     is the part #1150's third item (a run asserting its own quota
//     headroom) would cover and this does not.
//   - it never breaks a lock on its own. A lock whose pid is gone is
//     reported as looking stale, with the path to remove, and the run still
//     refuses. Taking over automatically would mean trusting a pid, and pids
//     are reused; refusing costs one `rm` that a human decided to type.
//   - it has no environment-variable override, on purpose. A gate an agent
//     can satisfy by exporting a variable constrains only the maintainer
//     (CLAUDE.md, #1102).
//
// #1150's second item - put the run id in the log path and make the artifact
// write conditional, so concurrent runs COULD coexist - is deliberately not
// done. It is the fix for wanting concurrency; this is the fix for not
// wanting it. Nothing today wants two certifications of one estate at once,
// and a per-run log path would leave the artifact race untouched, which is
// the half that decides what gets published.

// LiveCertLockPath is the lock for one estate's live-cert runs: a sibling of
// that estate's live-cert log, in the same gitignored directory, named so
// the two obviously go together.
func LiveCertLockPath(root, estate string) string {
	return filepath.Join(root, LogDir, "live-cert-"+estate+".lock")
}

// LiveCertLock is a held lock. Release it exactly once, with defer.
type LiveCertLock struct {
	path  string
	runID string
}

// Path is the lock file this lock holds, for a caller that wants to name it.
func (l *LiveCertLock) Path() string { return l.path }

// RunID is the id this invocation wrote into the lock. It is the GO side's
// id for the invocation, generated before the script starts; it is NOT the
// script's own RUN_ID (live/live-cert/terralith-scale.sh generates that
// itself, and this deliberately does not pass one in, because forcing RUN_ID
// would defeat the marker-based LIVECERT_RESUME path that reads it back off
// a held work dir). The two are separate ids for separate things: this one
// says which invocation holds the lock, the script's says which resources in
// the account belong to the run.
func (l *LiveCertLock) RunID() string { return l.runID }

// liveCertLockInfo is a lock file's contents, parsed.
type liveCertLockInfo struct {
	RunID   string
	PID     int
	Started string // as written, RFC3339 UTC
	Estate  string
	Target  string
	Region  string
	Host    string
	// Extra keeps any key this version does not know about, so a refusal
	// written by a newer gauntlet still prints everything it said.
	Extra map[string]string
}

// LiveCertBusyError is the refusal: another live-cert for this estate holds
// the lock. It is its own type so a caller can tell "someone else is running
// this estate" from "the lock file could not be written at all".
type LiveCertBusyError struct {
	Estate string
	Path   string
	Info   liveCertLockInfo
	// Alive is whether a process with the recorded pid exists right now.
	Alive bool
	// Age is how long ago the lock says the other run started; zero when
	// the lock's started= could not be parsed.
	Age time.Duration
}

func (e *LiveCertBusyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "live-cert %s: refusing to start - another live-cert for this estate is already running\n", e.Estate)
	fmt.Fprintf(&b, "  run_id   %s\n", orUnknown(e.Info.RunID))
	if e.Alive {
		fmt.Fprintf(&b, "  pid      %d (a process with this pid exists right now)\n", e.Info.PID)
	} else {
		fmt.Fprintf(&b, "  pid      %d (NO SUCH PROCESS - this lock looks stale)\n", e.Info.PID)
	}
	if e.Age > 0 {
		fmt.Fprintf(&b, "  started  %s (%s ago)\n", orUnknown(e.Info.Started), e.Age.Round(time.Second))
	} else {
		fmt.Fprintf(&b, "  started  %s\n", orUnknown(e.Info.Started))
	}
	fmt.Fprintf(&b, "  target   %s  region %s  host %s\n", orUnknown(e.Info.Target), orUnknown(e.Info.Region), orUnknown(e.Info.Host))
	for _, k := range sortedKeys(e.Info.Extra) {
		fmt.Fprintf(&b, "  %-8s %s\n", k, e.Info.Extra[k])
	}
	fmt.Fprintf(&b, "  lock     %s\n", e.Path)
	b.WriteString("Two runs of one estate do not collide in the account - every resource is named from a per-run prefix, and verify_empty and the sweep are scoped to it - but they DO collide in the evidence: both write the same live-cert log, and the second truncates the first while the first keeps writing at its old offset, and both write live/gauntlet.json at the end (#1150).\n")
	if e.Alive {
		b.WriteString("Wait for the run above to finish.")
	} else {
		b.WriteString("Nothing is running under that pid. If you are certain no live-cert is writing that log, delete the lock file named above and run again - this refuses rather than breaking the lock itself, because pids are reused and an automatic takeover would be a guess.")
	}
	return b.String()
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not recorded)"
	}
	return s
}

// NewLiveCertRunID mints this invocation's id. Same shape as the estate
// script's own RUN_ID default (`lc<epoch>-<pid>`) so a human reading a lock
// file and a human reading a run's log are reading the same kind of thing.
func NewLiveCertRunID() string {
	return fmt.Sprintf("lc%d-%d", time.Now().Unix(), os.Getpid())
}

// AcquireLiveCertLock takes the lock for estate under root, or returns a
// *LiveCertBusyError naming the run that already holds it.
//
// O_EXCL is the whole mechanism: the create either wins or reports that the
// file is already there, with no window between the check and the create for
// a second run to slip through. That matters here because the two runs this
// guards against start SECONDS apart (166, in the incident), not
// microseconds - but a check-then-create would still be wrong, and a correct
// primitive costs nothing over an incorrect one.
func AcquireLiveCertLock(root, estate, target, region string) (*LiveCertLock, error) {
	if err := os.MkdirAll(filepath.Join(root, LogDir), 0o755); err != nil {
		return nil, fmt.Errorf("live-cert %s: cannot create the log directory to lock in: %w", estate, err)
	}
	path := LiveCertLockPath(root, estate)
	runID := NewLiveCertRunID()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a gitignored path under the checkout, built from the estate name
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("live-cert %s: cannot take the run lock %s: %w", estate, path, err)
		}
		info := readLiveCertLock(path)
		busy := &LiveCertBusyError{Estate: estate, Path: path, Info: info, Alive: pidAlive(info.PID)}
		if t, perr := time.Parse(time.RFC3339, info.Started); perr == nil {
			busy.Age = time.Since(t)
		}
		return nil, busy
	}
	host, _ := os.Hostname()
	body := strings.Join([]string{
		"run_id=" + runID,
		"pid=" + strconv.Itoa(os.Getpid()),
		"started=" + time.Now().UTC().Format(time.RFC3339),
		"estate=" + estate,
		"target=" + target,
		"region=" + region,
		"host=" + host,
	}, "\n") + "\n"
	if _, werr := f.WriteString(body); werr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("live-cert %s: cannot write the run lock %s: %w", estate, path, werr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("live-cert %s: cannot write the run lock %s: %w", estate, path, cerr)
	}
	return &LiveCertLock{path: path, runID: runID}, nil
}

// Release removes the lock, but only when it is still ours: if the file's
// run_id is not the one we wrote, some other run has taken over the path and
// deleting it would unlock that run instead of this one. Safe to call on a
// nil lock, so a caller can `defer l.Release()` before checking err.
func (l *LiveCertLock) Release() error {
	if l == nil {
		return nil
	}
	info := readLiveCertLock(l.path)
	if info.RunID != "" && info.RunID != l.runID {
		return fmt.Errorf("live-cert: the run lock %s now says run_id=%s, not this run's %s - leaving it alone rather than unlocking someone else's run", l.path, info.RunID, l.runID)
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("live-cert: cannot remove the run lock %s: %w", l.path, err)
	}
	return nil
}

// readLiveCertLock parses a lock file. A lock that cannot be read, or that
// holds something this version does not understand, still produces a usable
// refusal: an unreadable lock is MORE reason to refuse, not less, so every
// failure here is a zero value rather than an error.
func readLiveCertLock(path string) liveCertLockInfo {
	info := liveCertLockInfo{Extra: map[string]string{}}
	b, err := os.ReadFile(path) //nolint:gosec // a gitignored path this package built itself
	if err != nil {
		return info
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "run_id":
			info.RunID = v
		case "pid":
			info.PID, _ = strconv.Atoi(v)
		case "started":
			info.Started = v
		case "estate":
			info.Estate = v
		case "target":
			info.Target = v
		case "region":
			info.Region = v
		case "host":
			info.Host = v
		default:
			info.Extra[k] = v
		}
	}
	return info
}

// pidAlive reports whether a process with this pid exists. Signal 0 is the
// POSIX existence check: it delivers nothing and only reports whether the
// process is there. EPERM means it is there and owned by someone else, which
// is still there.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM)
}
