// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	goversion "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	ctyconvert "github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/modsdir"
)

// Load builds a configuration tree from one directory without installing
// anything and without contacting anything.
//
// It exists because both front ends must work on a directory that has never
// heard of this fork: #114's whole point is an evaluator pointing the
// command at a repo they may not have written, and #102's corpus is other
// people's configurations. Requiring "tofu init" first would make the
// instrument measure only the configurations someone had already committed
// to enough to install.
//
// Module sources are resolved in the two ways that need no network:
//
//   - A local source ("./modules/vpc") is read straight off disk.
//   - Any other source is read from .terraform/modules, if a previous init
//     left a manifest there.
//
// A module that neither resolves is recorded in
// [LoadResult.UnresolvedModules] and skipped, and the walk continues. That
// is #102's third option for registry modules, chosen because it is the
// honest one: a configuration whose modules cannot be read without a
// network is a real onboarding cost, and a measurement that discarded those
// configurations would report a subset that had already passed the hardest
// step. The cost of the choice is that a skipped module's contents are
// unmeasured, which every report states.
func Load(ctx context.Context, dir string) LoadResult {
	var result LoadResult

	vars := newVariableValues(dir)
	result.vars = vars
	parser := configs.NewParser(nil)

	rootCall := configs.NewStaticModuleCall(addrs.RootModule, hcl.Range{}, vars.value, dir, defaultWorkspace)
	rootMod, diags := parser.LoadConfigDir(dir, rootCall)
	if diags.HasErrors() {
		result.Diags = diags
		return result
	}

	manifest := readModuleManifest(dir)

	walker := configs.ModuleWalkerFunc(func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *goversion.Version, hcl.Diagnostics) {
		childDir, ok := result.resolveModuleDir(dir, manifest, req)
		if !ok {
			// nil with no error diagnostic: configs.buildChildModules
			// skips the child and keeps walking, which is what makes an
			// uninstalled module a measured outcome rather than a dead
			// run. The skip is reported through UnresolvedModules.
			return nil, nil, nil
		}
		mod, modDiags := parser.LoadConfigDir(childDir, req.Call)
		return mod, nil, modDiags
	})

	cfg, buildDiags := configs.BuildConfig(ctx, rootMod, walker)
	result.Diags = append(result.Diags, buildDiags...)
	if buildDiags.HasErrors() {
		return result
	}
	result.Config = cfg
	return result
}

// LoadResult is what one directory produced.
type LoadResult struct {
	// Config is the configuration tree, or nil when loading failed. A nil
	// Config with diagnostics is an ordinary outcome for the corpus: a
	// configuration written for a Terraform version this fork's parser
	// does not accept is a measurement, not a crash.
	Config *configs.Config

	// Diags are the parse and build diagnostics. Errors here mean Config is
	// nil.
	Diags hcl.Diagnostics

	// UnresolvedModules are the module calls that could not be read without
	// installing them, sorted by module address. Their contents are absent
	// from every count in the report that follows.
	UnresolvedModules []UnresolvedModule

	// vars is the tracker behind [LoadResult.UnsetVariables]. It is kept
	// rather than snapshotted because the static evaluator is lazy: most
	// variables are first read during identity resolution, which happens
	// after this struct is returned. An earlier version of this recorded
	// the names at the end of Load and reported none, which is the
	// quietest possible way to lose the caveat that matters most.
	vars *variableValues
}

// UnsetVariables are the required root input variables no value was found
// for, sorted.
//
// Each was evaluated as an unknown value, so an expression depending on one
// is not statically evaluable, and the refusals that fire on it may be an
// artifact of the missing value rather than of the configuration. Reports
// say so rather than silently ranking them.
//
// It is a method rather than a field because it is only complete once the
// analysis has run: see the vars field.
func (r LoadResult) UnsetVariables() []string {
	if r.vars == nil {
		return nil
	}
	return r.vars.unset()
}

// UnresolvedModule is one module call this loader could not read.
type UnresolvedModule struct {
	// Path is the module call's address, as "vpc" or "vpc.subnets".
	Path string

	// Source is the source address as written.
	Source string

	// Reason says which of the two resolutions failed, in a form a report
	// can print without rewording.
	Reason string
}

// resolveModuleDir finds the directory holding one module call's source, by
// the two means that need no network. See [Load].
func (r *LoadResult) resolveModuleDir(rootDir string, manifest modsdir.Manifest, req *configs.ModuleRequest) (string, bool) {
	path := strings.Join(req.Path, ".")

	if local, ok := req.SourceAddr.(addrs.ModuleSourceLocal); ok {
		dir := filepath.Join(req.Parent.Module.SourceDir, string(local))
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
		r.unresolved(path, req.SourceAddr.String(), "the directory does not exist")
		return "", false
	}

	// Not a local source, so it has to have been installed. The manifest an
	// earlier "tofu init" wrote is the only record of where, and its keys
	// are the same dotted module addresses used above.
	if record, ok := manifest[path]; ok && record.Dir != "" {
		dir := record.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(rootDir, dir)
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
	}

	r.unresolved(path, req.SourceAddr.String(), "not installed; run \"tofu init\" to fetch it")
	return "", false
}

