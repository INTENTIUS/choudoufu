// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"path"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// Live represents a "live" block inside a "terraform" block in a
// module. Its presence is what puts a run into stateless mode: no state file,
// no backend, no lock, with prior state rebuilt from the live system on every
// operation.
//
// It is deliberately a configuration block and not a command-line flag.
// Whether a team's infrastructure has an authoritative state file is a
// property of the configuration, checked in and reviewed with it; a flag
// would mean a run that forgot it silently fell back to writing a state
// file, which is exactly the failure this mode exists to remove.
//
// The block is also why a stateless module cannot have a backend: a "backend"
// or "cloud" block alongside it is refused here, in the configuration
// decoder, rather than only by the stateless subset lint. The decoder is the
// earlier wall, and it is the one every command passes through - including
// the ones that would otherwise reach for a state manager before any
// stateless code ran.
type Live struct {
	// Estate is the name of the estate this configuration owns, as it appears
	// in the tofu-estate marker described by live/MARKERS.md. It is
	// optional: when it is not set, the estate name is derived from the
	// tofu-estate tags the configuration already stamps, and a configuration
	// that stamps none (or several) is told to name one here.
	//
	// The value must be a literal string. An estate name assembled from a
	// variable would mean the identity of the thing that owns live resources
	// depends on how a run was invoked, and ownership records that move with
	// the invocation are not ownership records.
	Estate string

	// EstateSet distinguishes an absent estate argument from one set to the
	// empty string, so that the second can be an error rather than silently
	// meaning "derive it".
	EstateSet bool

	// EstateRange is where the "estate" argument was written, the zero value
	// when the block does not set it. It is recorded so that a diagnostic
	// about the estate can point at the argument that named it rather than at
	// the whole block, which is what DeclRange gives.
	EstateRange hcl.Range

	// SnapshotPath, when set, turns on the optional observational snapshot
	// (P4.2): after a run's final state write, a scrubbed, metadata-only
	// record of what the projection held is written to this path, for
	// offline diff and audit. It is never read back by any code path in this
	// fork - the snapshot's contract is staleness, the opposite of what a
	// state file promises - so its absence changes nothing about how a
	// stateless run behaves.
	//
	// Empty means "no attribute in the block", which must mean no file is
	// ever written. There is deliberately no default path: a team that wants
	// the cache says so in the configuration that is checked in and
	// reviewed, the same reasoning that makes the "live" block itself a
	// config block and not a flag.
	//
	// The value must be a literal string, for the same reason as Estate: a
	// path assembled from a variable would make where the cache lands depend
	// on how a run was invoked. It must also be a relative path inside the
	// module directory that does not name a state file - see
	// validateSnapshotPath for the rules and why a cache gets so little
	// reach.
	//
	// When Snapshots is also true, this path is the fallback carrier rather
	// than the primary one: the snapshot goes to the orphan branch, and the
	// file is written only when the branch cannot be. Set alone, it keeps
	// exactly the file behavior described above.
	SnapshotPath string

	// Snapshots, when true, turns on the same optional observational
	// snapshot as SnapshotPath but carried as an orphan git branch
	// (tofu-snapshots/<estate>) in the repository enclosing the module
	// directory, one commit per apply, so drift over time is git log on the
	// branch rather than a single overwritten file. The branch is written
	// with git plumbing through a temporary object graph: no push, no
	// checkout, and the worktree, the index and HEAD are never touched. See
	// internal/live/projection's writeSnapshotBranch for the mechanics.
	//
	// It composes with SnapshotPath rather than excluding it, and the branch
	// wins: when both are set, the branch is the carrier and the file is the
	// fallback, written only when the branch cannot be (no enclosing git
	// repository, no git on PATH, or a failed ref update). Snapshots set
	// alone in a directory with no enclosing repository writes nothing and
	// warns, because inventing a default file path would break the rule that
	// every snapshot file was named by the operator. False is the same as
	// absent; the attribute exists so a team can turn the branch carrier on
	// in the reviewed configuration, the same reasoning as the block itself.
	//
	// The value must be a literal bool, for the reason Estate and
	// SnapshotPath must be literal strings: whether an apply leaves an
	// observational record behind must not depend on how the run was
	// invoked.
	Snapshots bool

	// SnapshotsRange is where the "snapshots" argument was written, the zero
	// value when the block does not set it. Same purpose as EstateRange.
	SnapshotsRange hcl.Range

	// SnapshotPathRange is where the "snapshot_path" argument was written,
	// the zero value when the block does not set it. Same purpose as
	// EstateRange: a diagnostic about the snapshot path points at the path.
	//
	// Every consumer of it is in decodeLiveBlock and validateSnapshotPath
	// below, and that is not an oversight. Everything that can be wrong with
	// the value is lexical - it is required to be a literal string - so the
	// decoder catches all of it. The only later diagnostic about the snapshot
	// is [projection.Manager]'s warning that the file could not be written,
	// and a failed write is an environment fact rather than a claim about the
	// configuration, so it is sourceless the way the rest of the
	// environment-level diagnostics in this fork are.
	SnapshotPathRange hcl.Range

	// DeclRange is the "live" block's own header, which is what a diagnostic
	// about the block as a whole points at - a backend beside it, or a
	// stateless refusal that has no more specific argument to name.
	DeclRange hcl.Range
}

var liveBlockSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "estate"},
		{Name: "snapshots"},
		{Name: "snapshot_path"},
	},
}

