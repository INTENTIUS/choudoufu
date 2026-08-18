// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

// Package onboard computes the source edit that turns a state-backed module
// into a live one, from the module's own text and from nothing else.
//
// # Why this exists
//
// Every instrument in this repository measures the ADOPTION question: can a
// stranger's published configuration be taken over exactly as it stands.
// tools/refusal-probe and tools/estate-plan both read the published form of
// every corpus entry, and - verified by scanning all 250 of them - not one
// declares a live block, a record_store or the sidecar file. So nothing
// measured the form the product is actually for: someone writes ordinary
// Terraform, adds a live block, applies, and choudoufu manages it from then
// on with no state file.
//
// The edit that gets from one to the other is small and mechanical, and it
// had been made by hand a dozen times - once per end-to-end crossing under
// live/e2e, and once in commit 5e6cf9c86f to justify a refusal's demotion.
// This is that edit, computed rather than typed.
//
// # The edit
//
// Three steps, and only the first two are this package's:
//
//  1. Add a live configuration declaring a record_store. That is what admits
//     [lint.ClassRecordAdmitted] logical types and what lets
//     identity.LocatedType place a markerless-but-locatable one, so it is the
//     step that moves refusals.
//  2. Remove the backend or cloud block. Not cosmetic:
//     configs.Module.appendFile refuses a module declaring both, so a live
//     configuration added beside a surviving backend does not load at all.
//  3. Pin the provider (GitHub issue #269). Not done here, because it is not
//     a source edit: tools/refusal-probe already holds hashicorp/aws at
//     [pins.AWSProviderVersion] for every entry it measures, in both forms,
//     so the pin is already in the measurement.
//
// # Why the sidecar
//
// Step 1 is written as [configs.LiveSidecarFilename] rather than as a live
// block inside a terraform block. The two decode identically - the sidecar's
// whole body is the block's content - but the sidecar adds one file and
// changes zero existing lines, which makes the edit auditable: a reader can
// see the entire live configuration in one place and diff the rest of the
// module against its published form byte for byte.
//
// # What is derived and what is not
//
// Everything here is read off the directory. There is no per-estate table and
// no list of type names: the files to rewrite come from
// [configs.Parser.ConfigFiles], which is the loader's own selection rather
// than a filter beside it, and the blocks to remove are found by walking
// them. The estate name is derived from the module's path, and is the one
// value in the edit that is invented rather than found - see [EstateName],
// which also records why it cannot bias any measurement taken through
// check.Analyze.
package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// Status is what [Compute] concluded about one directory.
type Status string

const (
	// StatusEdited means an onboarding edit was produced. [Plan.Overlay] is
	// non-empty.
	StatusEdited Status = "edited"

	// StatusAlreadyOnboarded means the module already declares a live
	// configuration with a record_store, so its published form IS its
	// onboarded form and no edit is needed. [Plan.Overlay] is empty and the
	// two measurements are the same measurement by construction.
	StatusAlreadyOnboarded Status = "already-onboarded"

	// StatusUnmeasurable means no honest edit could be computed. [Plan.Reason]
	// says which of [Unmeasurable]'s cases fired. An unmeasurable estate is a
	// finding, not a gap to paper over: its count belongs in any report built
	// on this package.
	StatusUnmeasurable Status = "unmeasurable"
)

// Unmeasurable reasons. Each is a shape this package refuses to guess at
// rather than a shape it has not got round to. They are exported so a caller
// can bucket its unmeasurable set without matching on prose.
const (
	// UnmeasurableUnreadable is a directory the loader's own file selection
	// could not enumerate.
	UnmeasurableUnreadable = "directory could not be enumerated"

	// UnmeasurableUnparseable is a native-syntax file carrying a terraform
	// block that hclwrite could not parse. The loader may well parse it -
	// hclwrite is stricter about some shapes - but this package will not
	// rewrite text it cannot read back.
	UnmeasurableUnparseable = "a configuration file did not parse as native HCL"

	// UnmeasurableJSONBackend is a backend or cloud block declared in a
	// .tf.json or .tofu.json file. Removing it means rewriting JSON, which
	// this package does not do: a re-marshalled object is not the edit an
	// operator would commit, and no entry in the corpus needs it (measured -
	// the corpus holds no root-module JSON configuration file at all).
	UnmeasurableJSONBackend = "backend or cloud block declared in JSON syntax"

	// UnmeasurableEstateName is a directory path that no valid estate name
	// can be derived from. See [EstateName].
	UnmeasurableEstateName = "no valid estate name could be derived from the module path"
)

