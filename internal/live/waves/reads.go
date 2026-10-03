// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/modsdir"
)

// LoadRoot reads the configuration in dir, the whole static module tree,
// and returns the estate it owns and the estates it reads. name is what
// the result's Root is set to: the directory as the set names it.
//
// It needs no "init" for a module called by a local path: that module is
// read from the path. Any other module is read from where "init" installed
// it (dir/.terraform/modules), and a module that is not installed is an
// error naming it, since a read inside it could not be seen.
//
// The estate is the live block's (or estate.chdf.hcl's) estate argument,
// and otherwise the one tofu-estate value the configuration stamps; a
// configuration that names none, or several, is an error.
func LoadRoot(ctx context.Context, name, dir string) (Root, error) {
	cfg, err := loadTree(ctx, dir)
	if err != nil {
		return Root{}, fmt.Errorf("root %s: %w", name, err)
	}
	estate := ""
	if cfg.Module.Live != nil && cfg.Module.Live.Estate != "" {
		estate = cfg.Module.Live.Estate
	} else {
		declared := discovery.DeclaredEstateNames(ctx, cfg)
		switch len(declared) {
		case 1:
			estate = declared[0]
		case 0:
			return Root{}, fmt.Errorf("root %s names no estate: declare one in a live block or an estate.chdf.hcl sidecar", name)
		default:
			return Root{}, fmt.Errorf("root %s stamps %d estates (%s): declare the one it owns in a live block or an estate.chdf.hcl sidecar", name, len(declared), strings.Join(declared, ", "))
		}
	}
	reads, err := ReadsOf(cfg)
	if err != nil {
		return Root{}, fmt.Errorf("root %s: %w", name, err)
	}
	return Root{Root: name, Estate: estate, Reads: reads}, nil
}

// loadTree builds dir's static module tree without a provider and without
// an init for local modules. A root input variable with no value is an
// error only where something static uses it, as in every other static
// reading of a configuration here.
func loadTree(ctx context.Context, dir string) (*configs.Config, error) {
	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if v.Required() {
				return cty.NilVal, hcl.Diagnostics{{
					Severity: hcl.DiagError,
					Summary:  "No value for required variable",
					Detail:   fmt.Sprintf("The root module input variable %q has no default, and wave planning reads configuration without variable values.", v.Name),
					Subject:  v.DeclRange.Ptr(),
				}}
			}
			return v.Default, nil
		},
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("the configuration does not load: %s", diags.Error())
	}
	if mod == nil {
		return nil, fmt.Errorf("%s holds no configuration", dir)
	}

	manifest, err := modsdir.ReadManifestSnapshotForDir(filepath.Join(dir, ".terraform", "modules"))
	if err != nil {
		return nil, fmt.Errorf("reading the installed modules: %w", err)
	}

	dirs := map[string]string{"": dir}
	cfg, cfgDiags := configs.BuildConfig(ctx, mod, call, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			var where string
			if rec, ok := manifest[manifest.ModuleKey(req.Path)]; ok && rec.Dir != "" {
				where = rec.Dir
				if !filepath.IsAbs(where) {
					where = filepath.Join(dir, where)
				}
			} else if local, ok := req.SourceAddr.(addrs.ModuleSourceLocal); ok {
				where = filepath.Join(dirs[req.Parent.Path.String()], string(local))
			} else {
				return nil, nil, hcl.Diagnostics{{
					Severity: hcl.DiagError,
					Summary:  "Module not installed",
					Detail:   fmt.Sprintf("Module %q is not called by a local path and is not installed, so a cross-estate read inside it cannot be seen. Run \"choudoufu init\" in this root first.", req.Path.String()),
					Subject:  &req.CallRange,
				}}
			}
			dirs[req.Path.String()] = where
			child, modDiags := parser.LoadConfigDir(where)
			return child, nil, modDiags
		}, parser.LoadSymbolFilesInDir,
	))
	if cfgDiags.HasErrors() {
		return nil, fmt.Errorf("the configuration does not load: %s", cfgDiags.Error())
	}
	return cfg, nil
}

