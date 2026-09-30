// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// GitHub issue #1709, part of the #1579 epic's second round. The epic's
// Done-when: "No provider-name string check or named-substrate field picks
// a code path outside internal/live/substrate, except sites listed with a
// reason." This guard is the string-check half of that sentence: it scans
// internal/ and tools/ (this package's own directory excluded, since it is
// the sanctioned home) for the syntactic shapes a provider-name literal
// takes when it decides something -
//
//   - a comparison (== or !=) against "aws", "kubernetes", "hashicorp/aws"
//     or "hashicorp/kubernetes";
//   - strings.HasPrefix/TrimPrefix/HasSuffix/TrimSuffix/Contains against
//     "aws_" or "kubernetes_";
//   - an expression-switch case naming one of the four literals above; and
//   - a map key literal naming one of them (a dispatch table keyed by
//     family, which is a comparison in another shape) -
//
// and fails on any site that is not named, with a reason, in
// providerStringAllowed or providerStringPendingRound2.
//
// It does not chase the field-naming half of the same sentence
// (discovery.Result.KubernetesAddressBound and its siblings, #1705's
// concern, since renamed to capability names): those are struct fields
// read as capability flags, never a string literal, and no syntactic
// rule here would tell one apart from an ordinary field read without
// type information this scanner deliberately does not carry (see
// measureSurfaceSeams's own doc comment in seams_test.go for the same
// trade: syntax only, so it costs about a second, at the price of over-approximating and never under-approximating
// what it cannot see at all - here, under-approximating in the other
// direction, by design: it does not claim the field-naming half).
//
// It reads source with go/parser alone: no type checking and no build. A
// site's "scope" is the enclosing top-level function (File:Func or
// File:Recv.Func) or, for a package-level var/const whose value expression
// contains the literal directly (a dispatch table, not a function body),
// File:the declared name.

// providerStringRoots are the directories scanned, relative to the module
// root.
var providerStringRoots = []string{"internal", "tools"}

// providerStringExcludedDirs are subtrees the walk skips outright,
// relative to the module root. internal/live/substrate is the seam this
// guard exists to keep everything else off of; a provider-name check
// there is the contract working, not a hole in it.
var providerStringExcludedDirs = map[string]bool{
	"internal/live/substrate": true,

	// internal/backend is OpenTofu's own state-backend registry, unmodified
	// by this fork. Its "kubernetes" key names a Terraform state backend
	// type, an upstream concept unrelated to this fork's live substrates,
	// and out of scope for a guard about this fork's own dispatch.
	"internal/backend": true,
}

// substrateLiterals are the whole-string spellings of a substrate identity
// that a comparison, switch case or map key can name.
var substrateLiterals = map[string]bool{
	"aws":                  true,
	"kubernetes":           true,
	"hashicorp/aws":        true,
	"hashicorp/kubernetes": true,
}

// substratePrefixLiterals are the type-name-prefix spellings a
// strings.HasPrefix family call can test against, standing in for "this
// type belongs to that provider" without ever naming the provider itself.
var substratePrefixLiterals = map[string]bool{
	"aws_":        true,
	"kubernetes_": true,
}

// providerStringException records one site's justification. Handles is
// not tracked the way surfaceSeamExemption tracks Handles: this guard is a
// yes/no per site, not a partial-coverage measurement.
type providerStringException struct {
	Why string
}

