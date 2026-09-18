// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ── which AWS API operations paginate, and where that answer comes from ──
//
// The AWS CLI applies --query to EACH page of an auto-paginated listing
// before merging, so any --query that reduces a list to a scalar reads one
// page at a time. Issue #1042 hit it with `length(ResourceTagMappingList)`
// ("100 100 100 35" where 335 was meant) and issue #1206 hit it again with
// `Policies[?...].Arn | [0]` (the matching arn plus fifteen literal
// "None"s, which travelled two layers on and surfaced as an EMPTY
// ownership marker). Deciding whether a given call site is in that class
// needs one fact: does this operation paginate?
//
// Issue #1214's ruling is that the fact is DERIVED, never maintained by
// hand. botocore ships it - data/<service>/<api-version>/paginators-1.json
// - and it is the same data the AWS CLI itself consults to decide whether
// to page, so a list built from it cannot disagree with the tool whose
// behaviour is being guarded against. A hand-maintained list would be
// wrong in both directions and would rot silently.
//
// The set is used UNPRUNED, deliberately. botocore marks
// iam ListRoleTags as paginating, and #1214 cites exactly that call as the
// obviously-safe one ("returns a handful of tags"). It is safe today, and
// that assumption is what produced both defects: a call that CAN page is
// unsafe with a reducing --query however few items it usually returns.
// Pruning to "the ones that really page in practice" puts back the
// judgement call this derived list exists to remove.
//
// The snapshot is vendored (PaginatingOperationsPath) rather than read out
// of botocore at test time: CI must not depend on a Python package being
// installed, and a guard that skips itself when the package is absent is
// permanently green. See tools/aws-paginators-gen for the generator and
// its -check mode, and TestPaginatingOperationsSnapshotIsCanonical for
// what is verified without botocore present.

// PaginatingOperationsPath is the vendored snapshot, relative to the
// repository root.
const PaginatingOperationsPath = "live/aws-paginating-operations.json"

// PaginatingOperations is the vendored snapshot's schema: for each
// botocore service directory, the API operation names botocore's
// paginators declare, sorted and deduplicated across every API version the
// service directory carries.
type PaginatingOperations struct {
	Generated string `json:"_generated"`

	// BotocoreVersion is the botocore release the snapshot was taken from.
	// It is recorded so a reader can tell how old the snapshot is; it is
	// NOT compared against an installed botocore at test time, because
	// there may not be one.
	BotocoreVersion string `json:"botocore_version"`

	// PaginatorFiles is how many paginators-1.json / -1.sdk-extras.json
	// files the generator read, and ServiceDirs how many service
	// directories it walked - both including the ones that declare no
	// paginators at all. Recorded so that "the snapshot shrank because the
	// generator stopped finding files" is distinguishable from "AWS
	// removed paginators".
	PaginatorFiles int `json:"paginator_files"`
	ServiceDirs    int `json:"service_dirs"`

	// Services maps a botocore service directory name to its paginating
	// operation names.
	Services map[string][]string `json:"services"`
}

// cliServiceRenames are the AWS CLI command names that do NOT equal the
// botocore service directory they address. botocore's directory names and
// the CLI's command names were aligned years ago; what is left is the
// handful awscli renames in its own driver, and only the one this tree
// actually calls is listed here.
//
// This map is the single hand-written thing in the whole derivation, and
// it is kept honest by refusing rather than guessing: Paginates returns an
// error for a service token it cannot resolve to a botocore directory, and
// the guard turns that into a test failure naming the token. A new CLI
// alias therefore stops a human instead of quietly classifying its calls
// as non-paginating.
var cliServiceRenames = map[string]string{
	// awscli reserves the `s3` command for its high-level file-transfer
	// commands and exposes the S3 API itself as `s3api`; botocore's
	// directory is `s3`.
	"s3api": "s3",
}

// LoadPaginatingOperations reads the vendored snapshot from path.
func LoadPaginatingOperations(path string) (*PaginatingOperations, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p PaginatingOperations
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(p.Services) == 0 {
		return nil, fmt.Errorf("%s: snapshot declares no services - a guard reading it would classify every call as non-paginating", path)
	}
	return &p, nil
}

// Marshal renders the snapshot in its one canonical on-disk form: two-space
// indent, sorted keys, one trailing newline. The generator writes this and
// the drift test compares against it, so a hand edit that reorders or
// reformats the file fails even on a machine with no botocore installed.
func (p *PaginatingOperations) Marshal() ([]byte, error) {
	norm := &PaginatingOperations{
		Generated:       p.Generated,
		BotocoreVersion: p.BotocoreVersion,
		PaginatorFiles:  p.PaginatorFiles,
		ServiceDirs:     p.ServiceDirs,
		Services:        make(map[string][]string, len(p.Services)),
	}
	for svc, ops := range p.Services {
		norm.Services[svc] = dedupeSorted(ops)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(norm); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func dedupeSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownCLIService is returned by Paginates for a CLI service token the
// snapshot has no directory for. It is deliberately an error rather than a
// false: "I do not know this service" and "this operation does not page"
// must not read the same to a guard.
type ErrUnknownCLIService struct{ Service string }

func (e ErrUnknownCLIService) Error() string {
	return fmt.Sprintf("no botocore service directory for the AWS CLI service %q - add it to cliServiceRenames in live/awspaginators.go (or regenerate live/aws-paginating-operations.json if botocore has grown the service)", e.Service)
}

// Paginates answers whether `aws <cliService> <cliOperation>` is an
// operation botocore declares a paginator for.
//
// cliOperation is the CLI's dashed spelling ("list-policies"); the API
// operation name is its PascalCase form ("ListPolicies"), which is exactly
// how awscli derives the command name in the other direction.
func (p *PaginatingOperations) Paginates(cliService, cliOperation string) (bool, error) {
	dir := cliService
	if renamed, ok := cliServiceRenames[cliService]; ok {
		dir = renamed
	}
	ops, ok := p.Services[dir]
	if !ok {
		return false, ErrUnknownCLIService{Service: cliService}
	}
	want := APIOperationName(cliOperation)
	for _, op := range ops {
		if op == want {
			return true, nil
		}
	}
	return false, nil
}

// APIOperationName turns the AWS CLI's dashed subcommand spelling into the
// API operation name botocore's paginator data is keyed by:
// "list-policy-tags" -> "ListPolicyTags".
func APIOperationName(cliOperation string) string {
	parts := strings.Split(cliOperation, "-")
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}
