// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// #1120 unit 12: the Azure family's post-create write, and the tags
// arguments that are not ARM's tags map.

func azurermCreated(typ string, obj cty.Value) Created {
	return Created{
		Addr:     addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typ, Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Provider: addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("azurerm")},
		Object:   obj,
	}
}

// TestAzurePostCreateNeededIsTheGeneratedList asks every type the generated
// facts hold, and requires the answer to be exactly the after-create rows,
// each reason naming the create call and the call that writes the tags.
func TestAzurePostCreateNeededIsTheGeneratedList(t *testing.T) {
	var needed []string
	for _, row := range armtypes.DefaultRMTags().Rows() {
		reason, ok := Azure.PostCreateNeeded(markers.SurfaceARMTags, azurermCreated(row.Type, cty.NilVal), nil)
		if ok != (row.Create != armtypes.CreateInCreate) {
			t.Errorf("%s (%s): PostCreateNeeded = %v", row.Type, row.Create, ok)
		}
		if !ok {
			continue
		}
		needed = append(needed, row.Type)
		for _, want := range []string{row.Type, row.CreateCall, row.Call, armtypes.RMTagsPath} {
			if !strings.Contains(reason, want) {
				t.Errorf("%s: reason %q does not name %q", row.Type, reason, want)
			}
		}
	}
	sort.Strings(needed)
	if want := []string{"azurerm_app_service_managed_certificate", "azurerm_subscription"}; strings.Join(needed, ",") != strings.Join(want, ",") {
		t.Errorf("post-create types = %v, want %v", needed, want)
	}
	for _, typ := range []string{"azapi_resource", "azurerm_resource_group", "azurerm_api_management_named_value"} {
		if _, ok := Azure.PostCreateNeeded(markers.SurfaceARMTags, azurermCreated(typ, cty.NilVal), nil); ok {
			t.Errorf("%s needs no post-create write", typ)
		}
	}
	if _, ok := Azure.PostCreateNeeded(markers.SurfaceTags, azurermCreated("azurerm_subscription", cty.NilVal), nil); ok {
		t.Error("PostCreateNeeded answered for a surface that is not the family's")
	}
}

// TestAzurePostCreateWriteIsConsistent is #1742's three-way check on the
// Azure answers: withheld, written through the Tags API, by the writer
// every azurerm configuration builds, into the marker map.
func TestAzurePostCreateWriteIsConsistent(t *testing.T) {
	created := azurermCreated("azurerm_app_service_managed_certificate", cty.NilVal)
	for _, c := range []Created{{Addr: created.Addr}, created} {
		reason, write, needed, err := PostCreateWrite(markers.SurfaceARMTags, c, nil)
		if err != nil || !needed || write != WriteARMTagsAPI || reason == "" {
			t.Errorf("PostCreateWrite(provider %v) = %q, %q, %v, %v", c.Provider, reason, write, needed, err)
		}
	}
	if Azure.MarkerWriter() != WriteARMTagsAPI || Azure.Writes(markers.SurfaceARMTags).PostCreate != WriteARMTagsAPI {
		t.Error("the Azure surface and writer disagree")
	}
}

func TestARMTagScope(t *testing.T) {
	obj := func(attrs map[string]string) cty.Value {
		m := map[string]cty.Value{}
		for k, v := range attrs {
			m[k] = cty.StringVal(v)
		}
		return cty.ObjectVal(m)
	}
	cert := "/subscriptions/0/resourceGroups/rg/providers/Microsoft.Web/certificates/c"
	for name, tc := range map[string]struct {
		obj  cty.Value
		want string
	}{
		"resource id":   {obj(map[string]string{"id": cert}), cert},
		"case kept":     {obj(map[string]string{"id": "/SUBSCRIPTIONS/0/resourcegroups/rg"}), "/SUBSCRIPTIONS/0/resourcegroups/rg"},
		"alias":         {obj(map[string]string{"id": "/providers/Microsoft.Subscription/aliases/a", "subscription_id": "1111"}), "/subscriptions/1111"},
		"alias, no sub": {obj(map[string]string{"id": "/providers/Microsoft.Subscription/aliases/a"}), ""},
		"no id":         {obj(map[string]string{"name": "x"}), ""},
		"data plane":    {obj(map[string]string{"id": "https://kv.vault.azure.net/secrets/s", "subscription_id": "1111"}), ""},
		"nil":           {cty.NilVal, ""},
	} {
		if got := ARMTagScope(tc.obj); got != tc.want {
			t.Errorf("%s: ARMTagScope = %q, want %q", name, got, tc.want)
		}
	}
	fix := Azure.ManualMarkFix(Created{Object: obj(map[string]string{"id": "/providers/Microsoft.Subscription/aliases/a", "subscription_id": "1111"})}, map[string]string{markers.TagEstate: "prod"}, nil)
	if !strings.Contains(fix, "--resource-id /subscriptions/1111 ") {
		t.Errorf("ManualMarkFix = %q, want it to tag the subscription", fix)
	}
}

// azurermTagsExtractPath is every azurerm 5.10.0 managed resource type's
// tags attribute, as `tofu providers schema -json` gives it: the external
// source the next test holds the surface predicate and the generated facts
// to. It is regenerated from that schema with -update-azurerm-tags-extract
// and AZURERM_SCHEMA set (the variable internal/live/armtypes' pin test
// reads).
const azurermTagsExtractPath = "testdata/azurerm-5.10.0-tags.json"

var updateAzurermTagsExtract = flag.Bool("update-azurerm-tags-extract", false, "regenerate "+azurermTagsExtractPath+" from $AZURERM_SCHEMA")