// providerStringAllowed is keyed by "<file>:<scope>" for one site, or by
// "<file>" alone to excuse every site the guard finds in that file. The
// unit is the file or the function, never the package, the same rule
// surfaceSeamExemptions follows and for the same reason.
//
// Seeded 2026-09-27 from the #1579 round-2 close-out audit (issue #1709)
// plus this guard's own first real run, which is the only trustworthy way
// to know every site it would otherwise fail on (a hand-typed list drifts
// from the scanner's actual reach the day either one changes).
var providerStringAllowed = map[string]providerStringException{
	// --- the audit's five named categories ---

	// identity/parent.go aws_ affinity.
	"internal/live/identity/parent.go:tfServicePrefix": {Why: "the Terraform-prefix affinity fallback (issue #129) for when no ServiceOf is supplied: every logical type is non-aws_ by construction, so this is not a substrate dispatch, it is the one convention (\"aws_<service>_...\") the fallback can read at all"},

	// discovery AWS-leg default adopters.
	"internal/live/discovery/discovery.go:defaultAdopterSiblings":             {Why: "the aws_default_* adoption pairing (issue #302); the AWS provider's own naming convention for a default-object adopter, read only from the AWS discovery leg per the file-level exemption already recorded for this file in seams_test.go"},
	"internal/live/discovery/discovery.go:typeNeedsResourceObjectToRecompose": {Why: "calls defaultAdopterPlainSibling/defaultAdopterSiblings for the same aws_default_* convention; the AWS discovery leg's own recompose rule"},

	// live_provider_version.go.
	"internal/command/live_provider_version.go:Meta.resolvedAWSProviderVersion": {Why: "issue #63's version-skew warning is asked once per run of a fork that, at this call, has admitted only the AWS provider into the version table; the AWS lock-file entry is what the warning is about, by name, and is not a dispatch between substrates"},

	// record-store kind switches.
	"internal/configs/live.go:decodeRecordStoreKubernetes":          {Why: "record_store has exactly two kinds today (bucket, kubernetes); this decodes the Kubernetes kind's own arguments and refuses them on the other, the same way its bucket-kind counterpart refuses bucket/region on this one"},
	"internal/configs/live.go:decodeRecordStoreBlock":               {Why: "the record_store block's kind switch (\"local\", \"s3\", \"kubernetes\"); record stores are not a Substrate concept, they are their own small enum with no AWS/Kubernetes symmetry to keep behind the seam"},
	"internal/configs/live.go:RecordStoreInsecureSettingsFor":       {Why: "names which settings allow_insecure may waive per record_store kind; same small enum as decodeRecordStoreBlock's, not a Substrate dispatch"},
	"internal/live/projection/store.go:newRecordStore":              {Why: "the record_store kind switch's projection-side counterpart; same enum as configs/live.go's, not a Substrate dispatch"},
	"internal/command/live_cluster.go:liveClusterSubject":           {Why: "reports which record_store \"kubernetes\" block (if any) supplies the connection this run inspects; record stores are their own enum, not a Substrate dispatch (see configs/live.go's entry)"},
	"internal/command/live_bucket_contract.go:bucketWaiverWarnings": {Why: "warns once per waived allow_insecure setting, named per record_store kind (the bucket's three settings, the cluster's four); same record-store enum as configs/live.go's, not a Substrate dispatch"},

	// uniquename regex.
	"internal/live/uniquename/uniquename.go:listingScopes": {Why: "issue #51's Cloud Control listing-scope word list, read only from AWS provider argument descriptions (Asserted's only caller is the AWS-only row-gen path); \"aws\" is a scope word (\"unique to your AWS account\") like \"account\" and \"region\", not a substrate dispatch"},

	// --- sites this guard's own first run found beyond the seed list ---

	"tools/estate-gen/gen.go": {Why: "a generator over the AWS provider's survey (same file-level reason already recorded in seams_test.go); it has no Kubernetes leg to pick between"},

	"tools/mapping-gen/overlay.go": {Why: "validates a mapping-gen overlay YAML file's schema, which only ever describes the AWS provider's CloudFormation mapping; \"aws_\" here is a forbidden-prefix check on a config key, not a substrate dispatch"},

	"tools/importdocs-gen/fetch.go":      {Why: "a generator over the AWS provider's doc cache (same reason as tools/estate-gen); no Kubernetes leg exists to pick between"},
	"tools/importdocs-gen/idtemplate.go": {Why: "a generator over the AWS provider's doc cache; no Kubernetes leg exists to pick between"},
	"tools/importdocs-gen/prosename.go":  {Why: "a generator over the AWS provider's doc cache; no Kubernetes leg exists to pick between"},
	"tools/importdocs-gen/example.go":    {Why: "a generator over the AWS provider's doc cache; no Kubernetes leg exists to pick between"},

	"tools/row-gen/importprecedence.go": {Why: "a generator over the AWS provider's survey and doc cache; no Kubernetes leg exists to pick between"},

	"tools/substrates-gen/build.go": {Why: "the capability-matrix generator itself (issue #1588): it builds live/substrates.json by iterating substrate.All and is keyed by family name (\"aws\", \"kubernetes\") throughout, including the floci harness pin read off the aws row - this file's whole job is naming the substrates, which is the seam's own artifact, not a bypass of it"},

	"tools/estate-plan/main.go:nonAWS": {Why: "live/survey-full.json describes exactly one provider (AWS); a type's prefix is checked against \"aws\" to report what estate-plan cannot mark, the survey's own scope, not a dispatch between substrates"},
	"tools/estate-plan/main.go:anyAWS": {Why: "the same single-provider survey scope as nonAWS, asked the other way"},

	"tools/gauntlet/livecert.go:PlanLiveCertWrites":   {Why: "target selects the live-cert harness (the floci emulator or a real AWS account, issue #1150/#1151), not a marker-surface dispatch; live-cert has no Kubernetes leg"},
	"tools/gauntlet/livecert.go:RunLiveCert":          {Why: "same target selection as PlanLiveCertWrites, validated once at the entry point"},
	"tools/gauntlet/scalerecord.go:validScaleTargets": {Why: "the same live-cert target enum (\"floci\", \"aws\") as livecert.go, declared as a set for validation"},
}