func (r *LoadResult) unresolved(path, source, reason string) {
	r.UnresolvedModules = append(r.UnresolvedModules, UnresolvedModule{
		Path:   path,
		Source: source,
		Reason: reason,
	})
	sort.Slice(r.UnresolvedModules, func(i, j int) bool {
		return r.UnresolvedModules[i].Path < r.UnresolvedModules[j].Path
	})
}

// readModuleManifest reads what a previous init recorded about where it put
// each non-local module. A directory that was never initialized has none,
// which is the ordinary case here and not an error.
func readModuleManifest(dir string) modsdir.Manifest {
	manifest, err := modsdir.ReadManifestSnapshotForDir(filepath.Join(dir, ".terraform", "modules"))
	if err != nil {
		return make(modsdir.Manifest)
	}
	return manifest
}

const defaultWorkspace = "default"

// variableValues answers the static evaluator's questions about root input
// variables, from the sources that are readable without asking anyone: the
// declared default, then the ordinary variables files sitting in the
// directory.
//
// A required variable with no value becomes an unknown of its declared
// type, and its name is recorded. Refusing the run instead would be wrong -
// most real configurations take values from CI or a .tfvars file that is
// not in the repo - and substituting a placeholder string would be worse,
// because it would make expressions look statically evaluable that are not.
// Unknown is the honest answer, and the recorded name is what lets a report
// say which refusals might be an artifact of it.
type variableValues struct {
	mu        sync.Mutex
	fromFiles map[string]cty.Value
	missing   map[string]bool
}

func newVariableValues(dir string) *variableValues {
	return &variableValues{
		fromFiles: readVarsFiles(dir),
		missing:   make(map[string]bool),
	}
}

func (v *variableValues) value(variable *configs.Variable) (cty.Value, hcl.Diagnostics) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if val, ok := v.fromFiles[variable.Name]; ok {
		if variable.ConstraintType != cty.NilType {
			converted, err := ctyconvert.Convert(val, variable.ConstraintType)
			if err == nil {
				return converted, nil
			}
			// A value that does not fit the declared type is the
			// configuration's own problem, reported by the ordinary
			// validate path rather than invented here. Fall through to
			// unknown so this pass measures the configuration instead of
			// the tfvars file.
		} else {
			return val, nil
		}
	}

	if variable.Default != cty.NilVal {
		return variable.Default, nil
	}

	v.missing[variable.Name] = true
	if variable.ConstraintType != cty.NilType {
		return cty.UnknownVal(variable.ConstraintType), nil
	}
	return cty.DynamicVal, nil
}

func (v *variableValues) unset() []string {
	v.mu.Lock()
	defer v.mu.Unlock()

	out := make([]string, 0, len(v.missing))
	for name := range v.missing {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// readVarsFiles reads terraform.tfvars, terraform.tfvars.json and every
// *.auto.tfvars(.json) in the directory, in the precedence tofu itself
// applies.
//
// Command-line -var and -var-file are not read here: this loader is shared
// by a corpus runner that has no command line to read them from. The
// command front end that does accept them is free to pass them, and until
// it does, a value given only on the command line reads as unset - which
// [variableValues] records rather than hides.
func readVarsFiles(dir string) map[string]cty.Value {
	out := make(map[string]cty.Value)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}

	var autoFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".auto.tfvars") || strings.HasSuffix(name, ".auto.tfvars.json") {
			autoFiles = append(autoFiles, name)
		}
	}
	sort.Strings(autoFiles)

	// Lowest precedence first, so later reads overwrite earlier ones.
	for _, name := range append([]string{"terraform.tfvars", "terraform.tfvars.json"}, autoFiles...) {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		for key, val := range readVarsFile(path) {
			out[key] = val
		}
	}
	return out
}

// readVarsFile reads one variables file. Anything unreadable is skipped in
// silence: a corpus entry with a malformed tfvars file still has a
// configuration worth measuring, and the ordinary validate path is where a
// user hears about the file itself.
func readVarsFile(path string) map[string]cty.Value {
	out := make(map[string]cty.Value)

	src, err := os.ReadFile(path)
	if err != nil {
		return out
	}

	parser := hclparse.NewParser()
	var file *hcl.File
	var diags hcl.Diagnostics
	if strings.HasSuffix(path, ".json") {
		file, diags = parser.ParseJSON(src, path)
	} else {
		file, diags = parser.ParseHCL(src, path)
	}
	if diags.HasErrors() || file == nil {
		return out
	}

	attrs, attrDiags := file.Body.JustAttributes()
	if attrDiags.HasErrors() && len(attrs) == 0 {
		return out
	}
	for name, attr := range attrs {
		val, valDiags := attr.Expr.Value(nil)
		if valDiags.HasErrors() {
			continue
		}
		out[name] = val
	}
	return out
}