func decodeLiveBlock(block *hcl.Block) (*Live, hcl.Diagnostics) {
	s := &Live{
		DeclRange: block.DefRange,
	}

	content, diags := block.Body.Content(liveBlockSchema)

	if attr, exists := content.Attributes["estate"]; exists {
		s.EstateRange = attr.Range
		val, valDiags := attr.Expr.Value(nil)
		diags = append(diags, valDiags...)
		switch {
		case valDiags.HasErrors():
			// attr.Expr.Value(nil) fails for anything that is not a constant,
			// which is the rule this argument wants; the diagnostics it
			// produces already name the variable or function involved.
		case val.IsNull() || !val.IsWhollyKnown() || val.Type() != cty.String:
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid estate name",
				Detail:   "The \"estate\" argument names the estate this configuration owns and must be a literal string.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		case val.AsString() == "":
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Empty estate name",
				Detail:   "The \"estate\" argument was set to an empty string. Give the estate a name, or omit the argument entirely to derive the name from the tofu-estate tags this configuration stamps.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		default:
			s.Estate = val.AsString()
			s.EstateSet = true
		}
	}

	if attr, exists := content.Attributes["snapshots"]; exists {
		s.SnapshotsRange = attr.Range
		val, valDiags := attr.Expr.Value(nil)
		diags = append(diags, valDiags...)
		switch {
		case valDiags.HasErrors():
			// Same rule as estate: attr.Expr.Value(nil) already names the
			// variable or function that makes this non-literal.
		case val.IsNull() || !val.IsWhollyKnown() || val.Type() != cty.Bool:
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid snapshots argument",
				Detail:   "The \"snapshots\" argument turns on the observational snapshot branch and must be a literal true or false.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		default:
			s.Snapshots = val.True()
		}
	}

	if attr, exists := content.Attributes["snapshot_path"]; exists {
		s.SnapshotPathRange = attr.Range
		val, valDiags := attr.Expr.Value(nil)
		diags = append(diags, valDiags...)
		switch {
		case valDiags.HasErrors():
			// Same rule as estate: attr.Expr.Value(nil) already names the
			// variable or function that makes this non-literal.
		case val.IsNull() || !val.IsWhollyKnown() || val.Type() != cty.String:
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid snapshot path",
				Detail:   "The \"snapshot_path\" argument names where the optional observational snapshot is written and must be a literal string.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		case val.AsString() == "":
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Empty snapshot path",
				Detail:   "The \"snapshot_path\" argument was set to an empty string. Give it a path, or omit the argument entirely - omitting it means no snapshot is ever written.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		default:
			if detail := validateSnapshotPath(val.AsString()); detail != "" {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid snapshot path",
					Detail:   detail,
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else {
				s.SnapshotPath = val.AsString()
			}
		}
	}

	return s, diags
}

// validateSnapshotPath returns the reason a snapshot_path may not be used, or
// "" when it is fine.
//
// The snapshot is a cache. Everything it is allowed to do to the filesystem
// has to be judged as "what may a cache overwrite", and the answer is: a file
// inside the module directory, that the operator named, that is not somebody's
// record of their infrastructure. Before this check the argument was accepted
// as any non-empty literal and then handed to os.MkdirAll and os.Rename
// unconditionally, which the audit used to destroy a real terraform.tfstate
// and to write through "../../" into a sibling project.
//
// The rules are all lexical, because the value is required to be a literal
// string: no filesystem is consulted here, and a configuration that would do
// something destructive is refused when it is read rather than when it is
// run. The complementary check that cannot be lexical - "the file already
// there parses as a state file, whatever it is called" - lives at the write
// itself, in internal/live/projection.
func validateSnapshotPath(raw string) string {
	// Windows separators are normalized so that one set of rules covers both
	// spellings; a configuration is meant to be portable, and "..\\victim" is
	// the same request as "../victim".
	norm := strings.ReplaceAll(raw, "\\", "/")

	if strings.HasPrefix(norm, "/") || (len(norm) >= 2 && norm[1] == ':') {
		return "The \"snapshot_path\" argument must be a relative path inside the module directory. It was given an absolute path, which would let a cache write anywhere on the machine running OpenTofu; the snapshot is observational and is not worth that reach."
	}

	cleaned := path.Clean(norm)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "The \"snapshot_path\" argument must stay inside the module directory, and this path leaves it. A cache that can be aimed at a sibling project is a way to overwrite that project's files from this configuration."
	}
	if cleaned == "." || cleaned == "" {
		return "The \"snapshot_path\" argument names a file to write, and this path names the module directory itself."
	}

	base := path.Base(cleaned)
	lower := strings.ToLower(base)
	if strings.HasPrefix(lower, "terraform.tfstate") || strings.HasSuffix(lower, ".tfstate") {
		return "The \"snapshot_path\" argument must not name a state file. The snapshot is a scrubbed, metadata-only cache with staleness in its contract; giving it a state file's name invites a later reader - a person, a script, a different tool - to treat it as the authoritative record this configuration exists to not have. It would also overwrite a real state file that happened to be there."
	}
	if first, _, _ := strings.Cut(cleaned, "/"); first == ".terraform" {
		return "The \"snapshot_path\" argument must not write inside the .terraform directory. That directory is OpenTofu's own working data, including the record of which backend a directory was initialized against, and a cache does not get to overwrite it."
	}

	return ""
}