// providerStringPendingRound2 names a site this guard measures today that
// a later round-2 unit is expected to remove, keyed the same way as
// providerStringAllowed. It exists so this guard can go in ahead of those
// units without either allowlisting a site everyone already agrees is a
// gap, or blocking on work assigned elsewhere. A pending entry that no
// longer matches anything fails loudly (TestNoProviderStringChecksOutsideSubstrate
// checks providerStringPendingRound2 the same way surfaceSeamExemptions is
// checked in seams_test.go): the unit that removes the site must also
// delete the entry, not leave a stale reservation behind.
//
// Measured 2026-09-27 against this guard's own detection shapes (a literal
// comparison, a HasPrefix-family call, a switch case, or a map key): #1705
// (Kubernetes-named fields on shared structs) and #1707 (the unclaimed-
// provider AWS fallback and live-ls's two hard sweep arms) are both real,
// but neither one's named sites take any of those four shapes - #1705's
// are struct field declarations and reads (KubernetesAddressBound,
// kubeSweepers, kubeDeletes, liveLsComparison.Kubernetes, since renamed
// by #1705 to AddressBound, labelListSweepers, sweeperDeletes and
// liveLsComparison.LabelListItems), and #1707's
// fallback is a bool (known) and a typed discovery.Sweeper value, not a
// string literal anywhere in the path this guard reads. Neither produced a
// site to seed here; this map is empty until a round-2 unit's own diff
// introduces one that does take this guard's shape.
var providerStringPendingRound2 = map[string]string{}

// providerStringSite is one place the scan found a substrate-name literal
// deciding something.
type providerStringSite struct {
	key  string // "<file>:<scope>"
	file string
	pos  string // "<file>:<line>"
	text string
}

func TestNoProviderStringChecksOutsideSubstrate(t *testing.T) {
	root := providerStringModuleRoot(t)
	sites, err := scanProviderStringSites(root, providerStringRoots, providerStringExcludedDirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) < 10 {
		t.Fatalf("measured %d provider-string sites; the tree has had more than 20 since this guard's first run - the scan has gone blind", len(sites))
	}

	seen := map[string]bool{}
	for _, s := range sites {
		if _, ok := providerStringAllowed[s.key]; ok {
			seen[s.key] = true
			continue
		}
		if _, ok := providerStringAllowed[s.file]; ok {
			seen[s.file] = true
			continue
		}
		if issue, ok := providerStringPendingRound2[s.key]; ok {
			seen[s.key] = true
			_ = issue
			continue
		}
		if issue, ok := providerStringPendingRound2[s.file]; ok {
			seen[s.file] = true
			_ = issue
			continue
		}
		t.Errorf("%s (%s): provider-name check (%s) outside internal/live/substrate with no allowlist entry. Add it to providerStringAllowed with a reason, to providerStringPendingRound2 if a round-2 unit already owns removing it, or move the logic behind internal/live/substrate.", s.key, s.pos, s.text)
	}
	for key := range providerStringAllowed {
		if !seen[key] {
			t.Errorf("providerStringAllowed[%q] names no site the guard measured; remove it", key)
		}
	}
	for key, issue := range providerStringPendingRound2 {
		if !seen[key] {
			t.Errorf("providerStringPendingRound2[%q] (issue #%s) names no site the guard measured; the site moved or the unit already landed - delete this entry", key, issue)
		}
	}
}

// TestProviderStringGuardSeesTheFixture proves the scanner is not blind by
// running it over a small tree containing exactly one unlisted site and
// checking it is found with the shape (text) expected. A guard that always
// reports zero sites would otherwise pass TestNoProviderStringChecksOutsideSubstrate
// for the wrong reason.
func TestProviderStringGuardSeesTheFixture(t *testing.T) {
	root := providerStringModuleRoot(t)
	fixtureRoot := filepath.Join(root, "internal/live/substrate/testdata/providerstringfixture")
	sites, err := scanProviderStringSites(root, []string{"internal/live/substrate/testdata/providerstringfixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixtureRoot); err != nil {
		t.Fatalf("fixture tree missing: %v", err)
	}

	want := map[string]string{
		"internal/live/substrate/testdata/providerstringfixture/fixture.go:decide":         `== "aws"`,
		"internal/live/substrate/testdata/providerstringfixture/fixture.go:prefixCheck":    `strings.HasPrefix(..., "kubernetes_")`,
		"internal/live/substrate/testdata/providerstringfixture/fixture.go:switchOnKind":   `case "kubernetes"`,
		"internal/live/substrate/testdata/providerstringfixture/fixture.go:familyHandlers": `key "aws"`,
	}
	got := map[string]string{}
	for _, s := range sites {
		got[s.key] = s.text
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("fixture site %s: measured %q, want %q (all sites: %+v)", k, got[k], w, sites)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("fixture site %s measured but not expected (text %q)", k, got[k])
		}
	}
}

