// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package largeset is the multi-estate fixture for large change sets (issue
// #1750, part of epic #1749): N estate roots, each with its own estate
// declaration, all calling one shared module, with a module bump from
// version A to version B to measure.
//
// It owns three things and nothing else:
//
//   - the rendering ([Render], [Write]) that tools/largeset-gen prints to a
//     directory, deterministically, so the same inputs give byte-identical
//     output;
//   - the module package ([ModulePackage]) the OCI variant publishes to a
//     local registry at each version, and the publisher that pushes it;
//   - the baseline record ([BaselineRecord], [GateBaseline]) the floci run
//     writes, in the same gated-record pattern as
//     internal/live/statefulcost's steadyrecord.go, so the epic's later
//     units (#1752, #1753, #1754) compare against a figure that was refused
//     rather than written whenever its conditions did not hold.
//
// # Shape
//
// Estates are numbered e01..eNN and take a role from their position in a
// block of five, so the shape holds at any N and N=5 is exactly one block:
//
//	position 1  producer  declares an aws_vpc at the root
//	position 2  middle    reads position 1's VPC through a data source
//	                      filtered on its tofu-estate/tofu-address markers
//	                      (live/OUTPUTS.md, the #561 shape), declares a subnet
//	position 3  leaf      reads position 2's subnet the same way and writes
//	                      its ID onto a queue's tag: a chain of depth 2
//	position 4  outlier   calls the shared module with with_queue = true, so
//	                      it owns one more resource and its plan for the bump
//	                      has one more change than every other estate's
//	position 5  plain     calls the shared module and nothing else
//
// Every estate calls the shared module. Version B of the module changes the
// work queue's visibility timeout, changes the version tag on every resource
// the module owns, and adds one queue, so the bump moves every estate and
// moves the outlier more.
//
// # Two sources for one module
//
// [SourceLocal] writes the module under modules/shared and every root calls
// it by relative path: bumping it is rewriting that one directory, which
// moves every root at once. [SourceOCI] writes no module directory; every
// root calls oci://<registry>/largeset/shared?tag=<version>, and
// [Options.PinB] names the roots pinned at B rather than A, so a pin bump
// across a subset of roots is a regeneration with a longer PinB. That second
// variant exists because of the 2026-10-01 thread on #1749: a pinned source
// makes "which roots does this change move" a plain path diff, and this
// fork's live path had never been run against an oci:// module at all.
package largeset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source is how the roots reach the shared module.
type Source string

const (
	// SourceLocal is a relative-path module call to modules/shared.
	SourceLocal Source = "local"
	// SourceOCI is a pinned oci:// module call to a registry.
	SourceOCI Source = "oci"
)

// Version is one of the shared module's two versions.
type Version string

const (
	VersionA Version = "A"
	VersionB Version = "B"
)

// OCITag is the registry tag a version is published under. Semver-shaped on
// purpose: the pin a reviewer reads in a diff is "1.0.0 -> 1.1.0", which is
// the reason string #1751 is asked to print for a pin bump.
func (v Version) OCITag() string {
	if v == VersionB {
		return "1.1.0"
	}
	return "1.0.0"
}

// OCIRepository is the repository path, under the registry, the module is
// published to.
const OCIRepository = "largeset/shared"

// ProviderVersion is the hashicorp/aws release every root pins. It matches
// internal/live/pins.AWSProviderVersion; pins_test.go holds the two equal so
// the plugin cache the floci tier warms serves this fixture too.
const ProviderVersion = "6.59.0"

// Role is an estate's part in the shape.
type Role string

const (
	RoleProducer Role = "producer"
	RoleMiddle   Role = "middle"
	RoleLeaf     Role = "leaf"
	RoleOutlier  Role = "outlier"
	RolePlain    Role = "plain"
)

// blockRoles is the role of each position in a block of five.
var blockRoles = [5]Role{RoleProducer, RoleMiddle, RoleLeaf, RoleOutlier, RolePlain}

// Options are the generator's inputs. The rendering is a pure function of
// them.
type Options struct {
	// Estates is N, at least 1. The shape's chain needs 3 and its outlier
	// needs 4; N=5 is one full block.
	Estates int
	// ModuleVersion is the shared module's version: the content written to
	// modules/shared for [SourceLocal], and the pin of every root not in
	// PinB for [SourceOCI].
	ModuleVersion Version
	// Prefix starts every name the fixture creates in the account and every
	// estate name, so two fixtures can share an emulator. Default "ls".
	Prefix string
	// Source selects the module source kind. Default [SourceLocal].
	Source Source
	// Registry is the OCI registry's host:port, for [SourceOCI] only.
	Registry string
	// PinB lists estates (e01, e02, ...) pinned at version B, for
	// [SourceOCI] only: the subset a pin-bump wave moves.
	PinB []string
}

