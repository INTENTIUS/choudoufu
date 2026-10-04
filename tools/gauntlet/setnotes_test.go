// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func setNotesFixture(t *testing.T) (*Artifact, []byte) {
	t.Helper()
	a := &Artifact{Schema: 1, Estates: []EstateResult{
		{Name: "a", Notes: "old a"},
		{Name: "b", Notes: "old b", Clear: true},
	}}
	b, err := a.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

// TestSetNotesChangesOnlyThatRow: the written bytes differ from the
// committed ones only in the one row's notes.
func TestSetNotesChangesOnlyThatRow(t *testing.T) {
	a, committed := setNotesFixture(t)
	out, err := setNotes(a, committed, "b", "new b")
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(committed, []byte(`"old b"`), []byte(`"new b"`), 1)
	if !bytes.Equal(out, want) {
		t.Errorf("set-notes wrote:\n%s\nwant:\n%s", out, want)
	}
	if r, _ := a.Result("b"); !r.Clear {
		t.Error("set-notes moved b's clear flag")
	}
}

// TestSetNotesRefuses is the red side: an unknown row, an unchanged note
// and a non-canonical committed file are all refused.
func TestSetNotesRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		estate, notes string
		mangle        func([]byte) []byte
		want          string
	}{
		"unknown row":   {"zz", "x", nil, "no row"},
		"same text":     {"a", "old a", nil, "already"},
		"not canonical": {"a", "x", func(b []byte) []byte { return append(b, '\n') }, "canonical"},
	} {
		t.Run(name, func(t *testing.T) {
			a, committed := setNotesFixture(t)
			if tc.mangle != nil {
				committed = tc.mangle(committed)
			}
			_, err := setNotes(a, committed, tc.estate, tc.notes)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
