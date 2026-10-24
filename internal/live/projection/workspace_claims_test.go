// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

func TestNoteWorkspaceClaim(t *testing.T) {
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Unix(1700000000, 0) }

	// A plan reads and never writes.
	if d := NoteWorkspaceClaim(ctx, store, "e", "a", false, now); len(d) != 0 {
		t.Fatalf("plan on empty registry: %v", d)
	}
	if _, _, exists, _ := store.Get(ctx, WorkspaceClaimsKey("e")); exists {
		t.Fatal("a plan wrote the registry")
	}

	// First apply registers silently, and so does a repeat.
	for i := 0; i < 2; i++ {
		if d := NoteWorkspaceClaim(ctx, store, "e", "a", true, now); len(d) != 0 {
			t.Fatalf("apply by the only workspace warned: %v", d)
		}
	}

	// A second workspace is told, naming both, on plan and on apply.
	for _, record := range []bool{false, true} {
		d := NoteWorkspaceClaim(ctx, store, "e", "b", record, now)
		if len(d) != 1 {
			t.Fatalf("record=%v: want one warning, got %v", record, d)
		}
		detail := d[0].Description().Detail
		for _, want := range []string{`"b"`, `"a"`, `estate "e"`} {
			if !strings.Contains(detail, want) {
				t.Errorf("warning lacks %s: %s", want, detail)
			}
		}
	}

	// Having recorded b, a is now told about b.
	if d := NoteWorkspaceClaim(ctx, store, "e", "a", false, now); len(d) != 1 || !strings.Contains(d[0].Description().Detail, `"b"`) {
		t.Errorf("a was not told about b: %v", d)
	}

	// A different estate is untouched.
	if d := NoteWorkspaceClaim(ctx, store, "other", "b", true, now); len(d) != 0 {
		t.Errorf("another estate warned: %v", d)
	}
}
