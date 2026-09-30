// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// jitterStore delays every read by a random 0..max, so the bulk load and the
// per-key reads of one build interleave differently every iteration.
type jitterStore struct {
	*staterecord.LocalStore
	max time.Duration
	// drop, when set, is left out of every GetAll: a short snapshot, the
	// shape the issue-era bulk read produced from a 404. The control arm.
	drop string
}

func (s *jitterStore) nap() { time.Sleep(time.Duration(rand.Int63n(int64(s.max) + 1))) }

func (s *jitterStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	s.nap()
	return s.LocalStore.Get(ctx, key)
}

func (s *jitterStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.nap()
	return s.LocalStore.List(ctx, prefix)
}

func (s *jitterStore) GetAll(ctx context.Context, prefix string) (map[string]staterecord.Record, error) {
	s.nap()
	all, err := s.LocalStore.GetAll(ctx, prefix)
	if s.drop != "" {
		delete(all, s.drop)
	}
	return all, err
}

// TestStressTwoRecordBackedInstancesBothMaterialize is GitHub issue #1355's
// estate at the projection layer, repeated: for_each over "a.b" and "plain",
// both records in the store, read through the production wrapping
// ([staterecord.NewRunCache] under [NewRecordEnvelopeStore]) with every read
// delayed at random. Prior state for a destroy is exactly what this builds,
// so both instances must materialize every time.
//
// A reproduction attempt, not a regression gate: it runs only when
// CHOUDOUFU_STRESS_1355 names an iteration count.
func TestStressTwoRecordBackedInstancesBothMaterialize(t *testing.T) {
	iters, _ := strconv.Atoi(os.Getenv("CHOUDOUFU_STRESS_1355"))
	if iters <= 0 {
		t.Skip("set CHOUDOUFU_STRESS_1355=<iterations> to run the #1355 stress loop")
	}
	dir := t.TempDir()
	const src = `
resource "null_resource" "trigger" {
  for_each = toset(["a.b", "plain"])
  triggers = {
    input = each.key
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	dotted := mustAddr(t, `null_resource.trigger["a.b"]`)
	plain := mustAddr(t, `null_resource.trigger["plain"]`)
	const prefix = "tofu-records/tagged-estate"

	bad := 0
	for it := 0; it < iters; it++ {
		staterecord.ResetRunCacheForTest(t)
		backing, err := staterecord.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		seedRecordObject(t, backing, prefix, dotted)
		seedRecordObject(t, backing, prefix, plain)
		js := &jitterStore{LocalStore: backing, max: time.Duration(rand.Intn(3000)) * time.Microsecond}
		if os.Getenv("CHOUDOUFU_STRESS_1355_CONTROL") == "1" {
			js.drop = RecordKey(prefix, plain)
		}
		store := NewRecordEnvelopeStore(
			staterecord.NewRunCache(js, prefix),
			prefix,
		)
		provs := SingleProvider(nullProvider, nullResourceProvider())
		// Resolution order varies too: identity resolution's output order is
		// not something prior state may depend on.
		resolutions := []identity.Resolution{
			{Addr: dotted, Class: identity.ClassRecordBacked},
			{Addr: plain, Class: identity.ClassRecordBacked},
		}
		if rand.Intn(2) == 0 {
			resolutions[0], resolutions[1] = resolutions[1], resolutions[0]
		}
		res, diags := BuildWith(context.Background(), cfg, resolutions, provs, Options{RecordStore: store, ReadParallelism: 1 + rand.Intn(16)})
		if diags.HasErrors() || len(res.Materialized) != 2 {
			bad++
			t.Errorf("iteration %d: materialized %v, diagnostics:\n%s", it, res.Materialized, renderDiags(diags))
		}
	}
	t.Logf("%d iterations, %d with fewer than two instances in prior state or an error", iters, bad)
}
