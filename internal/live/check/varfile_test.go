// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadVarsFilesExtraOverridesDirectory is #183's loader-level seam: an
// extra var file passed alongside a directory behaves the way stock
// OpenTofu's repeated -var-file applies - highest precedence, in the order
// given - and a bare read of the same directory (no extra) is exactly what
// it was before this seam existed.
func TestReadVarsFilesExtraOverridesDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfvars"), []byte(`name = "from-dir"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bare := readVarsFiles(dir)
	if v, ok := bare["name"]; !ok || v.AsString() != "from-dir" {
		t.Fatalf("bare readVarsFiles()[\"name\"] = %v, ok=%v, want \"from-dir\"", v, ok)
	}

	extraDir := t.TempDir()
	extraPath := filepath.Join(extraDir, "corpus.tfvars")
	if err := os.WriteFile(extraPath, []byte(`name = "from-corpus-var-file"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	withExtra := readVarsFiles(dir, extraPath)
	if v, ok := withExtra["name"]; !ok || v.AsString() != "from-corpus-var-file" {
		t.Fatalf("readVarsFiles(dir, extra)[\"name\"] = %v, ok=%v, want \"from-corpus-var-file\" (the extra file must win)", v, ok)
	}

	// The directory-only read is unaffected by the call above: the seam is
	// a per-call argument, not shared state that a var-file call could leak
	// into every other caller of the same directory.
	bareAgain := readVarsFiles(dir)
	if v, ok := bareAgain["name"]; !ok || v.AsString() != "from-dir" {
		t.Fatalf("readVarsFiles(dir) after a var-file call = %v, ok=%v, want \"from-dir\" unchanged", v, ok)
	}
}

// TestDirClearsUnsetVariableWithExtraVarFile exercises the same seam one
// layer up, through [Dir] - the entry point corpus-gen actually calls. The
// static evaluator behind [Load] is lazy (see [LoadResult.vars]'s doc
// comment): a required variable is not recorded as missing until something
// actually asks for its value, which for an identity-bearing argument like
// "bucket" only happens during [Analyze]'s identity resolution, so this
// goes through Dir rather than Load alone to trigger that read. A bare run
// reports the variable unset; supplying a var file clears it; a second bare
// run of the same directory afterward still reports it unset, proving the
// two calls do not share state.
func TestDirClearsUnsetVariableWithExtraVarFile(t *testing.T) {
	dir := t.TempDir()
	tf := `
variable "name" {
  type = string
}

resource "aws_s3_bucket" "b" {
  bucket = var.name
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o600); err != nil {
		t.Fatal(err)
	}

	bare := Dir(t.Context(), dir, Context{})
	if got := bare.Load.UnsetVariables(); len(got) != 1 || got[0] != "name" {
		t.Fatalf("bare Dir: UnsetVariables() = %v, want [name]", got)
	}

	extraDir := t.TempDir()
	varFile := filepath.Join(extraDir, "corpus.tfvars")
	if err := os.WriteFile(varFile, []byte(`name = "from-var-file"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	supplied := Dir(t.Context(), dir, Context{}, varFile)
	if got := supplied.Load.UnsetVariables(); len(got) != 0 {
		t.Fatalf("Dir with var file: UnsetVariables() = %v, want none", got)
	}

	bareAgain := Dir(t.Context(), dir, Context{})
	if got := bareAgain.Load.UnsetVariables(); len(got) != 1 || got[0] != "name" {
		t.Fatalf("bare Dir after a var-file Dir: UnsetVariables() = %v, want [name] unchanged", got)
	}
}

// TestResolvePropagatesVarFile is the manifest-side half: a source's
// var_file (#183) reaches the [CorpusEntryRef] for the directory it names,
// unresolved and repository-root-relative exactly as written, and a source
// with none leaves the field empty - so a corpus-gen run over one manifest
// can tell a vars-supplied entry from a bare one without touching the
// filesystem.
func TestResolvePropagatesVarFile(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"corpus/withvars", "corpus/bare"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "main.tf"), []byte("# config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	manifest := Manifest{Sources: []ManifestSource{
		{Glob: "corpus/withvars", Origin: "test", VarFile: "vars/withvars.tfvars"},
		{Glob: "corpus/bare", Origin: "test"},
	}}
	entries, err := manifest.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("resolved %d entries, want 2: %+v", len(entries), entries)
	}

	byName := map[string]CorpusEntryRef{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	if got := byName["corpus/withvars"].VarFile; got != "vars/withvars.tfvars" {
		t.Errorf("corpus/withvars: VarFile = %q, want \"vars/withvars.tfvars\"", got)
	}
	if got := byName["corpus/bare"].VarFile; got != "" {
		t.Errorf("corpus/bare: VarFile = %q, want empty - a source with no var_file must not acquire one", got)
	}
}

// TestCorpusAddRecordsVarFile is the artifact-side half: [Corpus.Add]'s
// optional varFile argument lands on the entry so a reader of
// live/corpus-refusals.json can tell a vars-supplied measurement from a
// bare one, and every ordinary call - the eight elsewhere in this package's
// own tests, all of which pass none - keeps compiling and keeps producing
// an empty field.
func TestCorpusAddRecordsVarFile(t *testing.T) {
	corpus := NewCorpus()
	corpus.Add("with-vars", "test", Report{}, "live/corpus-vars/example.tfvars")
	corpus.Add("bare", "test", Report{})
	corpus.Finish()

	byName := map[string]CorpusEntry{}
	for _, e := range corpus.Entries {
		byName[e.Name] = e
	}

	if got := byName["with-vars"].VarFile; got != "live/corpus-vars/example.tfvars" {
		t.Errorf("with-vars: VarFile = %q, want \"live/corpus-vars/example.tfvars\"", got)
	}
	if got := byName["bare"].VarFile; got != "" {
		t.Errorf("bare: VarFile = %q, want empty", got)
	}
}
