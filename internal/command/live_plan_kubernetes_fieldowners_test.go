// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestFieldGranularFixedKind: the three field-granular types that name no
// kind patch the kind of the type their own name extends (GitHub issue
// #1191).
func TestFieldGranularFixedKind(t *testing.T) {
	for typeName, want := range map[string]string{
		"kubernetes_config_map_v1_data": "ConfigMap",
		"kubernetes_secret_v1_data":     "Secret",
		"kubernetes_node_taint":         "Node",
	} {
		apiVersion, kind := fieldGranularFixedKind(typeName)
		if apiVersion != "v1" || kind != want {
			t.Errorf("%s: %s %s, want v1 %s", typeName, apiVersion, kind, want)
		}
	}
}

// TestEnvWriteFollowsTheKindsPodSpec: one container's env is found under
// the pod template for a workload, under the pod's own spec for a Pod, and
// under the job template for a CronJob; init_container selects
// initContainers.
func TestEnvWriteFollowsTheKindsPodSpec(t *testing.T) {
	env := cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("LOG"), "value": cty.StringVal("debug")})})
	planned := func(container, initContainer cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{"container": container, "init_container": initContainer, "env": env})
	}
	app := planned(cty.StringVal("app"), cty.NullVal(cty.String))
	for kind, want := range map[string][]string{
		"Deployment": {"f:spec", "f:template", "f:spec", "f:containers", `k:{"name":"app"}`, "f:env"},
		"Pod":        {"f:spec", "f:containers", `k:{"name":"app"}`, "f:env"},
		"CronJob":    {"f:spec", "f:jobTemplate", "f:spec", "f:template", "f:spec", "f:containers", `k:{"name":"app"}`, "f:env"},
	} {
		w, ok := envWrite(app, kind)
		if !ok || !reflect.DeepEqual(w.Root, want) || !reflect.DeepEqual(w.Members, []string{`k:{"name":"LOG"}`}) {
			t.Errorf("%s: %+v (ok=%v), want root %v", kind, w, ok, want)
		}
	}
	w, ok := envWrite(planned(cty.NullVal(cty.String), cty.StringVal("init")), "Deployment")
	if !ok || w.Root[3] != "f:initContainers" {
		t.Errorf("init_container: %+v", w)
	}
	if _, ok := envWrite(planned(cty.NullVal(cty.String), cty.NullVal(cty.String)), "Deployment"); ok {
		t.Error("an env write naming no container was built")
	}
}

// TestTaintWriteKeysByKeyAndEffect: a taint is keyed in managedFields by
// its key and effect, never its value.
func TestTaintWriteKeysByKeyAndEffect(t *testing.T) {
	planned := cty.ObjectVal(map[string]cty.Value{"taint": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"key": cty.StringVal("dedicated"), "value": cty.StringVal("gpu"), "effect": cty.StringVal("NoSchedule"),
	})})})
	w, ok := taintWrite(planned)
	if !ok || !reflect.DeepEqual(w.Members, []string{`k:{"effect":"NoSchedule","key":"dedicated"}`}) {
		t.Errorf("taintWrite = %+v, %v", w, ok)
	}
}
