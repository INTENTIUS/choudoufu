// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
)

// GitHub issue #1430's second finding: [RunCache.ensureLoaded] fell back to
// per-key reads when the bulk read failed and said nothing, so a run that
// took that path read the same in its log as one served from a snapshot.
// Since #1355 that is the path a store contradicting itself takes (a key the
// listing names and two GETs do not find fails [S3Store.GetAll]), which makes
// it the first thing anyone chasing a missed destroy needs to see.

// captureStdLog redirects the standard logger for the duration of fn and
// returns what was written, the shape internal/live/projection's
// captureWriteBackLog uses.
func captureStdLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	})
	fn()
	return buf.String()
}

// TestRunCacheLogsTheFallbackToPerKeyReads: a failed bulk read is logged at
// WARN with the namespace and the store's own error, once per failed load,
// and the per-key read still happens.
func TestRunCacheLogsTheFallbackToPerKeyReads(t *testing.T) {
	resetRunCacheState(t)
	ctx := context.Background()
	bulkErr := errors.New(`the listing named "tofu-records/e/terraform_data/k" and no record was there, twice over`)
	inner := &bulkFailsPerKeyAnswers{bulkErr: bulkErr}
	cache := NewRunCache(inner, testPrefix)

	out := captureStdLog(t, func() {
		for _, key := range []string{testPrefix + "terraform_data/k", testPrefix + "terraform_data/other"} {
			if _, _, exists, err := cache.Get(ctx, key); err != nil || exists {
				t.Fatalf("Get(%q) = (exists %v, err %v), want the per-key path's own answer (false, nil)", key, exists, err)
			}
		}
	})

	if inner.perKeyGets != 2 {
		t.Errorf("the fallback made %d per-key reads, want 2: the log line must not replace the read", inner.perKeyGets)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines for 2 failed bulk reads, want 2:\n%s", len(lines), out)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "[WARN] staterecord: the bulk read of ") {
			t.Errorf("the fallback's line is not a [WARN] from staterecord: %q", line)
		}
		if !strings.Contains(line, NamespacePrefix(testPrefix)) {
			t.Errorf("the fallback's line does not name the namespace %q: %q", NamespacePrefix(testPrefix), line)
		}
		if !strings.Contains(line, bulkErr.Error()) {
			t.Errorf("the fallback's line does not carry the store's own error, which is what names the key: %q", line)
		}
	}
}

// TestRunCacheLogsNothingWhenTheBulkReadSucceeds: the line is about the
// fallback and nothing else. A run served from its snapshot stays silent, so
// the line's presence in a log means what it says.
func TestRunCacheLogsNothingWhenTheBulkReadSucceeds(t *testing.T) {
	resetRunCacheState(t)
	ctx := context.Background()
	inner := &bulkFailingStore{snapshot: map[string]Record{
		testPrefix + "terraform_data/k": {Payload: []byte(`{}`), Version: "1"},
	}}
	cache := NewRunCache(inner, testPrefix)

	out := captureStdLog(t, func() {
		if _, _, exists, err := cache.Get(ctx, testPrefix+"terraform_data/k"); err != nil || !exists {
			t.Fatalf("Get = (exists %v, err %v), want (true, nil) from the snapshot", exists, err)
		}
	})
	if out != "" {
		t.Errorf("a bulk read that succeeded logged something:\n%s", out)
	}
}

// bulkFailsPerKeyAnswers fails every bulk read and answers every per-key read
// with "no record", which is the store #1355's route needs: the listing and
// the reads disagree, and the read that is finally believed says absent.
// [bulkFailingStore] cannot stand in, because its per-key Get raises the same
// error as its GetAll.
type bulkFailsPerKeyAnswers struct {
	Store
	bulkErr    error
	perKeyGets int
}

func (s *bulkFailsPerKeyAnswers) GetAll(context.Context, string) (map[string]Record, error) {
	return nil, s.bulkErr
}

func (s *bulkFailsPerKeyAnswers) Get(context.Context, string) ([]byte, string, bool, error) {
	s.perKeyGets++
	return nil, "", false, nil
}
