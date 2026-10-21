// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// googleLabelsSchema is testdata/google-8.6.0-labels.json: the labels and
// effective_labels attributes of every hashicorp/google and google-beta
// 8.6.0 resource type that has either, as `tofu providers schema -json`
// printed them. Regenerate it when the pin moves, and read every line this
// file's tests then fail on.
type googleLabelsSchema struct {
	ProviderVersion string                                          `json:"provider_version"`
	Resources       map[string]map[string]map[string]schemaJSONAttr `json:"resources"`
}

type schemaJSONAttr struct {
	Type        json.RawMessage `json:"type"`
	Optional    bool            `json:"optional"`
	Required    bool            `json:"required"`
	Computed    bool            `json:"computed"`
	Description string          `json:"description"`
}

func loadGoogleLabelsSchema(t *testing.T) googleLabelsSchema {
	t.Helper()
	raw, err := os.ReadFile("testdata/google-8.6.0-labels.json")
	if err != nil {
		t.Fatal(err)
	}
	var s googleLabelsSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Resources["google"]) == 0 {
		t.Fatal("testdata/google-8.6.0-labels.json holds no google types; a test over nothing passes for nothing")
	}
	return s
}

func (a schemaJSONAttr) attribute(t *testing.T) *configschema.Attribute {
	t.Helper()
	ty, err := ctyjson.UnmarshalType(a.Type)
	if err != nil {
		t.Fatal(err)
	}
	return &configschema.Attribute{Type: ty, Optional: a.Optional, Required: a.Required, Computed: a.Computed, Description: a.Description}
}

func (s googleLabelsSchema) block(t *testing.T, provider, typeName string) *configschema.Block {
	t.Helper()
	b := &configschema.Block{Attributes: map[string]*configschema.Attribute{"name": {Type: cty.String, Optional: true}}}
	for name, a := range s.Resources[provider][typeName] {
		b.Attributes[name] = a.attribute(t)
	}
	return b
}

// TestGoogleLabelSurfaceAtThePin pins which google 8.6.0 types are the
// google-labels surface and which have a propagation-only labels map. The
// two instance templates are the only propagation-only types; a release
// that rewords their description fails here instead of starting to stamp
// every VM a managed instance group makes (GitHub issue #1119).
func TestGoogleLabelSurfaceAtThePin(t *testing.T) {
	s := loadGoogleLabelsSchema(t)
	for _, provider := range []string{"google", "google-beta"} {
		var surface, propagated []string
		for typeName := range s.Resources[provider] {
			b := s.block(t, provider, typeName)
			if _, ok := GoogleLabelSurface(b); ok {
				surface = append(surface, typeName)
			}
			if _, ok := GooglePropagatedLabels(b); ok {
				propagated = append(propagated, typeName)
			}
		}
		sort.Strings(propagated)
		want := []string{"google_compute_instance_template", "google_compute_region_instance_template"}
		if len(propagated) != len(want) || propagated[0] != want[0] || propagated[1] != want[1] {
			t.Errorf("%s: propagation-only labels on %q, want exactly %q", provider, propagated, want)
		}
		if provider == "google" && len(surface) != 297 {
			// 301 types with a settable labels map; less the two with no
			// effective_labels (#1121's measurement) and the two templates.
			t.Errorf("google: %d types are the google-labels surface, want 297", len(surface))
		}
		for _, typeName := range want {
			if _, ok := GoogleLabelSurface(s.block(t, provider, typeName)); ok {
				t.Errorf("%s: %s is the google-labels surface; its labels belong to the instances it creates", provider, typeName)
			}
		}
	}
}
