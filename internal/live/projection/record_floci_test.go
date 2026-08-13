// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestRecordBackedLifecycleAgainstSSM is live/e2e/record-store/run.sh's
// claim (hydrate, clean re-plan-equivalent, write-back, delete) proved
// again with an [staterecord.SSMStore] talking to a real SSM Parameter
// Store served by floci, instead of [staterecord.LocalStore] on disk. The
// LOCAL variant is the behavioral e2e that exercises a real `choudoufu`
// binary end to end (see live/e2e/record-store/); this is the narrower,
// package-level proof that the same hydration/write-back machinery in this
// package works unchanged against the other backend GitHub issue #73's
// store abstraction supports - the same relationship
// internal/live/staterecord/ssm_live_test.go's TestSSMStoreAgainstFloci has
// to its own fake-server suite.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/projection/ -run TestRecordBackedLifecycleAgainstSSM -v
func TestRecordBackedLifecycleAgainstSSM(t *testing.T) {
	flocitest.Gate(t, "projection/record-backed-ssm")
	flocitest.RequireBinary(t, "docker")
	requireDockerDaemonForSSM(t)

	port := flocitest.StartFloci(t, "projection-record-ssm")
	endpoint := flocitest.Endpoint(port)

	client := ssm.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *ssm.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	prefix := "/choudoufu-record-test/" + randomSegment(t)
	store, err := staterecord.NewSSMStore(staterecord.SSMConfig{Client: client, KeyPrefix: prefix})
	if err != nil {
		t.Fatalf("NewSSMStore: %v", err)
	}

	ctx := context.Background()
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)
	provs := SingleProvider(nullProvider, nullResourceProvider())

	// 1. Nothing in the store yet: the resolution is record-backed but
	// absent, so the plan should propose creating it - the same claim
	// TestBuildRecordBackedAbsent makes against the local store.
	resolutions := []identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}}
	res, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: store, RecordKeyPrefix: prefix})
	assertNoErrors(t, diags)
	assertOmitted(t, res, map[string]Reason{`null_resource.trigger`: ReasonAbsent})

	// 2. Write-back, as if an apply had just created it: persists the
	// first record via a real ssm:PutParameter with Overwrite: false.
	schema := nullResourceSchema()
	newVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("ssm-created-id"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	finalState := states.NewState()
	obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: newVal}
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, nullProvider, addrs.NoKey)

	wbDiags := WriteBack(ctx, WriteBackRequest{
		Store:         store,
		KeyPrefix:     prefix,
		PriorVersions: nil, // nothing existed yet: create semantics (expectedVersion "")
		FinalState:    finalState,
		Schemas:       ssmTestSchemas(schema),
	})
	assertNoErrors(t, wbDiags)

	// 3. Re-hydrate: the record just written back should now come back on
	// the next Build, over the real wire, matching what was persisted.
	res2, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: store, RecordKeyPrefix: prefix})
	assertNoErrors(t, diags)
	assertMaterialized(t, res2, []string{`null_resource.trigger`})
	if len(res2.RecordVersions) != 1 {
		t.Fatalf("RecordVersions = %v, want one entry", res2.RecordVersions)
	}
	inst := res2.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object hydrated from SSM")
	}
	hydrated, err := inst.Current.Decode(schema.Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding: %s", err)
	}
	if got := hydrated.Value.GetAttr("id"); got.AsString() != "ssm-created-id" {
		t.Errorf("id = %v, want ssm-created-id", got)
	}

	// 4. Delete: write-back for a run whose final state no longer has the
	// address (a destroy) removes the SSM parameter, with the same version
	// check.
	emptyState := states.NewState()
	wbDiags = WriteBack(ctx, WriteBackRequest{
		Store:         store,
		KeyPrefix:     prefix,
		PriorVersions: res2.RecordVersions,
		FinalState:    emptyState,
		Schemas:       ssmTestSchemas(schema),
	})
	assertNoErrors(t, wbDiags)

	_, _, exists, err := store.Get(ctx, RecordKey(prefix, addr))
	if err != nil {
		t.Fatalf("checking deletion: %s", err)
	}
	if exists {
		t.Error("the SSM parameter still exists after write-back's delete")
	}
}

// ssmTestSchemas builds the *tofu.Schemas WriteBack needs to decode
// null_resource's final-state object, keyed under nullProvider's own
// provider FQN.
func ssmTestSchemas(schema providers.Schema) *tofu.Schemas {
	return &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		},
	}
}

func requireDockerDaemonForSSM(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker info failed, so the docker daemon is not usable here: %v\n%s", err, out)
	}
}

func randomSegment(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generating a random key segment: %v", err)
	}
	return hex.EncodeToString(b[:])
}