// ReadsOf finds every estate cfg's configuration reads, over the whole
// static module tree, sorted. A read is:
//
//   - a data source with a "filter" block named tag:tofu-estate, once per
//     value (internal/live/check's own reading of live/OUTPUTS.md's
//     pattern);
//   - a data source whose "tags" argument is an object with a tofu-estate
//     key;
//   - a terraform_estate_outputs data source's "estate" argument (#1371).
//
// A read whose estate is not a literal is an error naming it rather than a
// read silently missed: a missed read could land a reader before what it
// reads.
func ReadsOf(cfg *configs.Config) ([]Read, error) {
	var out []Read
	if err := readsInModule(cfg, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Estate < out[j].Estate
	})
	return out, nil
}

func readsInModule(cfg *configs.Config, out *[]Read) error {
	if cfg == nil || cfg.Module == nil {
		return nil
	}
	mod := cfg.Module
	names := make([]string, 0, len(mod.DataResources))
	for name := range mod.DataResources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rc := mod.DataResources[name]
		from := rc.Addr().String()
		if !cfg.Path.IsRoot() {
			from = cfg.Path.String() + "." + from
		}
		estates, err := dataSourceReads(rc)
		if err != nil {
			return fmt.Errorf("%s: %w", from, err)
		}
		for _, e := range estates {
			*out = append(*out, Read{Estate: e, From: from})
		}
	}
	children := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		children = append(children, name)
	}
	sort.Strings(children)
	for _, name := range children {
		if err := readsInModule(cfg.Children[name], out); err != nil {
			return err
		}
	}
	return nil
}

// dataSourceReads is the estates one data source reads.
func dataSourceReads(rc *configs.Resource) ([]string, error) {
	var estates []string
	if rc.Type == tf.EstateOutputsTypeName {
		content, _, _ := rc.Config.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "estate"}},
		})
		attr, ok := content.Attributes["estate"]
		if !ok {
			return nil, nil
		}
		e, ok := literalString(attr.Expr)
		if !ok {
			return nil, fmt.Errorf("its estate argument is not a literal string, so which estate it reads cannot be told from configuration")
		}
		return []string{e}, nil
	}

	content, _, _ := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "tags"}},
		Blocks:     []hcl.BlockHeaderSchema{{Type: "filter"}},
	})
	for _, block := range content.Blocks {
		name, values := check.StaticFilter(block.Body)
		if name != "tag:"+markers.TagEstate {
			continue
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("its tag:%s filter has no literal values, so which estate it reads cannot be told from configuration", markers.TagEstate)
		}
		estates = append(estates, values...)
	}
	if attr, ok := content.Attributes["tags"]; ok {
		obj, isObj := attr.Expr.(*hclsyntax.ObjectConsExpr)
		if isObj {
			for _, item := range obj.Items {
				key, ok := literalString(item.KeyExpr)
				if !ok || key != markers.TagEstate {
					continue
				}
				e, ok := literalString(item.ValueExpr)
				if !ok {
					return nil, fmt.Errorf("its tags argument's %s value is not a literal string, so which estate it reads cannot be told from configuration", markers.TagEstate)
				}
				estates = append(estates, e)
			}
		}
	}
	return estates, nil
}

// literalString evaluates expr with no context and reports its value when
// that is a known, unmarked string. An object key written as a bare word
// is a literal here, the way HCL itself reads it.
func literalString(expr hcl.Expression) (string, bool) {
	if k, ok := expr.(*hclsyntax.ObjectConsKeyExpr); ok {
		if kw := hcl.ExprAsKeyword(k.Wrapped); kw != "" && !k.ForceNonLiteral {
			return kw, true
		}
		expr = k.Wrapped
	}
	v, diags := expr.Value(nil)
	if diags.HasErrors() || v.IsNull() || !v.IsKnown() || v.IsMarked() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}
