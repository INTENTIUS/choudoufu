// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
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

	// Digest is sha256 over the canonical rendering of Services alone.
	//
	// It exists because the canonical-form comparison alone does not catch
	// the edit that actually matters. Deleting one operation from the file
	// by hand leaves a file that is still perfectly canonical - Marshal
	// re-renders whatever it parsed - and silently narrows the guard by one
	// call. This was found by running that edit, not by reasoning about it:
	// the offline test passed and only `-check` against botocore noticed.
	//
	// A digest stored beside the data it covers is not proof against
	// someone determined; it converts a one-line deletion from silent into
	// loud, and the cheap way to make it pass again is to run the
	// generator, which is the outcome wanted.
	Digest string `json:"content_sha256"`

	// Services maps a botocore service directory name to that service's
	// paginating operations: API operation name -> the AWS CLI's dashed
	// spelling of it.
	//
	// Both spellings are stored because neither derives reliably from the
	// other. The CLI's name is what a shell call site writes and is what
	// the lookup needs; the API name is what botocore's paginator data is
	// keyed by and what a failure message should print. Turning one into
	// the other by hand is where this first went wrong: a naive PascalCase
	// of "describe-db-instances" is "DescribeDbInstances", the API
	// operation is "DescribeDBInstances", and the mismatch silently
	// classified all 14 of this tree's `rds describe-db-instances --query
	// '...[0]'` call sites as safe. A false NEGATIVE, in a guard whose
	// whole purpose is not to have any.
	//
	// So the CLI spelling is not computed here at all. The generator calls
	// botocore's own xform_name - literally the function awscli uses to
	// name its subcommands - and the result is vendored.
	Services map[string]map[string]string `json:"services"`
}

// ContentDigest is sha256 over the services map rendered canonically, and
// over nothing else: not the generation date, not the botocore version, not
// the file counts. Only the data the guard actually reads.
func (p *PaginatingOperations) ContentDigest() string {
	svcs := make([]string, 0, len(p.Services))
	for svc := range p.Services {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	h := sha256.New()
	for _, svc := range svcs {
		fmt.Fprintf(h, "%s\n", svc)
		ops := p.Services[svc]
		names := make([]string, 0, len(ops))
		for api := range ops {
			names = append(names, api)
		}
		sort.Strings(names)
		for _, api := range names {
			fmt.Fprintf(h, "\t%s\t%s\n", api, ops[api])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
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
		Digest:          p.Digest,
		Services:        p.Services,
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

// ErrUnknownCLIService is returned by Paginates for a CLI service token the
// snapshot has no directory for. It is deliberately an error rather than a
// false: "I do not know this service" and "this operation does not page"
// must not read the same to a guard.
type ErrUnknownCLIService struct{ Service string }

func (e ErrUnknownCLIService) Error() string {
	return fmt.Sprintf("no botocore service directory for the AWS CLI service %q - add it to cliServiceRenames in live/awspaginators.go (or regenerate live/aws-paginating-operations.json if botocore has grown the service)", e.Service)
}

// Paginates answers whether `aws <cliService> <cliOperation>` is an
// operation botocore declares a paginator for. cliOperation is the CLI's
// dashed spelling, e.g. "describe-db-instances".
func (p *PaginatingOperations) Paginates(cliService, cliOperation string) (bool, error) {
	api, err := p.APIName(cliService, cliOperation)
	if err != nil {
		return false, err
	}
	return api != "", nil
}

// APIName returns the API operation name behind a CLI subcommand, or the
// empty string when the service has no paginator for it. The error is
// reserved for an unresolvable SERVICE, which is a different thing and must
// never read as "does not paginate".
func (p *PaginatingOperations) APIName(cliService, cliOperation string) (string, error) {
	dir := cliService
	if renamed, ok := cliServiceRenames[cliService]; ok {
		dir = renamed
	}
	ops, ok := p.Services[dir]
	if !ok {
		return "", ErrUnknownCLIService{Service: cliService}
	}
	for api, cli := range ops {
		if cli == cliOperation {
			return api, nil
		}
	}
	return "", nil
}
