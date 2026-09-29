// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1503. Under `strict { secrets = "refuse" }`, a rotated
// database password planned `No changes.` and was therefore never sent:
// [configuredAttrsSeed] seeded the import stub with the CONFIGURATION's own
// current value for `password`, the provider's Read never touches that
// attribute (RDS does not return it), so the projected prior came back
// holding exactly the value the configuration was proposing and the diff was
// empty. A silent no-op on a secret rotation.
//
// Both documents that describe the setting say the opposite -
// site/content/docs/use/secrets.md and live/SECRETS.md: "A sensitive
// argument the API never returns is left out of its record, so every plan
// shows it as a change" - and so does the fork's own lint warning for the
// same attribute ([residueWarning], "Attribute value cannot round-trip a
// live replan"): "Every live plan will therefore propose sending the value
// again - the same perpetual diff stock `terraform import` produces for this
// argument."
//
// # What this fixture holds sensitive, and where
//
// `password` is Sensitive in the SCHEMA and the variable feeding it is an
// ordinary, unmarked one. That is deliberate: the seed skip under test keys
// on the provider schema's own Sensitive flag, which is the same flag
// [residueCandidates], [fillResidue] and lint's [residueFlag] already key on
// for this setting. A `sensitive = true` variable would put a cty mark on
// the decoded value too and the test would then pass for a fix that only
// looked at marks, which is not the rule these four places share.

// stubSecretDBSchema is aws_db_instance's shape for the one argument that
// matters, at hashicorp/aws 6.58.0: `password` is Optional and Sensitive and
// never Computed - settable by configuration alone, which is exactly the
// population [configuredAttrsSeed] seeds.
//
// Deliberately NOT [stubDBSchema] (sensitive_rpc_test.go), whose own doc
// comment records that its `password` is non-Sensitive on purpose for the
// marks question. Two fixtures, two different questions.
func stubSecretDBSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":       {Type: cty.String, Computed: true},
			"name":     {Type: cty.String, Required: true},
			"password": {Type: cty.String, Optional: true, Sensitive: true},
		},
	}}
}

// secretDBProvider is hashicorp/aws's aws_db_instance read, reduced to the
// one behaviour #1503 turns on: DescribeDBInstances has no password in its
// response and the provider never calls d.Set("password", ...), so whatever
// the prior state carried for it survives the read verbatim. An import stub
// that arrives carrying the configuration's own password therefore comes
// back out of ReadResource still carrying it.
//
// priorPassword records what PriorState held on the way in, which is the
// measurement the seed itself is asserted by.
func secretDBProvider(priorPassword *cty.Value) *tofu.MockProvider {
	schema := stubSecretDBSchema()
	ty := schema.Block.ImpliedType()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_secretdb": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		attrs := make(map[string]cty.Value, len(ty.AttributeTypes()))
		for name, aty := range ty.AttributeTypes() {
			attrs[name] = cty.NullVal(aty)
		}
		attrs["id"] = cty.StringVal(r.Target.ID)
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    cty.ObjectVal(attrs),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		pw := r.PriorState.GetAttr("password")
		if priorPassword != nil {
			*priorPassword = pw
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			// The API answers id and name and nothing else this fixture
			// declares; password is left exactly as the prior held it.
			"id":       cty.StringVal("db-1"),
			"name":     cty.StringVal("app-db"),
			"password": pw,
		})}
	}
	return p
}

// secretRoot writes the estate #1503 measured: one resource whose sensitive
// settable argument comes from a variable, under a named secrets setting.
// A temp root rather than a committed fixture because the two settings are
// one character apart and the value has to move between runs.
func secretRoot(t *testing.T, secrets strict.Secrets, password string) *configs.Config {
	t.Helper()
	src := fmt.Sprintf(`
terraform {
  live {
    estate = "secret-seed"
    strict {
      secrets = %q
    }
  }
}

variable "db_password" {
  type    = string
  default = %q
}

resource "stub_secretdb" "main" {
  name     = "app-db"
  password = var.db_password
}
`, string(secrets), password)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return loadConfig(t, dir)
}

