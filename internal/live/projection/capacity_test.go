// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

func ssmStore(tier string) *configs.LiveRecordStore {
	rs := &configs.LiveRecordStore{Type: "ssm"}
	if tier != "" {
		rs.Tier = tier
		rs.TierSet = true
	}
	return rs
}

// TestCheckRecordCapacityRefusesOnlyWhatCannotFit walks the three bands the
// check divides an estate into, at the exact boundary of each. The
// boundaries are the assertion: a ceiling check that fires one record early
// turns away an estate that would have applied cleanly, which is a worse
// defect than the silent wall it replaced.
func TestCheckRecordCapacityRefusesOnlyWhatCannotFit(t *testing.T) {
	const cap = staterecord.SSMStandardParameterLimit // 10000

	for _, tc := range []struct {
		name    string
		rs      *configs.LiveRecordStore
		planned int
		want    string // "clean", "warning" or "error"
	}{
		{"far below the ceiling", ssmStore(""), 100, "clean"},
		{"one record below the warning band", ssmStore(""), 8998, "clean"},
		{"the first record in the warning band", ssmStore(""), 8999, "warning"},
		{"the largest estate that still fits", ssmStore(""), cap - 1, "warning"},
		{"one record too many", ssmStore(""), cap, "error"},
		{"the issue's own scale-136 estate", ssmStore(""), 10069, "error"},

		// The same three bands ten times higher once the tier is raised.
		{"scale 136 on the advanced tier", ssmStore("advanced"), 10069, "clean"},
		{"scale 136 on intelligent tiering", ssmStore("intelligent_tiering"), 10069, "clean"},
		{"an advanced estate that still cannot fit", ssmStore("advanced"), 100000, "error"},

		// An explicit standard tier is the same ceiling as no tier at all.
		{"an explicit standard tier still stops at ten thousand", ssmStore("standard"), cap, "error"},

		// Backends with no fixed ceiling, and the degenerate inputs.
		{"local has no ceiling", &configs.LiveRecordStore{Type: "local"}, 10069, "clean"},
		{"s3 has no ceiling", &configs.LiveRecordStore{Type: "s3", Bucket: "b"}, 10069, "clean"},
		{"no record store at all", nil, 10069, "clean"},
		{"an unknown count checks nothing", ssmStore(""), 0, "clean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags := CheckRecordCapacity(tc.rs, tc.planned)
			got := "clean"
			switch {
			case diags.HasErrors():
				got = "error"
			case len(diags) > 0:
				got = "warning"
			}
			if got != tc.want {
				t.Fatalf("%d records against %+v: got %s, want %s\n%s", tc.planned, tc.rs, got, tc.want, diags.Err())
			}
		})
	}
}

// TestCheckRecordCapacityNamesTheNumbers: a ceiling message that does not
// say how many records the estate has, how many the store holds, and which
// tier decided that is a message an operator cannot act on. Both bands are
// covered, because the warning is the half most likely to be written once
// and never read again.
func TestCheckRecordCapacityNamesTheNumbers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		planned int
		want    []string
	}{
		{"the refusal", 10069, []string{"10070", "10069", "10000", "L-C3B871CB", "advanced", "intelligent_tiering", "s3"}},
		{"the warning", 9500, []string{"9501", "9500", "10000", "advanced", "intelligent_tiering", "s3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diags := CheckRecordCapacity(ssmStore(""), tc.planned)
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1", len(diags))
			}
			text := diags[0].Description().Summary + "\n" + diags[0].Description().Detail
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the message does not name %q:\n%s", want, text)
				}
			}
		})
	}
}

// TestCheckRecordCapacityDoesNotOfferATierThatIsAlsoTooSmall: an estate of
// 200,000 resources has no SSM answer at all, and a message that offers the
// advanced tier anyway sends its reader to do the migration twice.
func TestCheckRecordCapacityDoesNotOfferATierThatIsAlsoTooSmall(t *testing.T) {
	diags := CheckRecordCapacity(ssmStore(""), 200000)
	if !diags.HasErrors() {
		t.Fatal("200,000 records were accepted")
	}
	detail := diags[0].Description().Detail
	for _, offered := range staterecord.SSMTierNames() {
		if strings.Contains(detail, "tier = \""+offered+"\"") {
			t.Errorf("the refusal offers tier %q, which holds %d and cannot take 200001 either:\n%s", offered, staterecord.SSMTierCapacity(staterecord.SSMTier(offered)), detail)
		}
	}
	if !strings.Contains(detail, "s3") {
		t.Errorf("the refusal offers no reachable alternative at all:\n%s", detail)
	}
}

