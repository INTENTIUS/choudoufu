// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #365 slice 3, and this file exists because an adversarial
// audit of that slice found the hole rather than any check in this
// repository.
//
// The slice gave secret-generating logical types a RecordBacked row so that
// `strict { secrets = "store" }` - the default, and what stock OpenTofu does -
// can keep their values. Two layers refuse them under `secrets = "refuse"`:
// internal/live/lint at the configuration, and internal/live/identity's
// resolver at the layer that acts.
//
// live-import runs NEITHER. internal/command/live_import.go loads the
// configuration for the estate name and the record store and nothing else -
// no lint pass, no resolver - so [Ratify] and [Ratification.Approve] were the
// only code between an operator who had turned the setting on and a stock
// state file's random_password.result landing in the estate's record store in
// clear. And what this path writes is not an identity or a residue attribute:
// it is the instance's WHOLE prior object, straight out of the state file.
//
// Everything below asserts on what the STORE HOLDS, for record_test.go's own
// stated reason: a record written when it should not have been produces a
// migration that reports success, and no verdict-level check can see it.

// secretPassword is the generated value the state file holds and the record
// store must or must not receive. It is deliberately distinctive so that a
// substring search over the whole store answers unambiguously.
const secretPassword = "quietly-noble-lemur-correct-horse-battery"

// secretProvider serves random_password, whose schema marks result and
// bcrypt_hash sensitive exactly as hashicorp/random 3.9.0 does - which is
// what live/logical-schemas.json measured and what
// identity.TypeIdentity.SecretMaterial is derived from.
type secretProvider struct{ *tofu.MockProvider }

func newSecretProvider() *secretProvider {
	sp := &secretProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"random_password": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":          {Type: cty.String, Computed: true},
					"length":      {Type: cty.Number, Required: true},
					"result":      {Type: cty.String, Computed: true, Sensitive: true},
					"bcrypt_hash": {Type: cty.String, Computed: true, Sensitive: true},
				}}},
			},
		},
	}}
	sp.ConfigureProviderCalled = true
	sp.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}
	return sp
}

func (p *secretProvider) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

// secretState is the stock state file a migration is pointed at: one
// random_password, its generated value recorded in clear, which is exactly
// how stock OpenTofu leaves it.
func secretState() *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "random_password", Name: "db"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"none","length":16,"result":"` + secretPassword + `","bcrypt_hash":"$2a$10$abc"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("random")},
		addrs.NoKey,
	)
	return state
}

// storeHoldsSecret reports whether ANY key in the store's record namespace
// holds the password, read as raw bytes rather than through this package's
// decoder.
//
// Asking the store rather than the key this migration would have used is the
// point: a future route that writes the same value somewhere else in the same
// namespace is the same defect, and a test keyed on one address could not see
// it.
func storeHoldsSecret(t *testing.T, store staterecord.Store, addr addrs.AbsResourceInstance) bool {
	t.Helper()
	raw, _, exists, err := store.Get(context.Background(), projection.RecordKey(recordTestPrefix(), addr))
	if err != nil {
		t.Fatalf("reading the record for %s: %s", addr, err)
	}
	if !exists {
		return false
	}
	return strings.Contains(string(raw), secretPassword)
}

func secretRatify(t *testing.T, store staterecord.Store, secrets strict.Secrets) *Ratification {
	t.Helper()
	rat, diags := Ratify(context.Background(), Request{
		Estate:      petEstate,
		State:       secretState(),
		Providers:   newSecretProvider(),
		Secrets:     secrets,
		RecordStore: projection.NewRecordEnvelopeStore(store, recordTestPrefix()),
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	return rat
}

// TestApprove_RefusesToSeedASecretRecordUnderSecretsRefuse is the defect
// itself, asserted on the store's own bytes.
func TestApprove_RefusesToSeedASecretRecordUnderSecretsRefuse(t *testing.T) {
	addr := mustAddr(t, "random_password.db")
	store := petStore(t)

	rat := secretRatify(t, store, strict.Refuse)
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}

	if storeHoldsSecret(t, store, addr) {
		t.Fatal("the migration wrote random_password.result into the estate's record store although this " +
			"configuration sets strict { secrets = \"refuse\" }.\n" +
			"live-import runs no lint pass and builds no resolver, so this package is the only gate on that " +
			"path - and what it writes is the instance's WHOLE prior object, not an identity.")
	}

	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %v, want SKIPPED - nothing was written, and the report must say so", out.Outcome)
	}
	if !strings.Contains(out.Detail, `secrets = "refuse"`) {
		t.Errorf("the outcome does not name the setting that withheld the record: %q", out.Detail)
	}
	// And the state file is what it always was: this package never writes
	// one, so nothing was lost by declining.
	if !strings.Contains(string(secretState().RootModule().Resources["random_password.db"].Instances[addrs.NoKey].Current.AttrsJSON), secretPassword) {
		t.Error("the fixture's own state file does not hold the value, so the assertion above proves nothing")
	}
}

// TestApprove_SeedsASecretRecordUnderTheDefault is the other half, and it is
// what makes the assertion above about the SETTING rather than about
// random_password being unreachable for some other reason.
//
// It is also the compatibility claim by value: stock OpenTofu's state file
// holds this password in clear, so a migration that dropped it would lose
// something stock does not lose - HANDOFF.md's second difference row.
func TestApprove_SeedsASecretRecordUnderTheDefault(t *testing.T) {
	addr := mustAddr(t, "random_password.db")
	store := petStore(t)

	rat := secretRatify(t, store, strict.DefaultSecrets)
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}

	if !storeHoldsSecret(t, store, addr) {
		t.Fatalf("the migration did not carry random_password.result into the record store under the default "+
			"secrets setting. The stock state file this run read holds it in clear, so dropping it loses "+
			"something stock does not lose. Outcomes: %+v", rep.Outcomes)
	}
	if out := onlyOutcome(t, rep); out.Outcome != OutcomeRecorded {
		t.Errorf("outcome = %v, want RECORDED", out.Outcome)
	}
}
