// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend/remote-state/inmem"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/remote"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/states/statemgr"
)

func readPublished(t *testing.T, c *inmem.RemoteClient) *statefile.File {
	t.Helper()
	f, err := statefile.Read(bytes.NewReader(c.Data), encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWriteOutputsState(t *testing.T) {
	ctx := context.Background()
	c := &inmem.RemoteClient{Name: "t-" + t.Name()}
	vals := map[string]cty.Value{"vpc_id": cty.StringVal("vpc-1"), "n": cty.NumberIntVal(3)}

	// Nothing to say and nothing there: no document.
	if err := writeOutputsState(ctx, remote.NewState(c, encryption.StateEncryptionDisabled()), nil); err != nil || c.Data != nil {
		t.Fatalf("err=%v data=%q", err, c.Data)
	}

	if err := writeOutputsState(ctx, remote.NewState(c, encryption.StateEncryptionDisabled()), vals); err != nil {
		t.Fatal(err)
	}
	f1 := readPublished(t, c)
	if f1.Lineage == "" || f1.Serial != 1 {
		t.Fatalf("lineage=%q serial=%d", f1.Lineage, f1.Serial)
	}
	if f1.State.HasManagedResourceInstanceObjects() || len(f1.State.RootModule().Resources) != 0 {
		t.Fatal("resources published")
	}
	if got := f1.State.RootModule().OutputValues["vpc_id"]; got == nil || got.Value.AsString() != "vpc-1" || got.Sensitive {
		t.Fatalf("vpc_id = %#v", got)
	}

	// Same values: untouched. Changed values: same lineage, serial + 1.
	if err := writeOutputsState(ctx, remote.NewState(c, encryption.StateEncryptionDisabled()), vals); err != nil {
		t.Fatal(err)
	}
	if f := readPublished(t, c); f.Serial != 1 {
		t.Fatalf("serial moved for identical outputs: %d", f.Serial)
	}
	vals["vpc_id"] = cty.StringVal("vpc-2")
	if err := writeOutputsState(ctx, remote.NewState(c, encryption.StateEncryptionDisabled()), vals); err != nil {
		t.Fatal(err)
	}
	f2 := readPublished(t, c)
	if f2.Lineage != f1.Lineage || f2.Serial != 2 {
		t.Fatalf("lineage %q->%q serial %d", f1.Lineage, f2.Lineage, f2.Serial)
	}

	// A removed output goes from the document; a destroyed estate leaves an empty one.
	if err := writeOutputsState(ctx, remote.NewState(c, encryption.StateEncryptionDisabled()), nil); err != nil {
		t.Fatal(err)
	}
	if f := readPublished(t, c); len(f.State.RootModule().OutputValues) != 0 || f.Serial != 3 {
		t.Fatalf("outputs=%d serial=%d", len(f.State.RootModule().OutputValues), f.Serial)
	}

	// The lock is released after every write.
	if _, err := inmemLockProbe(ctx, c); err != nil {
		t.Fatalf("lock left behind: %v", err)
	}
}

func inmemLockProbe(ctx context.Context, c *inmem.RemoteClient) (string, error) {
	id, err := c.Lock(ctx, statemgr.NewLockInfo())
	if err == nil {
		err = c.Unlock(ctx, id)
	}
	return id, err
}

func TestWriteOutputsStateRespectsLockAndResources(t *testing.T) {
	ctx := context.Background()

	// Someone else holds the lock: no write, and their lock stays theirs.
	c := &inmem.RemoteClient{Name: "t-" + t.Name()}
	id, err := c.Lock(ctx, statemgr.NewLockInfo())
	if err != nil {
		t.Fatal(err)
	}
	err = writeOutputsState(ctx, remote.NewState(c, encryption.StateEncryptionDisabled()), map[string]cty.Value{"a": cty.True})
	if err == nil || c.Data != nil {
		t.Fatalf("err=%v data=%q", err, c.Data)
	}
	if err := c.Unlock(ctx, id); err != nil {
		t.Fatalf("the other holder's lock was disturbed: %v", err)
	}

	// A stock state with resources is never overwritten.
	c2 := &inmem.RemoteClient{Name: "t-res-" + t.Name()}
	st := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "null_resource", Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"id":"1"}`)},
			addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("null"), Module: addrs.RootModule},
			addrs.NoKey,
		)
	})
	var buf bytes.Buffer
	if err := statefile.Write(statefile.New(st, "lin", 7), &buf, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatal(err)
	}
	c2.Data = buf.Bytes()
	before := string(c2.Data)
	err = writeOutputsState(ctx, remote.NewState(c2, encryption.StateEncryptionDisabled()), map[string]cty.Value{"a": cty.True})
	if err == nil || !strings.Contains(err.Error(), "resources") || string(c2.Data) != before {
		t.Fatalf("err=%v changed=%v", err, string(c2.Data) != before)
	}
	if _, err := inmemLockProbe(ctx, c2); err != nil {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestPublishOutputsStateNoBackend(t *testing.T) {
	if d := publishOutputsState(context.Background(), nil, nil); len(d) != 0 {
		t.Fatal(d)
	}
}