func (o Options) normalized() (Options, error) {
	if o.Prefix == "" {
		o.Prefix = "ls"
	}
	if o.Source == "" {
		o.Source = SourceLocal
	}
	if o.Estates < 1 {
		return o, fmt.Errorf("-estates must be at least 1, got %d", o.Estates)
	}
	if o.Estates > 99 {
		return o, fmt.Errorf("-estates must be at most 99, got %d: estate names are two digits", o.Estates)
	}
	switch o.ModuleVersion {
	case VersionA, VersionB:
	default:
		return o, fmt.Errorf("-module-version must be A or B, got %q", o.ModuleVersion)
	}
	if !validPrefix(o.Prefix) {
		return o, fmt.Errorf("-prefix %q must be 1-8 lowercase letters or digits, starting with a letter: it becomes part of S3 bucket and IAM role names", o.Prefix)
	}
	switch o.Source {
	case SourceLocal:
		if o.Registry != "" || len(o.PinB) > 0 {
			return o, errors.New("-registry and -pin-b apply to -source oci only; a local module is bumped by -module-version")
		}
	case SourceOCI:
		if o.Registry == "" {
			return o, errors.New("-source oci needs -registry host:port")
		}
		seen := map[string]bool{}
		pins := make([]string, 0, len(o.PinB))
		for _, e := range o.PinB {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			n, ok := estateIndex(e)
			if !ok || n > o.Estates {
				return o, fmt.Errorf("-pin-b names %q, which is not an estate of this fixture (e01..e%02d)", e, o.Estates)
			}
			if !seen[e] {
				seen[e] = true
				pins = append(pins, e)
			}
		}
		sort.Strings(pins)
		o.PinB = pins
	default:
		return o, fmt.Errorf("-source must be local or oci, got %q", o.Source)
	}
	return o, nil
}

