package residue

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestSmokeScriptsReadOnlyClaimsKeysThatExist is #1691's guard. A shell
// script under live/smoke reads claims.json with Python's cell.get("key"),
// and a key the schema no longer has reads as its default forever: #1598
// renamed real_aws to real_service, smoke.sh kept reading real_aws, and the
// real-AWS carve-out from the stall bound (#1593) silently stopped applying.
// Every proof.get or cell.get key a script reads must be a json tag on
// smokeProof or smokeClaimProviderCell (#1817 moved the scenario fields from
// the cell into its proofs).
func TestSmokeScriptsReadOnlyClaimsKeysThatExist(t *testing.T) {
	tags := map[string]bool{}
	for _, ty := range []reflect.Type{reflect.TypeOf(smokeClaimProviderCell{}), reflect.TypeOf(smokeProof{})} {
		for i := 0; i < ty.NumField(); i++ {
			name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				tags[name] = true
			}
		}
	}
	if len(tags) == 0 {
		t.Fatal("smokeClaimProviderCell and smokeProof have no json tags; the guard would pass vacuously")
	}
	scripts, err := filepath.Glob(filepath.Join("smoke", "*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts under live/smoke (%v)", err)
	}
	get := regexp.MustCompile(`(?:cell|proof)\.get\("([a-z_]+)"`)
	seen := 0
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range get.FindAllStringSubmatch(string(b), -1) {
			seen++
			if !tags[m[1]] {
				t.Errorf("%s reads claims.json key %q, which neither smokeClaimProviderCell nor smokeProof has: it will read as its default for every proof", s, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no proof.get or cell.get reads found under live/smoke; the guard would pass vacuously")
	}
}
