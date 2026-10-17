// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// The four outcomes a -progress-json line reports (GitHub issue #2063).
const (
	// LiveProgressMoved is a live-mv marker write that landed, or a follower
	// that moved with it.
	LiveProgressMoved = "moved"
	// LiveProgressStamped is a live-import -approve marker (or record) write
	// that landed.
	LiveProgressStamped = "stamped"
	// LiveProgressRefused is a write that was refused or failed.
	LiveProgressRefused = "refused"
	// LiveProgressSkipped is a resource with nothing to write: never
	// eligible, or already carrying the marker the run would have written.
	LiveProgressSkipped = "skipped"
)

// LiveProgressLine is one -progress-json line: one resource, written to
// stderr as its marker write returns. live-mv and live-import -approve print
// it; the document either command prints on stdout is unchanged by it.
type LiveProgressLine struct {
	// At is when the line was written, RFC 3339 in UTC with nanoseconds.
	At string `json:"at"`

	// EstateFrom is the estate the resource left: the -from-estate of a
	// cross-estate move, or the one estate a rename stays within. Empty for
	// live-import, whose source is a state file rather than an estate.
	EstateFrom string `json:"estate_from"`

	// EstateTo is the estate whose marker the write carries.
	EstateTo string `json:"estate_to"`

	// Address is the address the marker names after the write: live-mv's
	// destination address, or the address live-import stamps.
	Address string `json:"address"`

	// LiveID is the live resource's identity, when this run knows it. Empty
	// for a follower (it has no write and was never read) and for a refusal
	// that came before the resource was found.
	LiveID string `json:"live_id"`

	// Outcome is one of moved, stamped, refused or skipped.
	Outcome string `json:"outcome"`

	// Reason says why, for refused and skipped.
	Reason string `json:"reason,omitempty"`

	// Follows names the parent's address for an untaggable child that moves
	// with its parent's write and has none of its own, as live-mv -json's
	// followers do.
	Follows string `json:"follows,omitempty"`
}

// LiveWriteProgress writes -progress-json lines. It is safe for concurrent use:
// live-import -approve stamps up to -parallelism resources at once, and each
// line is written whole, in the order the writes returned.
//
// A nil *LiveWriteProgress writes nothing, so a command can hold one
// unconditionally and only build it when the flag was given.
type LiveWriteProgress struct {
	view *View
	now  func() time.Time
	mu   sync.Mutex
}

// NewLiveWriteProgress returns a LiveWriteProgress writing to view's stderr.
func NewLiveWriteProgress(view *View) *LiveWriteProgress {
	return &LiveWriteProgress{view: view, now: time.Now}
}

// Line stamps l with the current time and writes it as one JSON line.
func (p *LiveWriteProgress) Line(l LiveProgressLine) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	l.At = p.now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(l)
	if err != nil {
		// Every field is a plain string.
		panic(fmt.Sprintf("-progress-json: line could not be marshalled: %s", err))
	}
	_, _ = p.view.streams.Eprintln(string(b))
}