func validPrefix(p string) bool {
	if len(p) < 1 || len(p) > 8 || p[0] < 'a' || p[0] > 'z' {
		return false
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func estateIndex(name string) (int, bool) {
	if len(name) != 3 || name[0] != 'e' {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(name[1:], "%02d", &n); err != nil || n < 1 {
		return 0, false
	}
	return n, fmt.Sprintf("e%02d", n) == name
}

// Estate is one root of the fixture, as the manifest records it.
type Estate struct {
	// Name is the directory name under estates/, e01..eNN.
	Name string `json:"name"`
	// Dir is the root's path relative to the fixture directory.
	Dir string `json:"dir"`
	// Estate is the tofu-estate value its sidecar declares.
	Estate string `json:"estate"`
	Role   Role   `json:"role"`
	// Reads names the estates whose resources this one reads through a
	// marker-filtered data source; an apply must follow theirs.
	Reads []string `json:"reads,omitempty"`
	// ModuleVersion is the version this root's module call resolves to.
	ModuleVersion Version `json:"module_version"`
	// ModuleSource is the module call's source argument, verbatim.
	ModuleSource string `json:"module_source"`
}

// Manifest is fixture.json: what the generator wrote and why, so a harness
// reads the apply order and the roles instead of re-deriving them.
type Manifest struct {
	Schema int    `json:"schema"`
	Issue  string `json:"issue"`
	Source Source `json:"source"`
	Prefix string `json:"prefix"`
	// ModuleVersion is the -module-version the fixture was generated with.
	ModuleVersion Version  `json:"module_version"`
	Registry      string   `json:"registry,omitempty"`
	PinB          []string `json:"pin_b,omitempty"`
	Estates       []Estate `json:"estates"`
	// ApplyOrder is every estate's Dir in an order where each one follows
	// every estate it reads.
	ApplyOrder []string `json:"apply_order"`
	// Files is every path this rendering owns, relative to the fixture
	// directory. [Write] removes a previous rendering's files that the new
	// one no longer owns, and touches nothing else (.terraform, record
	// stores, caches stay).
	Files []string `json:"files"`
}

// ManifestSchema is fixture.json's version.
const ManifestSchema = 1

// ManifestFile is the manifest's name in the fixture directory.
const ManifestFile = "fixture.json"

// ApplyOrderFile is the apply order, one root directory per line, for a
// shell harness that should not need a JSON parser.
const ApplyOrderFile = "apply-order.txt"

// Render returns every file of the fixture keyed by its slash-separated path
// relative to the fixture directory, and the manifest describing them.
func Render(o Options) (map[string][]byte, Manifest, error) {
	o, err := o.normalized()
	if err != nil {
		return nil, Manifest{}, err
	}
	files := map[string][]byte{}
	m := Manifest{
		Schema: ManifestSchema, Issue: "#1750",
		Source: o.Source, Prefix: o.Prefix, ModuleVersion: o.ModuleVersion,
		Registry: o.Registry, PinB: o.PinB,
	}
	if o.Source == SourceLocal {
		for name, body := range ModulePackage(o.ModuleVersion) {
			files["modules/shared/"+name] = body
		}
	}
	pinB := map[string]bool{}
	for _, e := range o.PinB {
		pinB[e] = true
	}
	for i := 1; i <= o.Estates; i++ {
		e := estateAt(o, i)
		if o.Source == SourceOCI {
			v := VersionA
			if o.ModuleVersion == VersionB || pinB[e.Name] {
				v = VersionB
			}
			e.ModuleVersion = v
			e.ModuleSource = fmt.Sprintf("oci://%s/%s?tag=%s", o.Registry, OCIRepository, v.OCITag())
		} else {
			e.ModuleVersion = o.ModuleVersion
			e.ModuleSource = "../../modules/shared"
		}
		files[e.Dir+"/main.tf"] = renderRoot(o, i, e)
		files[e.Dir+"/estate.chdf.hcl"] = renderSidecar(e)
		m.Estates = append(m.Estates, e)
	}
	m.ApplyOrder = applyOrder(m.Estates)
	var order bytes.Buffer
	for _, d := range m.ApplyOrder {
		order.WriteString(d + "\n")
	}
	files[ApplyOrderFile] = order.Bytes()

	for p := range files {
		m.Files = append(m.Files, p)
	}
	m.Files = append(m.Files, ManifestFile)
	sort.Strings(m.Files)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, Manifest{}, err
	}
	files[ManifestFile] = append(b, '\n')
	return files, m, nil
}

// estateAt is the i'th estate (1-based) without its module source filled in.
func estateAt(o Options, i int) Estate {
	name := fmt.Sprintf("e%02d", i)
	e := Estate{
		Name:   name,
		Dir:    "estates/" + name,
		Estate: o.Prefix + "-" + name,
		Role:   blockRoles[(i-1)%5],
	}
	switch e.Role {
	case RoleMiddle:
		e.Reads = []string{fmt.Sprintf("e%02d", i-1)}
	case RoleLeaf:
		e.Reads = []string{fmt.Sprintf("e%02d", i-1)}
	}
	return e
}

// applyOrder is a topological order over Reads, ties broken by name, so it is
// deterministic and a consumer always follows its producer.
func applyOrder(estates []Estate) []string {
	byName := map[string]Estate{}
	for _, e := range estates {
		byName[e.Name] = e
	}
	done := map[string]bool{}
	var out []string
	var visit func(name string)
	visit = func(name string) {
		if done[name] {
			return
		}
		done[name] = true
		for _, r := range byName[name].Reads {
			visit(r)
		}
		out = append(out, byName[name].Dir)
	}
	names := make([]string, 0, len(estates))
	for _, e := range estates {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		visit(n)
	}
	return out
}

// block is the 0-based block of five an estate index falls in; each block
// gets its own VPC CIDR so the chains of a larger fixture never overlap.
func block(i int) int { return (i - 1) / 5 }

func vpcCIDR(i int) string    { return fmt.Sprintf("10.%d.0.0/16", 100+block(i)) }
func subnetCIDR(i int) string { return fmt.Sprintf("10.%d.1.0/24", 100+block(i)) }

const generatedHeader = "# Generated by tools/largeset-gen (issue #1750). Do not edit; regenerate.\n"

func renderSidecar(e Estate) []byte {
	return []byte(generatedHeader + fmt.Sprintf(`#
# The estate declaration for root %s (role: %s). One estate per root: two
# roots sharing a tofu-estate value would each see the other's resources as
# undeclared and propose destroying them.
estate = %q
`, e.Name, e.Role, e.Estate))
}

func renderRoot(o Options, i int, e Estate) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	fmt.Fprintf(&b, `#
# Root %s, role %s.

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= %s"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

module "shared" {
  source = %q

  name   = %q
  prefix = %q
`, e.Name, e.Role, ProviderVersion, e.ModuleSource, e.Name, o.Prefix)
	if e.Role == RoleOutlier {
		b.WriteString("\n  # The outlier: one more resource than every other estate, so its plan\n  # for the bump differs from the group's.\n  with_queue = true\n")
	}
	b.WriteString("}\n")

	switch e.Role {
	case RoleProducer:
		fmt.Fprintf(&b, `
# The head of this block's cross-estate chain: the next estate reads this VPC
# through its markers.
resource "aws_vpc" "network" {
  cidr_block = %q
}
`, vpcCIDR(i))
	case RoleMiddle:
		up := o.Prefix + "-" + e.Reads[0]
		fmt.Fprintf(&b, `
# Reads %s's VPC through its ownership markers (live/OUTPUTS.md), the first
# link of this block's chain.
data "aws_vpc" "upstream" {
  filter {
    name   = "tag:tofu-estate"
    values = [%q]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_vpc.network"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.upstream.id
  cidr_block = %q
}
`, up, up, subnetCIDR(i))
	case RoleLeaf:
		up := o.Prefix + "-" + e.Reads[0]
		fmt.Fprintf(&b, `
# Reads %s's subnet through its ownership markers: the second link, so this
# estate sits at depth 2 of the chain.
data "aws_subnet" "upstream" {
  filter {
    name   = "tag:tofu-estate"
    values = [%q]
  }
  filter {
    name   = "tag:tofu-address"
    values = ["aws_subnet.app"]
  }
}

resource "aws_sqs_queue" "downstream" {
  name = "%s-%s-downstream"
  tags = {
    "upstream-subnet" = data.aws_subnet.upstream.id
  }
}
`, up, up, o.Prefix, e.Name)
	}
	return []byte(b.String())
}

// ModulePackage is the shared module at version v, keyed by file name. The
// local variant writes it under modules/shared; the OCI variant zips it into
// the package it publishes.
func ModulePackage(v Version) map[string][]byte {
	var b strings.Builder
	b.WriteString(generatedHeader)
	fmt.Fprintf(&b, `#
# The shared module, version %s. Every root of the fixture calls it.

variable "name" {
  type = string
}

variable "prefix" {
  type = string
}

variable "with_queue" {
  type    = bool
  default = false
}

locals {
  module_version = %q
  tags = {
    "largeset-module" = local.module_version
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "${var.prefix}-${var.name}-data"
  tags   = local.tags
}

resource "aws_sqs_queue" "work" {
  name                       = "${var.prefix}-${var.name}-work"
  visibility_timeout_seconds = %d
  tags                       = local.tags
}

resource "aws_iam_role" "app" {
  name = "${var.prefix}-${var.name}-app"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
  tags = local.tags
}

resource "aws_sqs_queue" "extra" {
  count = var.with_queue ? 1 : 0
  name  = "${var.prefix}-${var.name}-extra"
  tags  = local.tags
}
`, v, string(v), visibilityTimeout(v))
	if v == VersionB {
		b.WriteString(`
# New in B: one more queue per estate.
resource "aws_sqs_queue" "events" {
  name = "${var.prefix}-${var.name}-events"
  tags = local.tags
}
`)
	}
	return map[string][]byte{"main.tf": []byte(b.String())}
}

// visibilityTimeout is the one argument B changes in place.
func visibilityTimeout(v Version) int {
	if v == VersionB {
		return 60
	}
	return 30
}

// Digest is a stable hash over a rendering: every path and its bytes, in path
// order. The byte-identity test pins it.
func Digest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(files[p]))
		h.Write(files[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Write renders o into dir. It overwrites every file the rendering owns and
// removes the files a previous rendering in dir owned that this one does not,
// and touches nothing else: an applied estate's .terraform directory and
// record store survive a bump, which is the point of regenerating in place.
//
// A non-empty dir with no fixture.json is refused rather than written into,
// so a mistyped -out cannot scatter files over somebody else's directory.
func Write(dir string, o Options) (Manifest, error) {
	files, m, err := Render(o)
	if err != nil {
		return Manifest{}, err
	}
	prev, err := previousFiles(dir)
	if err != nil {
		return Manifest{}, err
	}
	for _, p := range prev {
		if _, keep := files[p]; keep {
			continue
		}
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, err
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Manifest{}, err
		}
		if err := os.WriteFile(full, files[p], 0o644); err != nil {
			return Manifest{}, err
		}
	}
	return m, nil
}

func previousFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		if len(entries) > 0 {
			return nil, fmt.Errorf("%s is not empty and holds no %s: refusing to write a fixture over it", dir, ManifestFile)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("reading the previous %s: %w", ManifestFile, err)
	}
	for _, p := range m.Files {
		if strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			return nil, fmt.Errorf("the previous %s lists %q, which is not a path inside the fixture", ManifestFile, p)
		}
	}
	return m.Files, nil
}

// ReadManifest reads dir's fixture.json.
func ReadManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, err
	}
	if m.Schema != ManifestSchema {
		return Manifest{}, fmt.Errorf("%s has schema %d; this build reads %d", ManifestFile, m.Schema, ManifestSchema)
	}
	return m, nil
}