// Plan is the edit for one module directory.
type Plan struct {
	// Dir is the directory as it was passed to [Compute], not resolved: the
	// overlay's keys are joined against it, so a caller that loads the
	// directory by the same spelling gets the overlay applied.
	Dir string `json:"dir"`

	Status Status `json:"status"`

	// Reason is set for [StatusUnmeasurable] and for
	// [StatusAlreadyOnboarded]; empty for [StatusEdited].
	Reason string `json:"reason,omitempty"`

	// Estate is the derived estate name. Set whenever a sidecar was written.
	Estate string `json:"estate,omitempty"`

	// Overlay is path -> new content, where each path is
	// filepath.Join(Dir, name). It holds both added files and rewritten
	// ones; Added and Rewritten say which is which.
	Overlay map[string][]byte `json:"-"`

	// Added and Rewritten are the overlay's paths, split by whether the file
	// exists on disk, sorted.
	Added     []string `json:"added,omitempty"`
	Rewritten []string `json:"rewritten,omitempty"`

	// Removed describes each block this edit deletes, as
	// `backend "s3" at main.tf:3`. Recorded so a report can show what the
	// edit did without re-deriving it.
	Removed []string `json:"removed,omitempty"`
}

// Compute reads dir and returns the onboarding edit for it.
//
// It never writes anything. The whole edit is in [Plan.Overlay], for a caller
// to apply through check.LoadOverlay - which is the property that lets this
// run over a shared, read-only corpus checkout without a copy and without
// contaminating the next measurement.
func Compute(dir string) Plan {
	p := Plan{Dir: dir, Overlay: map[string][]byte{}}

	parser := configs.NewParser(nil)
	primary, override, diags := parser.ConfigFiles(dir)
	if diags.HasErrors() {
		p.Status = StatusUnmeasurable
		p.Reason = UnmeasurableUnreadable
		return p
	}

	files := append(append([]string{}, primary...), override...)
	sort.Strings(files)

	// One pass over the module's text: where the backends are, where a live
	// configuration already is, and whether that live configuration already
	// declares a record_store.
	var (
		liveFound      bool
		liveHasStore   bool
		liveFile       string
		liveIsSidecar  bool
		backendFiles   = map[string]bool{}
		rewritten      = map[string][]byte{}
		sidecarPath    = filepath.Join(dir, configs.LiveSidecarFilename)
		jsonBackend    bool
		unparseable    bool
		removedRecords []string
	)

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			p.Status = StatusUnmeasurable
			p.Reason = UnmeasurableUnreadable
			return p
		}
		if isJSONConfig(path) {
			if jsonDeclaresBackend(src) {
				jsonBackend = true
			}
			if jsonDeclaresLive(src) {
				// A live configuration in JSON: this package will not add a
				// record_store to it either, for the same reason it will not
				// remove a backend from it.
				liveFound = true
				liveFile = path
				jsonBackend = true
			}
			continue
		}

		f, fDiags := hclwrite.ParseConfig(src, path, hcl.InitialPos)
		if fDiags.HasErrors() || f == nil {
			// Only fatal if this file is one the edit would have to touch,
			// and we cannot know that without parsing it. Treat any
			// unparseable native file in the module as unmeasurable rather
			// than quietly editing the rest: a backend hiding in the file we
			// skipped is the failure this package must not have.
			unparseable = true
			continue
		}

		for _, blk := range f.Body().Blocks() {
			if blk.Type() != "terraform" {
				continue
			}
			for _, inner := range blk.Body().Blocks() {
				switch inner.Type() {
				case "backend", "cloud":
					backendFiles[path] = true
					removedRecords = append(removedRecords, describeBlock(dir, path, inner))
					blk.Body().RemoveBlock(inner)
				case "live":
					liveFound = true
					liveFile = path
					for _, ls := range inner.Body().Blocks() {
						if ls.Type() == "record_store" {
							liveHasStore = true
						}
					}
					if !liveHasStore {
						inner.Body().AppendNewBlock("record_store", []string{"local"})
						backendFiles[path] = true // force a rewrite of this file
					}
				}
			}
		}
		if backendFiles[path] {
			rewritten[path] = f.Bytes()
		}
	}

	// The sidecar is a file the loader appends to the primary set itself, so
	// ConfigFiles never returns it. Read it directly.
	if src, err := os.ReadFile(sidecarPath); err == nil {
		liveFound = true
		liveIsSidecar = true
		liveFile = sidecarPath
		if sidecarDeclaresStore(src) {
			liveHasStore = true
		} else {
			rewritten[sidecarPath] = append(append([]byte{}, src...), []byte("\n"+recordStoreBlock)...)
		}
	}

	switch {
	case unparseable:
		p.Status = StatusUnmeasurable
		p.Reason = UnmeasurableUnparseable
		return p
	case jsonBackend:
		p.Status = StatusUnmeasurable
		p.Reason = UnmeasurableJSONBackend
		return p
	case liveFound && liveHasStore:
		p.Status = StatusAlreadyOnboarded
		form := "live block"
		if liveIsSidecar {
			form = configs.LiveSidecarFilename + " sidecar"
		}
		p.Reason = fmt.Sprintf("%s with a record_store already at %s", form, relTo(dir, liveFile))
		return p
	}

	if !liveFound {
		estate, ok := EstateName(dir)
		if !ok {
			p.Status = StatusUnmeasurable
			p.Reason = UnmeasurableEstateName
			return p
		}
		p.Estate = estate
		rewritten[sidecarPath] = []byte(sidecar(estate))
	}

	p.Status = StatusEdited
	p.Overlay = rewritten
	p.Removed = removedRecords
	for path := range rewritten {
		if _, err := os.Stat(path); err == nil {
			p.Rewritten = append(p.Rewritten, relTo(dir, path))
		} else {
			p.Added = append(p.Added, relTo(dir, path))
		}
	}
	sort.Strings(p.Added)
	sort.Strings(p.Rewritten)
	sort.Strings(p.Removed)
	return p
}

