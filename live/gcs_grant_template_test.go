// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

const (
	gcsGrantRenderer = "../examples/record-store-gcs/iam/render-grant.sh"
	gcsGrantBucket   = "choudoufu-records"
	gcsGrantMember   = "serviceAccount:prod-apply@my-project.iam.gserviceaccount.com"
)

func renderGCSGrant(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatal("bash is not on PATH; this runs the real renderer and must not be skipped")
	}
	return exec.Command("bash", append([]string{gcsGrantRenderer}, args...)...).Output()
}

type gcsGrant struct {
	Bindings []struct {
		Role      string   `json:"role"`
		Members   []string `json:"members"`
		Condition *struct {
			Expression string `json:"expression"`
		} `json:"condition"`
	} `json:"bindings"`
}

// TestGCSGrantTemplateHasOneSource: the example is the renderer's output,
// byte for byte, so the page people copy cannot drift from the script.
func TestGCSGrantTemplateHasOneSource(t *testing.T) {
	out, err := renderGCSGrant(t, "prod", gcsGrantBucket, gcsGrantMember)
	if err != nil {
		t.Fatalf("render-grant.sh: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(filepath.Dir(gcsGrantRenderer), "example-prod.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(want) {
		t.Errorf("example-prod.json is not what render-grant.sh prod %s %s prints; re-render it", gcsGrantBucket, gcsGrantMember)
	}
}

// TestGCSGrantFencesExactlyTheStoresNamespaces: the conditioned binding names
// the three prefixes the store writes under, each with its trailing "/", and
// nothing else, for the default and for a prefix override.
func TestGCSGrantFencesExactlyTheStoresNamespaces(t *testing.T) {
	for _, tc := range []struct {
		rs   *configs.LiveRecordStore
		args []string
	}{
		{&configs.LiveRecordStore{Type: "gcs", Bucket: gcsGrantBucket}, nil},
		{&configs.LiveRecordStore{Type: "gcs", Bucket: gcsGrantBucket, KeyPrefix: "team/prod", KeyPrefixSet: true}, []string{"--key-prefix", "team/prod"}},
	} {
		out, err := renderGCSGrant(t, append([]string{"prod", gcsGrantBucket, gcsGrantMember}, tc.args...)...)
		if err != nil {
			t.Fatalf("render-grant.sh %v: %v", tc.args, err)
		}
		var g gcsGrant
		if err := json.Unmarshal(out, &g); err != nil {
			t.Fatalf("render-grant.sh %v printed something that is not JSON: %v", tc.args, err)
		}
		var conditioned []string
		for _, b := range g.Bindings {
			if !slices.Equal(b.Members, []string{gcsGrantMember}) {
				t.Errorf("binding %s grants %v, want only the member named", b.Role, b.Members)
			}
			if b.Role == "roles/storage.objectUser" {
				if b.Condition == nil {
					t.Fatal("the object binding has no condition, so it grants every estate's records")
				}
				for _, m := range regexp.MustCompile(`resource\.name\.startsWith\("projects/_/buckets/`+gcsGrantBucket+`/objects/([^"]*)"\)`).FindAllStringSubmatch(b.Condition.Expression, -1) {
					conditioned = append(conditioned, m[1])
				}
			}
		}
		want := projection.BucketNamespaces(tc.rs, "prod")
		for i := range want {
			want[i] = staterecord.NamespacePrefix(want[i])
		}
		if !slices.Equal(conditioned, want) {
			t.Errorf("render-grant.sh %v fences %v, want the store's namespaces %v", tc.args, conditioned, want)
		}
	}
}

func TestGCSGrantRendererRefusesWhatIsNotAName(t *testing.T) {
	for _, args := range [][]string{
		{"pr*d", gcsGrantBucket, gcsGrantMember},
		{"prod", "*", gcsGrantMember},
		{"prod", gcsGrantBucket, "allUsers"},
		{"prod", gcsGrantBucket, gcsGrantMember, "--key-prefix", `a") || true || ("`},
	} {
		if out, err := renderGCSGrant(t, args...); err == nil {
			t.Errorf("render-grant.sh accepted %v:\n%s", args, out)
		}
	}
}