type azurermTagsExtract struct {
	Provider     string `json:"provider"`
	Version      string `json:"version"`
	SchemaSHA256 string `json:"schema_sha256"`
	// Types maps each type with a tags attribute to that attribute's
	// schema JSON.
	Types map[string]json.RawMessage `json:"types"`
}

// TestAzureSurfaceMatchesTheSchemaAndTheFacts is the three never-a-marker
// types' guard. Their tags argument is a list of strings kept in the
// service's own properties, not ARM's tags map: API Management named
// values (two types) and a Sentinel threat-intelligence indicator. The
// arm-tags predicate ([markers.ARMTagSurface]) refuses them because their
// tags is not a map, so they fall to the untaggable path. This test reads
// the real schema's tags attributes and requires the predicate to answer
// true for exactly the generated facts' rows and false for exactly their
// tags_not_arm_tags list: the schema, the predicate and the facts agree on
// every type, and a provider bump that makes one of the three a map, or
// makes a fourth a list, fails here by name.
func TestAzureSurfaceMatchesTheSchemaAndTheFacts(t *testing.T) {
	if *updateAzurermTagsExtract {
		writeAzurermTagsExtract(t)
	}
	data, err := os.ReadFile(azurermTagsExtractPath)
	if err != nil {
		t.Fatal(err)
	}
	var ex azurermTagsExtract
	if err := json.Unmarshal(data, &ex); err != nil {
		t.Fatal(err)
	}
	facts := armtypes.DefaultRMTags()
	if ex.Version != facts.Version || ex.SchemaSHA256 != facts.SchemaSHA256 {
		t.Fatalf("the extract is azurerm %s (schema %s); the tag facts are %s (schema %s)", ex.Version, ex.SchemaSHA256, facts.Version, facts.SchemaSHA256)
	}
	notARM := map[string]bool{}
	for _, typ := range facts.NotARMTags {
		notARM[typ] = true
	}
	if len(notARM) != 3 {
		t.Errorf("the facts list %d types whose tags is not ARM's map, want the 3 this guard was written for: %v", len(notARM), facts.NotARMTags)
	}
	var carriers, refused int
	for typ, raw := range ex.Types {
		var attr struct {
			Type     json.RawMessage `json:"type"`
			Optional bool            `json:"optional"`
			Required bool            `json:"required"`
			Computed bool            `json:"computed"`
		}
		if err := json.Unmarshal(raw, &attr); err != nil {
			t.Fatalf("%s: %s", typ, err)
		}
		ty, err := ctyjson.UnmarshalType(attr.Type)
		if err != nil {
			t.Fatalf("%s: %s", typ, err)
		}
		block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"tags": {Type: ty, Optional: attr.Optional, Required: attr.Required, Computed: attr.Computed},
		}}
		surface, ok := SurfaceOf("azurerm", block)
		_, row := facts.Row(typ)
		switch {
		case notARM[typ]:
			refused++
			if ok {
				t.Errorf("%s: tags is %s, not ARM's map, and SurfaceOf answered %q", typ, ty.FriendlyName(), surface)
			}
			if !ty.IsListType() {
				t.Errorf("%s: the facts call its tags not ARM's map, and the schema says %s", typ, ty.FriendlyName())
			}
		case row:
			carriers++
			if !ok || surface != markers.SurfaceARMTags {
				t.Errorf("%s: a tags facts row, and SurfaceOf = %q, %v", typ, surface, ok)
			}
		default:
			t.Errorf("%s has a tags attribute (%s) and neither a facts row nor a place in tags_not_arm_tags", typ, ty.FriendlyName())
		}
	}
	if carriers != len(facts.Rows()) || refused != len(notARM) {
		t.Errorf("schema has %d carriers and %d refused; the facts have %d rows and %d not-ARM types", carriers, refused, len(facts.Rows()), len(notARM))
	}
}

// writeAzurermTagsExtract regenerates the extract from $AZURERM_SCHEMA,
// with the digest taken the way internal/live/armtypes takes it: over a
// canonical re-encoding of the azurerm provider's schema.
func writeAzurermTagsExtract(t *testing.T) {
	path := os.Getenv("AZURERM_SCHEMA")
	if path == "" {
		t.Fatal("-update-azurerm-tags-extract needs AZURERM_SCHEMA")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ProviderSchemas map[string]json.RawMessage `json:"provider_schemas"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, ok := doc.ProviderSchemas["registry.opentofu.org/hashicorp/azurerm"]
	if !ok {
		t.Fatal("no azurerm schema")
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	canon, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canon)
	var p struct {
		ResourceSchemas map[string]struct {
			Block struct {
				Attributes map[string]json.RawMessage `json:"attributes"`
			} `json:"block"`
		} `json:"resource_schemas"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	ex := azurermTagsExtract{
		Provider:     "hashicorp/azurerm",
		Version:      armtypes.DefaultRMTags().Version,
		SchemaSHA256: hex.EncodeToString(sum[:]),
		Types:        map[string]json.RawMessage{},
	}
	for typ, s := range p.ResourceSchemas {
		attr, ok := s.Block.Attributes["tags"]
		if !ok {
			continue
		}
		var a struct {
			Type     json.RawMessage `json:"type"`
			Optional bool            `json:"optional,omitempty"`
			Required bool            `json:"required,omitempty"`
			Computed bool            `json:"computed,omitempty"`
		}
		if err := json.Unmarshal(attr, &a); err != nil {
			t.Fatal(err)
		}
		compact, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		ex.Types[typ] = compact
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ex); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(azurermTagsExtractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(azurermTagsExtractPath, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
