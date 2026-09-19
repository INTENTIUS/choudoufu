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
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

// TestRecordBackedLifecycleAgainstS3 is live/e2e/record-store/run.sh's
// claim (hydrate, clean re-plan-equivalent, write-back, delete) proved
// again with a [staterecord.S3Store] talking to an S3 served by floci,
// instead of [staterecord.LocalStore] on disk. The LOCAL variant is the
// behavioral e2e that exercises a real `choudoufu` binary end to end (see
// live/e2e/record-store/); this is the narrower, package-level proof that
// the same hydration/write-back machinery in this package works unchanged
// against the other backend GitHub issue #73's store abstraction supports.
//
// Until GitHub issue #1346 this ran against the Parameter Store backend.
// That store was retired and the test moved to the one that replaced it, so
// the package kept a lifecycle proof against a backend over the network.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/projection/ -run TestRecordBackedLifecycleAgainstS3 -v
func TestRecordBackedLifecycleAgainstS3(t *testing.T) {
	flocitest.Gate(t, "projection/record-backed-s3")
	flocitest.RequireBinary(t, "docker")
	requireDockerDaemon(t)

	port := flocitest.StartFloci(t, "projection-record-s3")
	endpoint := flocitest.Endpoint(port)

	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	bucket := "choudoufu-record-test-" + randomSegment(t)
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	prefix := "choudoufu-record-test/" + randomSegment(t) + "/"
	store, err := staterecord.NewS3Store(staterecord.S3Config{Client: client, Bucket: bucket})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}

	ctx := context.Background()
	cfg := loadConfig(t, writeNullResourceFixture(t))
	addr := mustAddr(t, `null_resource.trigger`)
	provs := SingleProvider(nullProvider, nullResourceProvider())

	// 1. Nothing in the store yet: the resolution is record-backed but
	// absent, so the plan should propose creating it - the same claim
	// TestBuildRecordBackedAbsent makes against the local store.
	resolutions := []identity.Resolution{{Addr: addr, Class: identity.ClassRecordBacked}}
	rs := NewRecordEnvelopeStore(store, prefix)
	res, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: rs})
	assertNoErrors(t, diags)
	assertOmitted(t, res, map[string]Reason{`null_resource.trigger`: ReasonAbsent})

	// 2. Write-back, as if an apply had just created it: persists the
	// first record via a real s3:PutObject with If-None-Match.
	schema := nullResourceSchema()
	newVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("s3-created-id"),
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
		Store:         rs,
		PriorVersions: nil, // nothing existed yet: create semantics (expectedVersion "")
		FinalState:    finalState,
		Schemas:       recordTestSchemas(schema),
	})
	assertNoErrors(t, wbDiags)

	// 3. Re-hydrate: the record just written back should now come back on
	// the next Build, over the real wire, matching what was persisted.
	res2, diags := BuildWith(ctx, cfg, resolutions, provs, Options{RecordStore: rs})
	assertNoErrors(t, diags)
	assertMaterialized(t, res2, []string{`null_resource.trigger`})
	if len(res2.RecordVersions) != 1 {
		t.Fatalf("RecordVersions = %v, want one entry", res2.RecordVersions)
	}
	inst := res2.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object hydrated from S3")
	}
	hydrated, err := inst.Current.Decode(schema.Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding: %s", err)
	}
	if got := hydrated.Value.GetAttr("id"); got.AsString() != "s3-created-id" {
		t.Errorf("id = %v, want s3-created-id", got)
	}

	// 4. Delete: write-back for a run whose final state no longer has the
	// address (a destroy) removes the S3 object, with the same version
	// check.
	emptyState := states.NewState()
	wbDiags = WriteBack(ctx, WriteBackRequest{
		Store:         rs,
		PriorVersions: res2.RecordVersions,
		FinalState:    emptyState,
		Schemas:       recordTestSchemas(schema),
	})
	assertNoErrors(t, wbDiags)

	_, _, exists, err := store.Get(ctx, RecordKey(prefix, addr))
	if err != nil {
		t.Fatalf("checking deletion: %s", err)
	}
	if exists {
		t.Error("the S3 object still exists after write-back's delete")
	}
}

// recordTestSchemas builds the *tofu.Schemas WriteBack needs to decode
// null_resource's final-state object, keyed under nullProvider's own
// provider FQN.
func recordTestSchemas(schema providers.Schema) *tofu.Schemas {
	return &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
			},
		},
	}
}

func requireDockerDaemon(t *testing.T) {
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
