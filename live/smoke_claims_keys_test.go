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
// Every cell.get key a script reads must be a json tag on
// smokeClaimProviderCell.
func TestSmokeScriptsReadOnlyClaimsKeysThatExist(t *testing.T) {
	tags := map[string]bool{}
	ty := reflect.TypeOf(smokeClaimProviderCell{})
	for i := 0; i < ty.NumField(); i++ {
		name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			tags[name] = true
		}
	}
	if len(tags) == 0 {
		t.Fatal("smokeClaimProviderCell has no json tags; the guard would pass vacuously")
	}
	scripts, err := filepath.Glob(filepath.Join("smoke", "*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts under live/smoke (%v)", err)
	}
	get := regexp.MustCompile(`cell\.get\("([a-z_]+)"`)
	seen := 0
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range get.FindAllStringSubmatch(string(b), -1) {
			seen++
			if !tags[m[1]] {
				t.Errorf("%s reads claims.json cell key %q, which smokeClaimProviderCell does not have: it will read as its default for every cell", s, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no cell.get reads found under live/smoke; the guard would pass vacuously")
	}
}