// scanProviderStringSites walks roots (relative to root, root included in
// the returned sites' file paths) and reports every provider-name literal
// site outside excludeDirs (relative-to-root directory paths).
func scanProviderStringSites(root string, roots []string, excludeDirs map[string]bool) ([]providerStringSite, error) {
	fset := token.NewFileSet()
	var files []string
	for _, r := range roots {
		start := filepath.Join(root, r)
		err := filepath.WalkDir(start, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if excludeDirs[rel] {
					return filepath.SkipDir
				}
				if d.Name() == "testdata" || d.Name() == "node_modules" || (strings.HasPrefix(d.Name(), ".") && p != start) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "_generated.go") {
				return nil
			}
			files = append(files, p)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)

	var sites []providerStringSite
	for _, p := range files {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)

		imports := map[string]string{}
		for _, im := range f.Imports {
			path, unquoteErr := strconv.Unquote(im.Path.Value)
			if unquoteErr != nil {
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if im.Name != nil {
				name = im.Name.Name
			}
			imports[name] = path
		}

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				name := d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					name = providerStringRecvName(d.Recv.List[0].Type) + "." + name
				}
				inspectForProviderStrings(d.Body, rel, name, fset, imports, &sites)
			case *ast.GenDecl:
				if d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					name := "?"
					for _, n := range vs.Names {
						if n.Name != "_" {
							name = n.Name
							break
						}
					}
					for _, v := range vs.Values {
						inspectForProviderStrings(v, rel, name, fset, imports, &sites)
					}
				}
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].pos < sites[j].pos })
	return sites, nil
}

// inspectForProviderStrings walks n (a function body or a top-level
// value expression) for the four shapes this guard measures, recording
// each as a site keyed by file:scope.
func inspectForProviderStrings(n ast.Node, file, scope string, fset *token.FileSet, imports map[string]string, sites *[]providerStringSite) {
	add := func(pos token.Pos, text string) {
		p := fset.Position(pos)
		*sites = append(*sites, providerStringSite{
			key:  file + ":" + scope,
			file: file,
			pos:  fmt.Sprintf("%s:%d", file, p.Line),
			text: text,
		})
	}
	ast.Inspect(n, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return true
			}
			if lit, ok := providerStringLiteral(x.X, substrateLiterals); ok {
				add(x.Pos(), fmt.Sprintf("%s %q", x.Op, lit))
			} else if lit, ok := providerStringLiteral(x.Y, substrateLiterals); ok {
				add(x.Pos(), fmt.Sprintf("%s %q", x.Op, lit))
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || imports[id.Name] != "strings" {
				return true
			}
			switch sel.Sel.Name {
			case "HasPrefix", "TrimPrefix", "HasSuffix", "TrimSuffix", "Contains":
				if len(x.Args) < 2 {
					return true
				}
				if lit, ok := providerStringLiteral(x.Args[1], substratePrefixLiterals); ok {
					add(x.Pos(), fmt.Sprintf("strings.%s(..., %q)", sel.Sel.Name, lit))
				}
			}
		case *ast.CaseClause:
			for _, e := range x.List {
				if lit, ok := providerStringLiteral(e, substrateLiterals); ok {
					add(e.Pos(), fmt.Sprintf("case %q", lit))
				}
			}
		case *ast.KeyValueExpr:
			if lit, ok := providerStringLiteral(x.Key, substrateLiterals); ok {
				add(x.Pos(), fmt.Sprintf("key %q", lit))
			}
		}
		return true
	})
}

func providerStringLiteral(e ast.Expr, set map[string]bool) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	if set[s] {
		return s, true
	}
	return "", false
}

func providerStringRecvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return providerStringRecvName(t.X)
	case *ast.IndexExpr:
		return providerStringRecvName(t.X)
	case *ast.IndexListExpr:
		return providerStringRecvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func providerStringModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
