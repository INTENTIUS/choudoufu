// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #2137, the AWS half of #2136's rename question. The block
// keeps its address and its identity argument changes: aws_ecs_service.web
// used to name service "svc-old" in cluster "web" and now names "svc-new".
// The service the last apply made still exists at web/svc-old and still
// carries tofu-address = aws_ecs_service.web, and the Resource Groups
// Tagging API leg lists it.
//
// aws_ecs_service's identity row has three components (cluster, "/",
// name), so [configIdentityMismatch]'s whole-import-id fallback declines,
// and a tagging-leg claimant carries no identity object, so its
// per-attribute path has nothing to compare either. Neither comparison
// runs.

const (
	unprovenCluster = "web"
	unprovenOldSvc  = "svc-old"
	unprovenNewSvc  = "svc-new"
	unprovenAddr    = "aws_ecs_service.web"
)

const unprovenConfig = `
resource "aws_ecs_service" "web" {
  name    = "svc-new"
  cluster = "web"
}
`

// unprovenRequest is the configuration above, loaded and resolved the way
// [Discover] resolves it, with the declared set built over its
// resolutions.
func unprovenRequest(t *testing.T) (Request, *declared, identity.Resolution) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(unprovenConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	all := resolveOrFail(t, cfg).All()
	var svc identity.Resolution
	found := false
	for _, r := range all {
		if r.Addr.String() == unprovenAddr {
			svc, found = r, true
		}
	}
	if !found {
		t.Fatalf("%s did not resolve: %+v", unprovenAddr, all)
	}
	if svc.Class != identity.ClassConcrete || svc.ImportID != unprovenCluster+"/"+unprovenNewSvc {
		t.Fatalf("setup: %s resolved %s at %q, want concrete at %q", unprovenAddr, svc.Class, svc.ImportID, unprovenCluster+"/"+unprovenNewSvc)
	}
	return Request{Estate: estateName, Config: cfg, Resolutions: all}, declaredOf(all...), svc
}

// unprovenSighting is a tagging-leg candidate for one ECS service carrying
// this estate's marker for aws_ecs_service.web.
func unprovenSighting(svc string) taggedCandidate {
	return taggedCandidate{
		importID: unprovenCluster + "/" + svc,
		tags: map[string]string{
			TagEstate:  estateName,
			TagAddress: markers.EscapeAddress(unprovenAddr),
		},
	}
}

// TestUnprovenSighting_multiComponentRenameIsMeasured pins what the
// tagging leg does with the renamed service's old sighting today: the
// verdict is verdictOwnObject, nothing is reported, and [fileCandidate]
// adds aws_ecs_service.web to VerifiedDeclared, so the projection would
// admit whatever sits at web/svc-new without reading its tags.
func TestUnprovenSighting_multiComponentRenameIsMeasured(t *testing.T) {
	if ti, ok := identity.LookupType("aws_ecs_service"); !ok || len(ti.Components) == 1 {
		t.Fatalf("aws_ecs_service's identity row is not multi-component (%+v, %v); this pin no longer exercises the declining fallback", ti, ok)
	}
	req, decl, svc := unprovenRequest(t)
	escaped := markers.EscapeAddress(unprovenAddr)

	old := unprovenSighting(unprovenOldSvc)
	claim := claimant{importID: old.importID, escaped: escaped, marker: escaped, tags: old.tags}
	if _, mismatched := configIdentityMismatch("aws_ecs_service", svc, claim); mismatched {
		t.Fatalf("setup: a comparison ran and mismatched; this pin is about the case where none can run")
	}
	if _, got := decl.displacedFrom(context.Background(), "aws_ecs_service", escaped, claim); got != verdictOwnObject {
		t.Errorf("verdict %d, want verdictOwnObject (%d)", got, verdictOwnObject)
	}

	res := &Result{}
	diags := fileCandidate(context.Background(), req, decl, "aws_ecs_service", old, res, " (Tagging API)", true)
	assertNoErrors(t, diags)
	if n := len(res.ProblemsOfKind(ProblemDisplacedMarker)); n != 0 {
		t.Errorf("%d DISPLACED_MARKER problems, want none today", n)
	}
	assertNotAnOrphan(t, res, old.importID)
	if !res.MarkerVerified()[unprovenAddr] {
		t.Errorf("web/svc-old's sighting did not vouch for %s: %v", unprovenAddr, res.VerifiedDeclared)
	}
}
