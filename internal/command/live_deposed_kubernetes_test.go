// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"reflect"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestDeposedRecordAddrsAsksKubernetesAddresses is GitHub issue #1683's
// command half. The Kubernetes two-claimant collision (#1641) consults the
// deposed record the way AWS's does (#361), but the record is read before
// discovery runs, per address the caller names, and #361 names only
// needs-discovery addresses. A Kubernetes address is never one: its
// collision arises at a concrete address whose key is not listed, or at an
// address the static evaluator refused and the node took over (#1539's
// shape). Those are asked too, for a block whose provider the label-list
// sweep serves; an AWS address outside needs-discovery is not, because the
// AWS collision branch reads only needs-discovery entries and a read for
// anything else would be a record GET with no consumer.
func TestDeposedRecordAddrsAsksKubernetesAddresses(t *testing.T) {
	config := liveLsLoadConfig(t, `
provider "kubernetes" {}
resource "kubernetes_config_map" "reader" {
  metadata {
    name      = "r"
    namespace = "ns"
  }
}
resource "kubernetes_config_map" "named" {
  metadata {
    name      = "n"
    namespace = "ns"
  }
}
resource "aws_s3_bucket" "concrete" {
  bucket = "b"
}
resource "aws_vpc" "found" {}
resource "aws_iam_role" "refused" {}
`)
	res := func(addr string, class identity.Class) identity.Resolution {
		a, diags := addrs.ParseAbsResourceInstanceStr(addr)
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		return identity.Resolution{Addr: a, Class: class}
	}
	all := []identity.Resolution{
		res("kubernetes_config_map.named", identity.ClassConcrete),
		res("aws_s3_bucket.concrete", identity.ClassConcrete),
		res("aws_vpc.found", identity.ClassNeedsDiscovery),
	}
	refused := map[string]bool{"kubernetes_config_map.reader": true, "aws_iam_role.refused": true}

	var got []string
	for _, a := range deposedRecordAddrs(config, all, refused) {
		got = append(got, a.String())
	}
	sort.Strings(got)
	want := []string{"aws_vpc.found", "kubernetes_config_map.named", "kubernetes_config_map.reader"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deposed record reads for %v, want %v", got, want)
	}
}