// recordStoreBlock is step 2 of the edit, as text. "local" is the store an
// operator onboarding for the first time gets to use with nothing else to
// configure: configs.LiveRecordStore's path argument defaults to a
// .tofu-records directory beside the module, and the other two backends need
// a bucket or a key namespace that is not derivable from the configuration.
const recordStoreBlock = "record_store \"local\" {}\n"

// sidecar renders the whole live configuration for a module that had none.
func sidecar(estate string) string {
	return fmt.Sprintf(`# Onboarding edit, computed by internal/live/onboard.
#
# This file is the live configuration: the same content a "live" block inside
# a terraform block would carry. It is what replaces the backend block this
# edit removed - a module may declare one or the other, never both.
estate = %q

# Admits the record-backed logical types and gives a record-located type
# somewhere to keep the ID choudoufu minted for it.
%s`, estate, recordStoreBlock)
}

// EstateName derives an estate name from a module directory path, and reports
// whether the result is well-formed under markers.ValidEstateName.
//
// The rule is the whole path rather than its last segment, because the last
// segment is very often "complete" or "simple" and an estate name has to be
// unique to the estate. Separators, dots and every other character outside
// the grammar become hyphens; runs collapse; the name is trimmed to start
// with a letter and truncated from the FRONT to the grammar's 128 characters,
// so that what survives is the specific tail rather than a shared prefix.
//
// This is the one value in the edit that is invented rather than found, so it
// is worth being explicit about what it can affect: nothing that check.Analyze
// measures. The only two readers of configs.Live inside that analysis are
// lint's record-store gate and identity.LocatedType's, and both test
// RecordStore != nil without looking at the name. TestEstateNameCannotBias
// pins that by measuring one directory under several names.
func EstateName(dir string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(dir))
	var b strings.Builder
	lastHyphen := true // also suppresses a leading hyphen
	for _, r := range strings.ToLower(clean) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	// The grammar wants a leading letter. Trim digits and hyphens off the
	// front rather than prefixing something, so the name stays a slug of the
	// path and two paths cannot collide through a shared invented prefix.
	s = strings.TrimLeft(s, "0123456789-")
	if len(s) > 128 {
		s = strings.TrimLeft(s[len(s)-128:], "0123456789-")
	}
	if !markers.ValidEstateName(s) {
		return "", false
	}
	return s, true
}

// describeBlock renders one removed block for [Plan.Removed].
func describeBlock(dir, path string, blk *hclwrite.Block) string {
	label := blk.Type()
	if ls := blk.Labels(); len(ls) > 0 {
		label = fmt.Sprintf("%s %q", blk.Type(), ls[0])
	}
	return fmt.Sprintf("%s in %s", label, relTo(dir, path))
}

func relTo(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}

func isJSONConfig(path string) bool {
	return strings.HasSuffix(path, ".tf.json") || strings.HasSuffix(path, ".tofu.json")
}

// jsonDeclaresBackend reports whether a JSON configuration file's terraform
// block declares a backend or a cloud block. HCL's JSON syntax lets the
// terraform key hold either an object or an array of them, so both are
// walked.
func jsonDeclaresBackend(src []byte) bool { return jsonTerraformHas(src, "backend", "cloud") }

func jsonDeclaresLive(src []byte) bool { return jsonTerraformHas(src, "live") }

func jsonTerraformHas(src []byte, keys ...string) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(src, &obj); err != nil {
		return false
	}
	raw, ok := obj["terraform"]
	if !ok {
		return false
	}
	var one map[string]json.RawMessage
	var many []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &one); err == nil {
		many = []map[string]json.RawMessage{one}
	} else if err := json.Unmarshal(raw, &many); err != nil {
		return false
	}
	for _, t := range many {
		for _, k := range keys {
			if _, ok := t[k]; ok {
				return true
			}
		}
	}
	return false
}

// sidecarDeclaresStore reports whether an existing sidecar file already
// declares a record_store, parsed rather than matched: the sidecar's body is
// live-block content, so a record_store is a top-level block in it.
func sidecarDeclaresStore(src []byte) bool {
	f, diags := hclwrite.ParseConfig(src, configs.LiveSidecarFilename, hcl.InitialPos)
	if diags.HasErrors() || f == nil {
		return false
	}
	for _, blk := range f.Body().Blocks() {
		if blk.Type() == "record_store" {
			return true
		}
	}
	return false
}
