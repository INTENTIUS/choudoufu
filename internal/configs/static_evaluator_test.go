// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/zclconf/go-cty/cty"
)

// This exercises most of the logic in StaticEvaluator and staticScopeData
func TestStaticEvaluator_Evaluate(t *testing.T) {
	// Synthetic file for building test components
	testData := `
variable "str" {
	type = string
}
variable "str_map" {
	type = map(string)
}
variable "str_def" {
	type = string
	default = "sane default"
}
variable "str_map_def" {
	type = map(string)
	default = {
		keyA = "A"
	}
}
# Should/can variable checks be performed during static evaluation?

locals {
	# Simple static cases
	static = "static"
	static_ref = local.static
	static_fn = md5(local.static)
	path_root = path.root
	path_module = path.module

	# Variable References with Defaults
	var_str_def_ref = var.str_def
	var_map_def_access = var.str_map_def["keyA"]

	# Variable References without Defaults
	var_str_ref = var.str
	var_map_access = var.str_map["keyA"]

	# Bad References
	invalid_ref = invalid.attribute
	unavailable_ref = foo.bar.attribute
	module_whole_ref = module.child
	module_output_ref = module.child.out

	# Circular References
	circular = local.circular
	circular_ref = local.circular
	circular_a = local.circular_b
	circular_b = local.circular_a

	# Dependency chain
	ref_a = var.str
	ref_b = local.ref_a
	ref_c = local.ref_b

	# Missing References
	local_missing = local.missing
	var_missing = var.missing

	# Terraform 
	ws = terraform.workspace

	# Functions
	func = md5("my-string")
	missing_func = missing_fn("my-string")
	provider_func = provider::type::fn("my-string")
}

resource "foo" "bar" {}
	`

	parser := testParser(map[string]string{"eval.tf": testData})

	file, fileDiags := parser.LoadConfigFile("eval.tf")
	if fileDiags.HasErrors() {
		t.Fatal(fileDiags)
	}

	dummyIdentifier := StaticIdentifier{Subject: "local.test"}

	t.Run("Empty Eval", func(t *testing.T) {
		mod, _ := NewModule([]*File{file}, nil, RootModuleCallForTesting(), "dir", SelectiveLoadAll)
		emptyEval := StaticEvaluator{}

		// Expr with no traversals shouldn't access any fields
		value, diags := emptyEval.Evaluate(t.Context(), mod.Locals["static"].Expr, dummyIdentifier)
		if diags.HasErrors() {
			t.Error(diags)
		}
		if value.AsString() != "static" {
			t.Errorf("Expected %s got %s", "static", value.AsString())
		}

		// Expr with traversals should panic, indicating a programming error
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("should panic")
			}
		}()
		_, _ = emptyEval.Evaluate(t.Context(), mod.Locals["static_ref"].Expr, dummyIdentifier)
	})

	t.Run("Simple static cases", func(t *testing.T) {
		// The call's rootPath is "dir" here, matching mod's own SourceDir,
		// the same way every real construction site keeps a root module's
		// rootPath and SourceDir identical (see [StaticEvaluator.baseDir]
		// and [StaticEvaluator.modulePath]'s docs) - RootModuleCallForTesting's
		// placeholder "<testing>" would exercise a mismatch no real caller
		// produces for a root module, making path_module a value
		// (filepath.Rel("<testing>", "dir") = "../dir") this test would be
		// asserting by accident rather than by design.
		call := NewStaticModuleCall(nil, hcl.Range{}, func(_ *Variable) (cty.Value, hcl.Diagnostics) {
			panic("Variables have not been configured for this test!")
		}, "dir", "")
		mod, _ := NewModule([]*File{file}, nil, call, "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, call)

		locals := []struct {
			ident string
			value string
		}{
			{"static", "static"},
			{"static_ref", "static"},
			{"static_fn", "a81259cef8e959c624df1d456e5d3297"},
			{"path_root", "dir"},
			{"path_module", "."},
		}
		for _, local := range locals {
			t.Run(local.ident, func(t *testing.T) {
				value, diags := eval.Evaluate(t.Context(), mod.Locals[local.ident].Expr, dummyIdentifier)
				if diags.HasErrors() {
					t.Error(diags)
				}
				if value.AsString() != local.value {
					t.Errorf("Expected %s got %s", local.value, value.AsString())
				}
			})
		}
	})

	t.Run("Valid Variables", func(t *testing.T) {
		input := map[string]cty.Value{
			"str":     cty.StringVal("vara"),
			"str_map": cty.MapVal(map[string]cty.Value{"keyA": cty.StringVal("mapa")}),
		}
		call := NewStaticModuleCall(nil, hcl.Range{}, func(v *Variable) (cty.Value, hcl.Diagnostics) {
			if in, ok := input[v.Name]; ok {
				return in, nil
			}
			return v.Default, nil
		}, "<testing>", "")
		mod, _ := NewModule([]*File{file}, nil, call, "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, call)

		locals := []struct {
			ident string
			value string
		}{
			{"var_str_def_ref", "sane default"},
			{"var_map_def_access", "A"},
			{"var_str_ref", "vara"},
			{"var_map_access", "mapa"},
		}
		for _, local := range locals {
			t.Run(local.ident, func(t *testing.T) {
				value, diags := eval.Evaluate(t.Context(), mod.Locals[local.ident].Expr, dummyIdentifier)
				if diags.HasErrors() {
					t.Error(diags)
				}
				if value.AsString() != local.value {
					t.Errorf("Expected %s got %s", local.value, value.AsString())
				}
			})
		}
	})

	t.Run("Bad References", func(t *testing.T) {
		mod, _ := NewModule([]*File{file}, nil, RootModuleCallForTesting(), "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, RootModuleCallForTesting())

		locals := []struct {
			ident string
			diag  string
		}{
			{"invalid_ref", "eval.tf:37,16-33: Dynamic value in static context; Unable to use invalid.attribute in static context, which is required by local.invalid_ref"},
			{"unavailable_ref", "eval.tf:38,20-27: Dynamic value in static context; Unable to use foo.bar in static context, which is required by local.unavailable_ref"},
			// A bare module reference (every output, unkeyed) refuses with
			// the same summary as a named output: both need the module
			// evaluated, which static evaluation never does. See #191 and
			// the matching case in StaticValidateReferences.
			{"module_whole_ref", "eval.tf:39,21-33: Module output not supported in static context; Unable to use module.child in static context, which is required by local.module_whole_ref"},
			{"module_output_ref", "eval.tf:40,22-38: Module output not supported in static context; Unable to use module.child.out in static context, which is required by local.module_output_ref"},
		}
		for _, local := range locals {
			t.Run(local.ident, func(t *testing.T) {
				badref := mod.Locals[local.ident]
				_, diags := eval.Evaluate(t.Context(), badref.Expr, StaticIdentifier{Subject: fmt.Sprintf("local.%s", badref.Name), DeclRange: badref.DeclRange})
				assertExactDiagnostics(t, diags, []string{local.diag})
			})
		}
	})

	t.Run("Circular References", func(t *testing.T) {
		mod, _ := NewModule([]*File{file}, nil, RootModuleCallForTesting(), "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, RootModuleCallForTesting())

		locals := []struct {
			ident string
			diags []string
		}{
			{"circular", []string{
				"eval.tf:43,2-27: Circular reference; local.circular is self referential",
			}},
			{"circular_ref", []string{
				"eval.tf:43,2-27: Circular reference; local.circular is self referential",
				"eval.tf:44,2-31: Unable to compute static value; local.circular_ref depends on local.circular which is not available",
			}},
			{"circular_a", []string{
				"eval.tf:45,2-31: Unable to compute static value; local.circular_a depends on local.circular_b which is not available",
				"eval.tf:45,2-31: Circular reference; local.circular_a is self referential",
			}},
		}
		for _, local := range locals {
			t.Run(local.ident, func(t *testing.T) {
				badref := mod.Locals[local.ident]
				_, diags := eval.Evaluate(t.Context(), badref.Expr, StaticIdentifier{Subject: fmt.Sprintf("local.%s", badref.Name), DeclRange: badref.DeclRange})
				assertExactDiagnostics(t, diags, local.diags)
			})
		}
	})

	t.Run("Dependency chain", func(t *testing.T) {
		call := NewStaticModuleCall(nil, hcl.Range{}, func(v *Variable) (cty.Value, hcl.Diagnostics) {
			return cty.DynamicVal, hcl.Diagnostics{&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Variable value not provided",
				Detail:   fmt.Sprintf("var.%s not included", v.Name),
				Subject:  v.DeclRange.Ptr(),
			}}
		}, "<testing>", "")
		mod, _ := NewModule([]*File{file}, nil, call, "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, call)

		badref := mod.Locals["ref_c"]
		_, diags := eval.Evaluate(t.Context(), badref.Expr, StaticIdentifier{Subject: fmt.Sprintf("local.%s", badref.Name), DeclRange: badref.DeclRange})
		assertExactDiagnostics(t, diags, []string{
			"eval.tf:2,1-15: Variable value not provided; var.str not included",
			"eval.tf:49,2-17: Unable to compute static value; local.ref_a depends on var.str which is not available",
			"eval.tf:50,2-21: Unable to compute static value; local.ref_b depends on local.ref_a which is not available",
			"eval.tf:51,2-21: Unable to compute static value; local.ref_c depends on local.ref_b which is not available",
		})
	})

	t.Run("Missing References", func(t *testing.T) {
		mod, _ := NewModule([]*File{file}, nil, RootModuleCallForTesting(), "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, RootModuleCallForTesting())

		locals := []struct {
			ident string
			diag  string
		}{
			{"local_missing", "eval.tf:54,18-31: Undefined local; Undefined local local.missing"},
			{"var_missing", "eval.tf:55,16-27: Undefined variable; Undefined variable var.missing"},
		}
		for _, local := range locals {
			t.Run(local.ident, func(t *testing.T) {
				badref := mod.Locals[local.ident]
				_, diags := eval.Evaluate(t.Context(), badref.Expr, StaticIdentifier{Subject: fmt.Sprintf("local.%s", badref.Name), DeclRange: badref.DeclRange})
				assertExactDiagnostics(t, diags, []string{local.diag})
			})
		}
	})

	t.Run("Workspace", func(t *testing.T) {
		call := NewStaticModuleCall(nil, hcl.Range{}, nil, "<testing>", "my-workspace")
		mod, _ := NewModule([]*File{file}, nil, call, "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, call)

		value, diags := eval.Evaluate(t.Context(), mod.Locals["ws"].Expr, dummyIdentifier)
		if diags.HasErrors() {
			t.Error(diags)
		}
		if value.AsString() != "my-workspace" {
			t.Errorf("Expected %s got %s", "my-workspace", value.AsString())
		}
	})

	t.Run("Functions", func(t *testing.T) {
		mod, _ := NewModule([]*File{file}, nil, RootModuleCallForTesting(), "dir", SelectiveLoadAll)
		eval := NewStaticEvaluator(mod, RootModuleCallForTesting())

		value, diags := eval.Evaluate(t.Context(), mod.Locals["func"].Expr, dummyIdentifier)
		if diags.HasErrors() {
			t.Error(diags)
		}
		if value.AsString() != "f887f41a53a46e2d40a3f8f86cacaaa2" {
			t.Errorf("Expected %s got %s", "f887f41a53a46e2d40a3f8f86cacaaa2", value.AsString())
		}

		_, diags = eval.Evaluate(t.Context(), mod.Locals["missing_func"].Expr, StaticIdentifier{Subject: fmt.Sprintf("local.%s", mod.Locals["missing_func"].Name), DeclRange: mod.Locals["missing_func"].DeclRange})
		assertExactDiagnostics(t, diags, []string{`eval.tf:62,17-27: Call to unknown function; There is no function named "missing_fn".`})
		_, diags = eval.Evaluate(t.Context(), mod.Locals["provider_func"].Expr, StaticIdentifier{Subject: fmt.Sprintf("local.%s", mod.Locals["provider_func"].Name), DeclRange: mod.Locals["provider_func"].DeclRange})
		assertExactDiagnostics(t, diags, []string{`eval.tf:63,18-36: Provider function in static context; Unable to use provider::type::fn in static context, which is required by local.provider_func`})
	})
}

func TestStaticEvaluator_DecodeExpression(t *testing.T) {
	dummyIdentifier := StaticIdentifier{Subject: "local.test"}
	parser := testParser(map[string]string{"eval.tf": ""})
	file, fileDiags := parser.LoadConfigFile("eval.tf")
	if fileDiags.HasErrors() {
		t.Fatal(fileDiags)
	}
	mod, _ := NewModule([]*File{file}, nil, RootModuleCallForTesting(), "dir", SelectiveLoadAll)
	mod.Locals["my_ephemeral_local"] = &Local{
		Name:      "my_ephemeral_local",
		Expr:      hcl.StaticExpr(cty.StringVal("ephemeral local value").Mark(marks.Ephemeral), hcl.Range{}),
		DeclRange: hcl.Range{},
	}
	mod.Locals["my_sensitive_local"] = &Local{
		Name:      "my_sensitive_local",
		Expr:      hcl.StaticExpr(cty.StringVal("sensitive local value").Mark(marks.Sensitive), hcl.Range{}),
		DeclRange: hcl.Range{},
	}
	eval := NewStaticEvaluator(mod, RootModuleCallForTesting())
	cases := []struct {
		expr  string
		diags []string
	}{
		{
			expr: `"static"`,
		},
		{
			expr: `count`,
			diags: []string{
				`eval.tf:1,1-6: Invalid reference; The "count" object cannot be accessed directly. Instead, access one of its attributes.`,
				`:0,0-0: Dynamic value in static context; Unable to use count. in static context, which is required by local.test`,
			},
		},
		{
			expr:  `module.foo.bar`,
			diags: []string{`eval.tf:1,1-15: Module output not supported in static context; Unable to use module.foo.bar in static context, which is required by local.test`},
		},
		{
			expr:  `local.my_ephemeral_local`,
			diags: []string{`eval.tf:1,1-25: Ephemeral value not allowed; Ephemeral values, or values derived from ephemeral values, cannot be used as local.test.`},
		},
		{
			expr:  `local.my_sensitive_local`,
			diags: []string{`eval.tf:1,1-25: Sensitive value not allowed; Sensitive values, or values derived from sensitive values, cannot be used as local.test.`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			expr, _ := hclsyntax.ParseExpression([]byte(tc.expr), "eval.tf", hcl.InitialPos)
			var str string
			diags := eval.DecodeExpression(t.Context(), expr, dummyIdentifier, &str)
			assertExactDiagnostics(t, diags, tc.diags)
		})
	}
}
func TestStaticEvaluator_DecodeBlock(t *testing.T) {
	cases := []struct {
		ident string
		body  string
		diags []string
	}{{
		ident: "valid",
		body: `
locals {
	static = "static"
}

terraform {
	backend "valid" {
		thing = local.static
	}
}`,
	}, {
		ident: "badref",
		body: `
terraform {
	backend "badref" {
		thing = count
	}
}`,
		diags: []string{`eval.tf:4,11-16: Invalid reference; The "count" object cannot be accessed directly. Instead, access one of its attributes.`},
	}, {
		ident: "badeval",
		body: `
terraform {
	backend "badeval" {
		thing = module.foo.bar
	}
}`,
		diags: []string{`eval.tf:4,11-25: Module output not supported in static context; Unable to use module.foo.bar in static context, which is required by backend.badeval`},
	}, {
		ident: "sensitive",
		body: `
locals {
	sens = sensitive("magic")
}
terraform {
	backend "sensitive" {
		thing = local.sens
	}
}`,
		diags: []string{`eval.tf:6,2-21: Backend config contains sensitive values; The backend configuration is stored in .terraform/terraform.tfstate as well as plan files. It is recommended to instead supply sensitive credentials via backend specific environment variables`},
	}, {
		ident: "ephemeral",
		body: `
variable "backend_cfg" {
	ephemeral = true
    default = "foo"
}
terraform {
	backend "my_backend" {
		thing = var.backend_cfg
	}
}`,
		diags: []string{`eval.tf:7,2-22: Backend config contains ephemeral values; The backend configuration is stored in .terraform/terraform.tfstate as well as plan files. It is recommended to instead supply ephemeral credentials via backend specific environment variables`},
	}}

	schema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"thing": {
				Type: cty.String,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.ident, func(t *testing.T) {
			parser := testParser(map[string]string{"eval.tf": tc.body})
			file, fileDiags := parser.LoadConfigFile("eval.tf")
			if fileDiags.HasErrors() {
				t.Fatal(fileDiags)
			}

			modCall := StaticModuleCall{
				vars: func(v *Variable) (cty.Value, hcl.Diagnostics) {
					return v.Default, nil
				},
			}
			mod, _ := NewModule([]*File{file}, nil, modCall, "dir", SelectiveLoadAll)

			_, diags := mod.Backend.Hash(t.Context(), schema)
			if diags.HasErrors() {
				assertExactDiagnostics(t, diags, tc.diags)
				return
			}

			_, diags = mod.Backend.Decode(t.Context(), schema)

			assertExactDiagnostics(t, diags, tc.diags)
		})
	}
}

// TestStaticEvaluator_baseDir exercises [StaticEvaluator.baseDir] directly:
// the value file(), fileexists(), fileset(), the filehash functions,
// templatefile() and templatestring() resolve a bare relative path against
// (issue #205). See that method's doc for why rootPath, not cfg.SourceDir
// or the literal ".", is the right answer for every real caller.
func TestStaticEvaluator_baseDir(t *testing.T) {
	cases := []struct {
		name string
		eval *StaticEvaluator
		want string
	}{
		{
			name: "nil evaluator",
			eval: nil,
			want: ".",
		},
		{
			name: "rootPath set - the ordinary case for every real construction site",
			eval: &StaticEvaluator{
				call: NewStaticModuleCall(nil, hcl.Range{}, nil, "deployments/github", ""),
				cfg:  &Module{SourceDir: "deployments/github"},
			},
			want: "deployments/github",
		},
		{
			name: "rootPath empty, cfg.SourceDir set - a zero-value StaticModuleCall{} some configload tests build directly",
			eval: &StaticEvaluator{
				call: StaticModuleCall{},
				cfg:  &Module{SourceDir: "some/dir"},
			},
			want: "some/dir",
		},
		{
			name: "rootPath empty, cfg nil",
			eval: &StaticEvaluator{
				call: StaticModuleCall{},
			},
			want: ".",
		},
		{
			name: "rootPath empty, cfg.SourceDir also empty",
			eval: &StaticEvaluator{
				call: StaticModuleCall{},
				cfg:  &Module{SourceDir: ""},
			},
			want: ".",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.eval.baseDir(); got != tc.want {
				t.Errorf("baseDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStaticEvaluator_modulePath exercises [StaticEvaluator.modulePath]
// directly: path.module's value. It must stay relative to call.rootPath -
// the same anchor baseDir() resolves bare paths against - or every
// "${path.module}/x"-style file()/templatefile() call at the root doubles
// (see modulePath's own doc, and
// TestStaticEvaluator_PathModulePrefixedFileDoesNotDouble below for the
// end-to-end proof).
func TestStaticEvaluator_modulePath(t *testing.T) {
	cases := []struct {
		name string
		eval *StaticEvaluator
		want string
	}{
		{
			name: "nil evaluator",
			eval: nil,
			want: "",
		},
		{
			name: "nil cfg",
			eval: &StaticEvaluator{call: NewStaticModuleCall(nil, hcl.Range{}, nil, "dir", "")},
			want: "",
		},
		{
			name: "root module: SourceDir equals rootPath, so the relative value is '.'",
			eval: &StaticEvaluator{
				call: NewStaticModuleCall(nil, hcl.Range{}, nil, "deployments/github", ""),
				cfg:  &Module{SourceDir: "deployments/github"},
			},
			want: ".",
		},
		{
			name: "descendant module: SourceDir carries rootPath as a real prefix, chained the same way internal/configs/config_build.go and internal/live/check/load.go compute it",
			eval: &StaticEvaluator{
				call: NewStaticModuleCall(nil, hcl.Range{}, nil, "deployments/cluster-services", ""),
				cfg:  &Module{SourceDir: "deployments/cluster-services/modules/gatekeeper"},
			},
			want: "modules/gatekeeper",
		},
		{
			name: "absolute SourceDir and rootPath (internal/command's usual case) behave the same as the relative form",
			eval: &StaticEvaluator{
				call: NewStaticModuleCall(nil, hcl.Range{}, nil, "/repo/deployments/github", ""),
				cfg:  &Module{SourceDir: "/repo/deployments/github"},
			},
			want: ".",
		},
		{
			name: "rootPath empty falls back to cfg.SourceDir unchanged - this method's pre-fix behaviour",
			eval: &StaticEvaluator{
				call: StaticModuleCall{},
				cfg:  &Module{SourceDir: "some/dir"},
			},
			want: "some/dir",
		},
		{
			name: "SourceDir cannot be expressed relative to rootPath (mismatched absolute/relative-ness) falls back to cfg.SourceDir unchanged",
			eval: &StaticEvaluator{
				call: NewStaticModuleCall(nil, hcl.Range{}, nil, "/repo/deployments/github", ""),
				cfg:  &Module{SourceDir: "deployments/github"},
			},
			want: "deployments/github",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.eval.modulePath(); got != tc.want {
				t.Errorf("modulePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// writeRealModule plants a real .tf file (plus whatever other real files
// the caller wants) under dir, on the actual OS filesystem - file() and its
// relatives read through os.Open, never through the in-memory afero
// filesystem [testParser] uses for ordinary parsing tests, so an
// end-to-end test of file() resolution needs a real directory regardless
// of how the .tf content itself gets loaded.
func writeRealModule(t *testing.T, dir, tf string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStaticEvaluator_BareFileResolvesAgainstEntryDirectory reproduces
// issue #205 end to end: a root module whose bare file("x") call worked
// under real `terraform plan` (verified independently against terraform
// 1.15.8) but failed in this fork with "Invalid function argument" because
// [newStaticScope] resolved BaseDir against the process's actual working
// directory - the repo root corpus-gen and live-check run from - rather
// than the directory the module was actually loaded from.
//
// The govuk-infrastructure config this bug came from is
// "deployments/github/main.tf": locals { repositories =
// yamldecode(file("repos.yml"))["repos"] }. This test reproduces the same
// shape: a process whose actual cwd is nowhere near the module (simulated
// with t.Chdir to a directory one level up), loading the module by a
// relative path exactly as tools/corpus-gen's entry.Dir and
// internal/command/live_check.go's directory argument both are.
func TestStaticEvaluator_BareFileResolvesAgainstEntryDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	entryDir := filepath.Join(repoRoot, "deployments", "github")
	writeRealModule(t, entryDir,
		`locals {
  repositories = file("repos.yml")
}
`,
		map[string]string{"repos.yml": "repos: [a, b, c]\n"})

	t.Chdir(repoRoot)
	relDir := filepath.Join("deployments", "github")

	parser := NewParser(nil)
	rootCall := NewStaticModuleCall(nil, hcl.Range{}, nil, relDir, "")
	mod, diags := parser.LoadConfigDir(relDir, rootCall)
	if diags.HasErrors() {
		t.Fatalf("load: %s", diags)
	}

	ident := StaticIdentifier{Subject: "local.repositories"}
	val, evalDiags := mod.StaticEvaluator.Evaluate(t.Context(), mod.Locals["repositories"].Expr, ident)
	if evalDiags.HasErrors() {
		t.Fatalf("bare file() call failed to resolve against the module's own directory: %s", evalDiags)
	}
	if got, want := val.AsString(), "repos: [a, b, c]\n"; got != want {
		t.Errorf("file() returned %q, want %q", got, want)
	}
}

// TestStaticEvaluator_PathModulePrefixedFileDoesNotDouble guards the
// correction to the first version of the #205 fix: making BaseDir the real
// entry directory (call.rootPath) without also making path.module relative
// to that same anchor doubles the base for every
// "${path.module}/x"-style file()/templatefile() call at the root -
// file("${path.module}/x") becomes Join(rootPath, rootPath+"/x"). That
// pattern is far more common across real configurations (dozens of sites
// in this fork's own corpus) than the bare form issue #205 reported,
// because module authors reach for "${path.module}/x" specifically to make
// file() resolution reliable.
//
// This test proves the failure mode is not merely "loud instead of quiet":
// it plants a file at both the correct, single-joined location and the
// wrong, doubled one, with different content, and asserts file() reads the
// correct one. Without modulePath()'s fix, this test reads "doubled"
// instead of "single" - a silently wrong file, not a refusal - which is
// exactly the risk flagged for this change: "A file() that now resolves
// could read the wrong file if the base is right for one caller and wrong
// for another."
func TestStaticEvaluator_PathModulePrefixedFileDoesNotDouble(t *testing.T) {
	repoRoot := t.TempDir()
	entryDir := filepath.Join(repoRoot, "deployments", "github")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "data.txt"), []byte("single"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The doubled path: entryDir/entryDir/data.txt (Join(rootPath,
	// rootPath+"/data.txt") when rootPath is "deployments/github").
	doubled := filepath.Join(entryDir, "deployments", "github")
	if err := os.MkdirAll(doubled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(doubled, "data.txt"), []byte("doubled"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRealModule(t, entryDir,
		`locals {
  bare      = file("data.txt")
  prefixed  = file("${path.module}/data.txt")
}
`,
		nil)

	t.Chdir(repoRoot)
	relDir := filepath.Join("deployments", "github")

	parser := NewParser(nil)
	rootCall := NewStaticModuleCall(nil, hcl.Range{}, nil, relDir, "")
	mod, diags := parser.LoadConfigDir(relDir, rootCall)
	if diags.HasErrors() {
		t.Fatalf("load: %s", diags)
	}

	for _, ident := range []string{"bare", "prefixed"} {
		t.Run(ident, func(t *testing.T) {
			id := StaticIdentifier{Subject: "local." + ident}
			val, evalDiags := mod.StaticEvaluator.Evaluate(t.Context(), mod.Locals[ident].Expr, id)
			if evalDiags.HasErrors() {
				t.Fatalf("file() failed: %s", evalDiags)
			}
			if got, want := val.AsString(), "single"; got != want {
				t.Errorf("file() read %q, want %q (reading %q means the base directory got doubled and a DIFFERENT real file was silently read)", got, want, "doubled")
			}
		})
	}
}

// TestStaticEvaluator_ChildModuleFileResolution proves the fix generalizes
// past the root module, matching stock OpenTofu's own (verified) behaviour
// of resolving every module's bare file() call against the SAME base
// directory - the root's - rather than the calling module's own directory,
// while a "${path.module}/x"-prefixed call in the same child correctly
// reaches the child's own directory.
//
// Verified against real `terraform plan` 1.15.8: a child module's bare
// file("data.txt") only succeeds when that file sits next to the ROOT
// module's own config (not the child's), and file("child/data.txt") from
// the root succeeds - i.e. BaseDir is constant across the whole module
// tree in stock OpenTofu, never varying per calling module.
func TestStaticEvaluator_ChildModuleFileResolution(t *testing.T) {
	repoRoot := t.TempDir()
	entryDir := filepath.Join(repoRoot, "deployments", "cluster-services")
	childDir := filepath.Join(entryDir, "modules", "gatekeeper")

	// A file that only a BARE file() call resolved against the ROOT's own
	// directory (not the child's) can reach - mirrors stock's verified
	// behaviour for a child module's unprefixed file() call.
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "root-only.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRealModule(t, childDir,
		`locals {
  bare      = file("root-only.txt")
  prefixed  = file("${path.module}/child-only.txt")
}
`,
		map[string]string{"child-only.txt": "child"})

	t.Chdir(repoRoot)
	rootRel := filepath.Join("deployments", "cluster-services")
	childRel := filepath.Join(rootRel, "modules", "gatekeeper")

	// call.rootPath is the SAME real directory for both the root and the
	// child - internal/configs/config_build.go's buildChildModules always
	// propagates parent.Root.Module.SourceDir, never the child's own
	// directory, as every descendant's rootPath.
	childCall := NewStaticModuleCall(nil, hcl.Range{}, nil, rootRel, "")

	parser := NewParser(nil)
	mod, diags := parser.LoadConfigDir(childRel, childCall)
	if diags.HasErrors() {
		t.Fatalf("load: %s", diags)
	}

	t.Run("bare file() reaches the root's directory, matching stock", func(t *testing.T) {
		ident := StaticIdentifier{Subject: "local.bare"}
		val, evalDiags := mod.StaticEvaluator.Evaluate(t.Context(), mod.Locals["bare"].Expr, ident)
		if evalDiags.HasErrors() {
			t.Fatalf("file() failed: %s", evalDiags)
		}
		if got, want := val.AsString(), "root"; got != want {
			t.Errorf("file() returned %q, want %q", got, want)
		}
	})

	t.Run("${path.module}-prefixed file() reaches the child's own directory", func(t *testing.T) {
		ident := StaticIdentifier{Subject: "local.prefixed"}
		val, evalDiags := mod.StaticEvaluator.Evaluate(t.Context(), mod.Locals["prefixed"].Expr, ident)
		if evalDiags.HasErrors() {
			t.Fatalf("file() failed: %s", evalDiags)
		}
		if got, want := val.AsString(), "child"; got != want {
			t.Errorf("file() returned %q, want %q", got, want)
		}
	})
}