// projectSecretDB runs the root through the same BuildFrom path a live plan
// does and returns the projected prior plus what the provider's Read was
// handed for `password`.
func projectSecretDB(t *testing.T, cfg *configs.Config) (*tofu.MockProvider, *Result, cty.Value) {
	t.Helper()
	var priorPassword cty.Value
	p := secretDBProvider(&priorPassword)
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `stub_secretdb.main`), Class: identity.ClassConcrete, ImportID: "db-1", IdentityValues: map[string]string{"name": "app-db"}},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_secretdb.main`})
	return p, res, priorPassword
}

// planSecretDB plans cfg against the projected prior with the same provider
// the projection asked, exactly as [TestFrameworkTimeoutsSeedReplansAsNoChange]
// does, and returns the one instance's change decoded.
func planSecretDB(t *testing.T, cfg *configs.Config, p *tofu.MockProvider, res *Result, password string) *plans.ResourceInstanceChange {
	t.Helper()
	addr := mustAddr(t, `stub_secretdb.main`)
	plan, diags := stubContext(t, p).Plan(context.Background(), cfg, res.State, &tofu.PlanOpts{
		Mode:         plans.NormalMode,
		SetVariables: tofu.InputValues{"db_password": &tofu.InputValue{Value: cty.StringVal(password), SourceType: tofu.ValueFromCaller}},
	})
	if diags.HasErrors() {
		t.Fatalf("planning against the projected state: %s", diags.Err())
	}
	src := plan.Changes.ResourceInstance(addr)
	if src == nil {
		t.Fatalf("the plan holds no change at all for %s; changes: %v", addr, changedAddrs(plan))
	}
	schema := stubSecretDBSchema()
	change, err := src.Decode(&schema)
	if err != nil {
		t.Fatalf("decoding the change for %s: %s", addr, err)
	}
	return change
}

// TestRefusedSensitiveArgumentIsProposedAgain is #1503's reproduction.
//
// Measured red at d770559366, before the fix:
//
//	the provider's ReadResource was handed password = cty.StringVal("rotated-2026-09")
//	  under refuse; the seed hands the provider the very value the plan is
//	  meant to be proposing, so the prior and the desired value can never differ
//	stub_secretdb.main replans as NoOp under secrets = "refuse", want Update
//	  proposing password = "rotated-2026-09"
//
// The apply step of the sequence in the issue is what the fixture's live
// object already is: a database created earlier whose password is whatever
// it is, and which no API call will ever tell us. The rotation is the
// configuration's current value, and the whole question is whether a plan
// proposes sending it.
func TestRefusedSensitiveArgumentIsProposedAgain(t *testing.T) {
	const rotated = "rotated-2026-09"
	cfg := secretRoot(t, strict.Refuse, rotated)
	p, res, priorPassword := projectSecretDB(t, cfg)

	if priorPassword != cty.NilVal && !priorPassword.IsNull() {
		got, _ := priorPassword.Unmark()
		t.Errorf("the provider's ReadResource was handed password = %#v under secrets = %q.\n"+
			"A sensitive argument the API never returns has no prior value this fork is allowed to know: "+
			"seeding it from the configuration makes the prior and the desired value the same by construction, "+
			"so the rotation can never show as a change. Want a null prior for it.", got, strict.Refuse)
	}

	change := planSecretDB(t, cfg, p, res, rotated)
	if change.Action != plans.Update {
		t.Errorf("stub_secretdb.main replans as %s under secrets = %q, want %s.\n"+
			"site/content/docs/use/secrets.md and live/SECRETS.md both say a sensitive argument the API never "+
			"returns is left out of its record so every plan shows it as a change, and lint's own "+
			"%q warning promises the same. A NoOp here means the rotated value is silently never sent.",
			change.Action, strict.Refuse, plans.Update, "Attribute value cannot round-trip a live replan")
	}
	after, _ := change.After.GetAttr("password").Unmark()
	if after.IsNull() || after.AsString() != rotated {
		t.Errorf("the plan proposes password = %#v, want %q: the point of proposing the change is sending the new value", after, rotated)
	}
	before, _ := change.Before.GetAttr("password").Unmark()
	if !before.IsNull() {
		t.Errorf("the plan's prior holds password = %#v, want null: nothing under refuse carries the value back", before)
	}
}

// TestStoredSensitiveArgumentIsStillSeeded is the other arm, and the reason
// the skip is gated on the setting rather than applied to every Sensitive
// attribute. Under the default `secrets = "store"` the record store is what
// carries the last-applied value back ([fillResidue]'s #393 branch), the
// seed keeps doing exactly what #395 and #376 need it to, and nothing about
// the projected prior moves.
func TestStoredSensitiveArgumentIsStillSeeded(t *testing.T) {
	const configured = "hunter2"
	cfg := secretRoot(t, strict.Store, configured)
	_, _, priorPassword := projectSecretDB(t, cfg)

	if priorPassword == cty.NilVal || priorPassword.IsNull() {
		t.Fatalf("the provider's ReadResource was handed password = %#v under secrets = %q, want the configured value.\n"+
			"Under the default setting this attribute is an ordinary residue candidate and the seed is what stands "+
			"in for a persisted state file's PriorState; skipping it here would be a behaviour change well outside #1503.",
			priorPassword, strict.Store)
	}
	got, _ := priorPassword.Unmark()
	if got.AsString() != configured {
		t.Errorf("the provider's ReadResource was handed password = %q under secrets = %q, want %q", got.AsString(), strict.Store, configured)
	}
}

// TestConfiguredAttrsSeedAndTheSecretsSetting asks [configuredAttrsSeed]
// itself, so the rule is pinned at the unit it lives in and not only through
// a provider: the VALUE of a Sensitive attribute is withheld under refuse,
// every other attribute is seeded either way, and the attribute's
// configuration MARKS are still returned under both settings.
//
// The marks half is not incidental. GitHub issue #401 family 3 is a
// config-derived mark that never comes back on the projected prior turning
// an unchanged value into a perpetual sensitivity-only diff, and the same
// trap is one line away here: the skip belongs beside the existing
// `attr.Computed` one, AFTER [configuredAttrSeed] has run and the marks loop
// has collected what it found, never before it.
func TestConfiguredAttrsSeedAndTheSecretsSetting(t *testing.T) {
	ctx := context.Background()
	schema := stubSecretDBSchema()

	for _, tc := range []struct {
		secrets  strict.Secrets
		wantSeed bool
	}{
		{strict.Store, true},
		{strict.Refuse, false},
	} {
		t.Run(string(tc.secrets), func(t *testing.T) {
			cfg := secretRootSensitiveVar(t, tc.secrets)
			rc := cfg.Module.ManagedResources["stub_secretdb.main"]
			if rc == nil {
				t.Fatalf("fixture has no stub_secretdb.main; has %v", managedKeys(cfg))
			}
			seed, marks := configuredAttrsSeed(ctx, cfg.Module.StaticEvaluator, cfg.Path, rc, schema, nil, tc.secrets)

			if _, ok := seed["password"]; ok != tc.wantSeed {
				t.Errorf(`seed["password"] present = %v under secrets = %q, want %v (seed keys: %v)`, ok, tc.secrets, tc.wantSeed, seedKeys(seed))
			}
			if got, ok := seed["name"]; !ok || got.AsString() != "app-db" {
				t.Errorf(`seed["name"] = %#v, ok=%v, want "app-db": the setting governs Sensitive attributes only`, got, ok)
			}
			var sawPasswordMark bool
			for _, pvm := range marks {
				if len(pvm.Path) == 1 {
					if step, ok := pvm.Path[0].(cty.GetAttrStep); ok && step.Name == "password" {
						sawPasswordMark = true
					}
				}
			}
			if !sawPasswordMark {
				t.Errorf("no configuration mark returned for password under secrets = %q; marks = %#v.\n"+
					"Withholding the VALUE must not withhold the MARK: that is GitHub issue #401 family 3, a "+
					"perpetual sensitivity-only diff.", tc.secrets, marks)
			}
		})
	}
}

// secretRootSensitiveVar is [secretRoot] with a `sensitive = true` variable,
// so the configuration expression for password carries a mark for
// [TestConfiguredAttrsSeedAndTheSecretsSetting]'s marks half to find.
func secretRootSensitiveVar(t *testing.T, secrets strict.Secrets) *configs.Config {
	t.Helper()
	src := fmt.Sprintf(`
terraform {
  live {
    estate = "secret-seed"
    strict {
      secrets = %q
    }
  }
}

variable "db_password" {
  type      = string
  sensitive = true
  default   = "hunter2"
}

resource "stub_secretdb" "main" {
  name     = "app-db"
  password = var.db_password
}
`, string(secrets))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return loadConfig(t, dir)
}

// managedKeys is the fixture's own managed-resource keys, for a failure
// message that says what was loaded rather than only what was missing.
func managedKeys(cfg *configs.Config) []string {
	out := make([]string, 0, len(cfg.Module.ManagedResources))
	for k := range cfg.Module.ManagedResources {
		out = append(out, k)
	}
	return out
}
