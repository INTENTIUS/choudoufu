// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The #2277 follow-up ruling ("Key from a CI secret"): when
// CHOUDOUFU_PLAN_HMAC_KEY is set, plan and apply take the key of the
// hashes from it, so a joint plan made in one job applies in another.
// The value never reaches a plan file or the session directory and is
// never printed.

func jekKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// jekPlanUnder plans in a fresh session with the environment's key and
// returns the unit's stub body, naming a planning session that the apply's
// machine does not have.
func jekPlanUnder(t *testing.T) (body []byte, stub jointStub, sessionDir string) {
	t.Helper()
	sess := &jointSession{dir: t.TempDir()}
	key, from, err := sess.sensitiveKey()
	if err != nil {
		t.Fatal(err)
	}
	if from != jointKeyFromEnv {
		t.Fatalf("key source = %q with %s set, want %q", from, jointKeyEnv, jointKeyFromEnv)
	}
	if _, err := os.Stat(sess.path(jointKeyFile)); !os.IsNotExist(err) {
		t.Errorf("planning with %s set wrote %s to the session (%v)", jointKeyEnv, jointKeyFile, err)
	}
	plan, doc, hashes, err := jointDropSensitive(jsvPlanFile(t, jsvPlanned), jsvDoc(jsvPlanned), key)
	if err != nil {
		t.Fatal(err)
	}
	units := []jointUnit{{ID: "db", Dir: "/r/db", Seq: 1, Out: "/out/db/tfplan"}}
	b, err := jointBundleFor("db", plan, doc, units, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Scrubbed, b.Sensitive = true, hashes
	b.KeyFrom, b.KeyCheck = from, jointKeyCheck(key)
	// The planning job's session is on another machine.
	gone := filepath.Join(t.TempDir(), "planned-elsewhere")
	body, err = json.Marshal(jointStub{Magic: 1, Session: gone, Unit: "db", Serial: 1, Units: []string{"db"}, Run: "run-1", Tool: "terragrunt", Bundle: b})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &stub); err != nil {
		t.Fatal(err)
	}
	return body, stub, sess.dir
}

// jekNoKeyIn fails when any part of the plan file, or any file of the
// session, holds the key or the variable's text.
func jekNoKeyIn(t *testing.T, body []byte, sessionDir string, key []byte, envText string) {
	t.Helper()
	parts := jsvBytesOf(t, body)
	_ = filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, err := os.ReadFile(path); err == nil {
				parts = append(parts, b)
			}
		}
		return nil
	})
	for i, part := range parts {
		if bytes.Contains(part, key) || bytes.Contains(part, []byte(envText)) {
			t.Errorf("part %d of the plan file or session holds the key from %s", i, jointKeyEnv)
		}
	}
}

// TestJointEnvKeyCrossSessionApply: planned with the key in the
// environment, a unit's plan file applies in another session (the planning
// session's directory is not there) with the same variable, hex or base64.
func TestJointEnvKeyCrossSessionApply(t *testing.T) {
	key := jekKey(0x5a)
	t.Setenv(jointKeyEnv, hex.EncodeToString(key))
	body, stub, sessionDir := jekPlanUnder(t)
	jekNoKeyIn(t, body, sessionDir, key, hex.EncodeToString(key))
	if _, err := os.Stat(stub.Session); !os.IsNotExist(err) {
		t.Fatalf("the planning session %s is here; the test would prove nothing", stub.Session)
	}

	for name, text := range map[string]string{
		"hex":        hex.EncodeToString(key),
		"base64":     base64.StdEncoding.EncodeToString(key),
		"base64-url": base64.RawURLEncoding.EncodeToString(key),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(jointKeyEnv, text)
			got, err := jointReadKey(stub.Session, stub.Bundle.KeyFrom, stub.Bundle.KeyCheck)
			if err != nil {
				t.Fatalf("apply in another session refused: %s", err)
			}
			approved := jsvApproved(t, stub.Bundle.Plan)
			refused := false
			check := &jointSensitiveCheck{Hashes: stub.Bundle.Sensitive, Key: got}
			guard := check.guard(approved, approvalGuard(approved, &refused), &refused)
			if diags := guard(jsvPlan(t, jsvPlanned), jsvRunSchemas()); diags.HasErrors() || refused {
				t.Fatalf("apply with the same values and the same key refused: %s", diags.Err())
			}
		})
	}
}