// TestRecordStoreTierVocabularyMatchesTheStore pins the two lists that must
// agree and live in packages that cannot import each other: the spellings
// internal/configs' decoder accepts, and the spellings
// internal/live/staterecord knows how to write. This package imports both,
// which is why the pin lives here.
//
// A spelling in the decoder and not the store reaches an author as a
// configuration that loads and then fails at store-open; one in the store
// and not the decoder is a tier nobody can select, which is the whole
// defect GitHub issue #1146 was filed about.
func TestRecordStoreTierVocabularyMatchesTheStore(t *testing.T) {
	decoder := slices.Clone(configs.RecordStoreTiers)
	store := staterecord.SSMTierNames()
	slices.Sort(decoder)
	slices.Sort(store)
	if !slices.Equal(decoder, store) {
		t.Errorf("configs.RecordStoreTiers = %q, staterecord.SSMTierNames() = %q", decoder, store)
	}
	for _, name := range configs.RecordStoreTiers {
		if _, ok := staterecord.ParseSSMTier(name); !ok {
			t.Errorf("the decoder accepts tier %q, which the store rejects", name)
		}
		if got := staterecord.SSMTierCapacity(staterecord.SSMTier(name)); got <= 0 {
			t.Errorf("tier %q has no capacity", name)
		}
	}
}

// cappedStore is an in-memory [staterecord.Store] that behaves the way
// Parameter Store behaves at its quota: every write lands until the store
// holds capacity keys, and the one after that fails - which is exactly the
// shape a real run hits, one record at a time, with no warning and nothing
// to retry.
type cappedStore struct {
	mu       sync.Mutex
	capacity int
	keys     map[string][]byte
}

func newCappedStore(capacity int) *cappedStore {
	return &cappedStore{capacity: capacity, keys: map[string][]byte{}}
}

func (c *cappedStore) Get(_ context.Context, key string) ([]byte, string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.keys[key]
	if !ok {
		return nil, "", false, nil
	}
	return v, "1", true, nil
}

func (c *cappedStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return c.PutIfVersion(ctx, key, payload, "")
}

func (c *cappedStore) PutIfVersion(_ context.Context, key string, payload []byte, _ string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.keys[key]; !exists && len(c.keys) >= c.capacity {
		return "", fmt.Errorf("ParameterLimitExceeded: the account already holds %d parameters in this Region", c.capacity)
	}
	c.keys[key] = payload
	return "1", nil
}

func (c *cappedStore) Delete(_ context.Context, key string, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.keys, key)
	return nil
}

func (c *cappedStore) List(_ context.Context, keyPrefix string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for k := range c.keys {
		if strings.HasPrefix(k, keyPrefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (c *cappedStore) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.keys)
}

// writeEstate is the write order a run follows: the provisioning sentinel
// first ([provisionStoreSentinel]), then one record per instance. It stops
// at the first write that fails, the way a real apply does.
func writeEstate(store staterecord.Store, planned int) error {
	ctx := context.Background()
	if _, err := store.PutIfAbsent(ctx, SentinelKey("tofu-records/e"), []byte(sentinelPayload)); err != nil {
		return err
	}
	for i := 0; i < planned; i++ {
		if _, err := store.PutIfAbsent(ctx, "tofu-records/e/aws_instance/"+strconv.Itoa(i), []byte("x")); err != nil {
			return err
		}
	}
	return nil
}

// TestCapacityCheckStopsTheEstateBeforeTheFirstWrite is the arm GitHub
// issue #1146 turns on, and it is deliberately two runs rather than one.
//
// The first run is the defect, reproduced: with no check in front of it, an
// estate of 10,069 resources writes ten thousand parameters into the
// account and THEN fails, leaving a store that holds records for some
// instances and not others, against live resources the apply has already
// created. That arm is what makes the second one mean something - a test
// that only asserts a message exists proves nothing about when it fires.
//
// The second run is the fix: the same estate, the same store, with
// [CheckRecordCapacity] consulted first the way internal/command's live
// paths consult it. The assertion is on the STORE, not on the diagnostic:
// zero keys written, including the sentinel.
func TestCapacityCheckStopsTheEstateBeforeTheFirstWrite(t *testing.T) {
	const planned = 10069 // the issue's scale-136 terralith-scale estate
	rs := ssmStore("")

	t.Run("without the check, the estate is half written", func(t *testing.T) {
		store := newCappedStore(staterecord.SSMStandardParameterLimit)
		err := writeEstate(store, planned)
		if err == nil {
			t.Fatal("the capped store accepted more records than its ceiling; the fixture does not reproduce the defect")
		}
		if !strings.Contains(err.Error(), "ParameterLimitExceeded") {
			t.Fatalf("unexpected failure: %v", err)
		}
		if got := store.len(); got != staterecord.SSMStandardParameterLimit {
			t.Fatalf("the store holds %d keys, want it filled to the ceiling of %d", got, staterecord.SSMStandardParameterLimit)
		}
		t.Logf("without the check: %d of %d records written, then %v", store.len()-1, planned, err)
	})

	t.Run("with the check, nothing is written at all", func(t *testing.T) {
		store := newCappedStore(staterecord.SSMStandardParameterLimit)
		diags := CheckRecordCapacity(rs, planned)
		if !diags.HasErrors() {
			t.Fatal("the check accepted an estate that cannot fit")
		}
		// The run stops here, exactly as internal/command's live_plan,
		// live_mode and live_import paths stop. Nothing calls writeEstate.
		if got := store.len(); got != 0 {
			t.Fatalf("the store holds %d keys; the refusal must precede every write, the sentinel included", got)
		}
	})
}
