// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// terraformBin is the stock binary this pin drives. Stock rather than the
// fork's own: the schema dump below is a fact about the PROVIDER, and
// asking the tool under test for it would make the pin circular.
const terraformBin = "terraform"

// TestTaggableSetAgainstRealSchemas is TestTaggableSetCoversAdmissionTable's
// live half: the same taggability pin, answered by the schema the real AWS
// provider serves instead of the caricature in testSchemas. terraform init
// downloads the provider release the estate fixture pins, `terraform
// providers schema -json` dumps its schema without configuring anything, and
// the tags attribute of each admitted type is rebuilt as a configschema
// attribute and put through the real taggable predicate. A provider release
// that adds a tags argument to one of the untaggable four, or drops it from
// a type that has it, fails here on the version bump that brings it in - a
// reviewable failure instead of a silent change in which resources report
// SkipUntaggable.
//
// It is gated with the rest of this tier because it needs a terraform binary
// and a provider download, but it starts no emulator: a schema dump needs no
// cloud.
func TestTaggableSetAgainstRealSchemas(t *testing.T) {
	flocitest.Gate(t, "taggable-set pin")
	flocitest.RequireBinary(t, terraformBin)

	dir := flocitest.CopyEstate(t)
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-backend=false", "-input=false", "-no-color")

	cmd := exec.Command(terraformBin, "providers", "schema", "-json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("terraform providers schema -json failed: %v%s", err, stderrOfExit(err))
	}

	// The slice of the dump this test reads: per resource type, the fields of
	// the top-level tags attribute that the taggable predicate consults.
	type schemaAttr struct {
		Type     json.RawMessage `json:"type"`
		Optional bool            `json:"optional"`
		Required bool            `json:"required"`
		Computed bool            `json:"computed"`
		// Description is what the fifth clause of the predicate reads, and
		// it was missing here: the attribute was rebuilt without it, so
		// markers.VocabularyRefusal saw an empty string for every type and
		// this test could not exercise that clause even when it fired. On
		// hashicorp/aws 6.59.0 not one of the 847 tags attributes carries a
		// description, so the field arrives empty and nothing moves - which
		// is the point of reading it rather than assuming it. A release
		// that starts describing them is exactly the change this pin exists
		// to surface. Issue #285.
		Description string `json:"description"`
	}
	type resourceSchema struct {
		Block struct {
			Attributes map[string]schemaAttr `json:"attributes"`
		} `json:"block"`
	}
	var dump struct {
		ProviderSchemas map[string]struct {
			ResourceSchemas map[string]resourceSchema `json:"resource_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal(out, &dump); err != nil {
		t.Fatalf("decoding the schema dump: %v", err)
	}

	var resources map[string]resourceSchema
	for addr, ps := range dump.ProviderSchemas {
		if strings.HasSuffix(addr, "/aws") {
			resources = ps.ResourceSchemas
			break
		}
	}
	if resources == nil {
		t.Fatalf("the schema dump names no aws provider; it has %d provider(s)", len(dump.ProviderSchemas))
	}

	check := func(types []string, want bool) {
		for _, resourceType := range types {
			rs, ok := resources[resourceType]
			if !ok {
				t.Errorf("the provider serves no schema for admitted type %s", resourceType)
				continue
			}
			// taggable reads nothing but the top-level tags attribute, so
			// that attribute alone is rebuilt for it - INCLUDING its
			// description, which the fifth clause consults and which this
			// rebuild used to drop on the floor.
			block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
			if a, ok := rs.Block.Attributes["tags"]; ok {
				ty, err := ctyjson.UnmarshalType(a.Type)
				if err != nil {
					t.Errorf("decoding the type of %s.tags: %v", resourceType, err)
					continue
				}
				block.Attributes["tags"] = &configschema.Attribute{
					Type:        ty,
					Optional:    a.Optional,
					Required:    a.Required,
					Computed:    a.Computed,
					Description: a.Description,
				}
			}
			if got := taggable(block); got != want {
				t.Errorf("taggable(%s) = %v against the real provider schema, want %v; if the provider's schema changed, update the pin in stamp_test.go and the untaggable list in live/LIMITATIONS.md together",
					resourceType, got, want)
			}
		}
	}
	check(taggableAdmittedTypes, true)
	check(untaggableAdmittedTypes, false)
}

// stderrOfExit is the stderr an exec.ExitError carried, or nothing.
func stderrOfExit(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return "\n" + string(exit.Stderr)
	}
	return ""
}