// TestJointEnvKeyMismatchRefuses: the apply's variable holds another key
// than the plan's. It refuses with "not the key the plan was made with",
// naming the unit, address and attribute, and printing neither key; the
// hashes alone refuse it too. An apply with the variable unset refuses,
// saying the plan was made with it.
func TestJointEnvKeyMismatchRefuses(t *testing.T) {
	planKey, applyKey := jekKey(0x11), jekKey(0x22)
	t.Setenv(jointKeyEnv, hex.EncodeToString(planKey))
	_, stub, _ := jekPlanUnder(t)

	t.Setenv(jointKeyEnv, hex.EncodeToString(applyKey))
	_, err := jointReadKey(stub.Session, stub.Bundle.KeyFrom, stub.Bundle.KeyCheck)
	if err == nil || !strings.Contains(err.Error(), "not the key the plan was made with") {
		t.Fatalf("apply with another key = %v, want a refusal saying the key differs", err)
	}
	detail := jointMissingKeyDetail("/out/db/tfplan", stub.Session, stub.Bundle.KeyFrom, stub.Bundle.Sensitive, err)
	for _, want := range []string{jointKeyEnv, "unit db: module.db.test_thing.a (sensitive after password)"} {
		if !strings.Contains(detail, want) {
			t.Errorf("refusal does not say %q:\n%s", want, detail)
		}
	}
	for _, k := range [][]byte{planKey, applyKey} {
		for _, text := range []string{hex.EncodeToString(k), base64.StdEncoding.EncodeToString(k), string(k)} {
			if strings.Contains(detail, text) {
				t.Errorf("refusal prints a key:\n%s", detail)
			}
		}
	}

	// Without the key check, the hashes under another key do not match.
	approved := jsvApproved(t, stub.Bundle.Plan)
	refused := false
	guard := (&jointSensitiveCheck{Hashes: stub.Bundle.Sensitive, Key: applyKey}).guard(approved, nil, &refused)
	if diags := guard(jsvPlan(t, jsvPlanned), jsvRunSchemas()); !diags.HasErrors() || !refused {
		t.Error("hashes checked under another key did not refuse")
	}

	t.Setenv(jointKeyEnv, "")
	if _, err := jointReadKey(stub.Session, stub.Bundle.KeyFrom, stub.Bundle.KeyCheck); err == nil || !strings.Contains(err.Error(), jointKeyEnv) {
		t.Errorf("apply with %s unset = %v, want a refusal naming it", jointKeyEnv, err)
	}
}

// TestJointEnvKeyShortRefuses: a key under 32 bytes, or text that is
// neither hex nor base64, refuses at plan and at apply with a message that
// names the variable and the rule and not the value. Planning writes no
// key file then.
func TestJointEnvKeyShortRefuses(t *testing.T) {
	short := hex.EncodeToString(bytes.Repeat([]byte{0x33}, 16))
	for name, text := range map[string]string{
		"short-hex":    short,
		"short-base64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 31)),
		"not-encoded":  "correct horse battery staple!!",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(jointKeyEnv, text)
			sess := &jointSession{dir: t.TempDir()}
			_, _, err := sess.sensitiveKey()
			if err == nil {
				t.Fatal("planning with a short or undecodable key did not refuse")
			}
			msg := err.Error()
			if !strings.Contains(msg, jointKeyEnv) || !strings.Contains(msg, "at least 32") {
				t.Errorf("refusal does not name %s and the 32-byte rule:\n%s", jointKeyEnv, msg)
			}
			if strings.Contains(msg, text) {
				t.Errorf("refusal prints the variable's value:\n%s", msg)
			}
			if _, err := os.Stat(sess.path(jointKeyFile)); !os.IsNotExist(err) {
				t.Errorf("a refused key still wrote %s (%v)", jointKeyFile, err)
			}
			if _, err := jointReadKey(sess.dir, jointKeyFromEnv, ""); err == nil || strings.Contains(err.Error(), text) {
				t.Errorf("apply with a short key = %v, want a refusal without the value", err)
			}
		})
	}
	if _, err := jointDecodeKey(short); err == nil || !strings.Contains(err.Error(), "16 bytes") {
		t.Errorf("16-byte key = %v, want it named as 16 bytes", err)
	}
}
