// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
)

// LiveSidecarFilename is the name of the sidecar configuration file that can
// carry a module's live configuration instead of a "live" block inside a
// "terraform" block. The file's whole body is the live block's content -
// "estate", "policy", "record_store" - with no wrapper block around it.
//
// The sidecar exists for ecosystem compatibility (GitHub issue #72): strict
// HCL parsers - stock tofu and terraform validate, tflint, editors - are
// right to reject an unknown block inside terraform{}, so the in-.tf form
// taxes any repository whose CI runs stock tooling over the same files. The
// sidecar is a file those tools never read (the extension is deliberately
// not .tf or .tofu), so adopting live markers adds one file and changes zero
// existing lines. It is still a checked-in, reviewed file and not a flag,
// which is the property [Live]'s doc comment explains the mode depends on.
//
// A module may use either form; both at once is refused in Module.appendFile,
// because a live configuration must have one source of truth.
const LiveSidecarFilename = "estate.chdf.hcl"

// loadLiveSidecarFile looks for [LiveSidecarFilename] in dir and, when it is
// present, parses and decodes it into a synthetic *File carrying exactly one
// Live with Sidecar set.
//
// It returns (nil, nil) when the file does not exist, which is every
// configuration that has not opted in: stock behavior is untouched, and
// Parser.IsConfigDir deliberately does not know about the sidecar - a
// directory holding only a sidecar is not a module.
//
// The callers are the directory loaders in parser_config_dir.go, which append
// the returned file to the primary file set before module assembly. That
// placement is load-bearing: it is the same decoder lifecycle point the
// in-terraform{} form is decoded at, so the backend-versus-live wall in
// Module.appendFile - reached through SelectiveLoadBackend by every command
// that would otherwise touch a state manager - sees a sidecar's live
// configuration exactly as it sees a block's.
func (p *Parser) loadLiveSidecarFile(dir string) (*File, hcl.Diagnostics) {
	path := filepath.Join(dir, LiveSidecarFilename)
	src, err := p.fs.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, hcl.Diagnostics{&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Failed to read live sidecar file",
			Detail:   fmt.Sprintf("The live sidecar file %s exists but could not be read: %s.", path, err),
		}}
	}

	hclFile, diags := p.p.ParseHCL(src, path)
	if hclFile == nil || hclFile.Body == nil {
		return nil, diags
	}

	live, liveDiags := decodeLiveBody(hclFile.Body, hcl.Range{
		Filename: path,
		Start:    hcl.InitialPos,
		End:      hcl.InitialPos,
	})
	diags = append(diags, liveDiags...)
	live.Sidecar = true

	return &File{Lives: []*Live{live}}, diags
}

// appendLiveSidecar loads dir's live sidecar file, if there is one, and
// appends it to the primary file set. Every directory loader calls this so
// that the sidecar is visible under every SelectiveLoader whose filter
// carries Lives - in particular SelectiveLoadBackend, whose whole purpose is
// to let the backend-versus-live wall see backends, cloud blocks and live
// configurations in one load.
func (p *Parser) appendLiveSidecar(dir string, primary []*File) ([]*File, hcl.Diagnostics) {
	sidecar, diags := p.loadLiveSidecarFile(dir)
	if sidecar != nil {
		primary = append(primary, sidecar)
	}
	return primary, diags
}
