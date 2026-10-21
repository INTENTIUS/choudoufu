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

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
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
// runs. Measured before the change: the verdict was verdictOwnObject and
// the old sighting vouched for the address, as #2136 found on Azure. The
// maintainer's ruling (2026-10-20): a sighting no comparison could run on
// is unproven, and vouches for nothing.
//
// No taggable AWS type has a multi-component row whose components supply
// no identity attribute at all (aws_route_table_association is the only
// multi-component one, and it carries no tags), so the tagging leg's
// identity-less claimant is the "no identity attributes in the sighting"
// case here.

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

// TestUnprovenSighting_multiComponentRenameDoesNotVouch: the renamed
// service's old sighting, filed through [fileCandidate] as the tagging leg
// files it. No comparison can run, so the verdict is verdictUncompared:
// nothing is reported (nothing proves a displacement either), it is not an
// orphan, and aws_ecs_service.web stays out of VerifiedDeclared, so the
// projection reads whatever sits at web/svc-new and judges it by its own
// tags. Before #2137 the verdict was verdictOwnObject and the sighting
// vouched (this file's first commit pinned that).
//
// The same holds for a sighting at the declared id itself: with nothing
// compared, being at the right id is not something this pass can see, so
// it does not vouch either. The projection's read of that object is what
// admits it.
func TestUnprovenSighting_multiComponentRenameDoesNotVouch(t *testing.T) {
	if ti, ok := identity.LookupType("aws_ecs_service"); !ok || len(ti.Components) == 1 {
		t.Fatalf("aws_ecs_service's identity row is not multi-component (%+v, %v); this pin no longer exercises the declining fallback", ti, ok)
	}
	for name, svcName := range map[string]string{"old name": unprovenOldSvc, "declared name": unprovenNewSvc} {
		t.Run(name, func(t *testing.T) {
			req, decl, svc := unprovenRequest(t)
			escaped := markers.EscapeAddress(unprovenAddr)

			sighting := unprovenSighting(svcName)
			claim := claimant{importID: sighting.importID, escaped: escaped, marker: escaped, tags: sighting.tags}
			if _, _, compared := compareIdentity("aws_ecs_service", svc, claim); compared {
				t.Fatalf("setup: a comparison ran; this pin is about the case where none can run")
			}
			if _, got := decl.displacedFrom(context.Background(), "aws_ecs_service", escaped, claim); got != verdictUncompared {
				t.Errorf("verdict %d, want verdictUncompared (%d)", got, verdictUncompared)
			}

			res := &Result{}
			diags := fileCandidate(context.Background(), req, decl, "aws_ecs_service", sighting, res, " (Tagging API)", true)
			assertNoErrors(t, diags)
			if n := len(res.ProblemsOfKind(ProblemDisplacedMarker)); n != 0 {
				t.Errorf("%d DISPLACED_MARKER problems, want none: nothing was compared", n)
			}
			assertNotAnOrphan(t, res, sighting.importID)
			if res.MarkerVerified()[unprovenAddr] {
				t.Errorf("an uncompared sighting at %s vouched for %s: %v", sighting.importID, unprovenAddr, res.VerifiedDeclared)
			}
		})
	}
}

// TestUnprovenSighting_comparisonsStillAnswer bounds the change from the
// other side: where a comparison does run, its answer stands. The ECS
// service's claimant carrying an identity object is compared per
// attribute, and agrees or disagrees; aws_s3_bucket's one-component row
// still takes the whole-import-id fallback.
func TestUnprovenSighting_comparisonsStillAnswer(t *testing.T) {
	_, decl, _ := unprovenRequest(t)
	escaped := markers.EscapeAddress(unprovenAddr)
	withIdentity := func(svc string) claimant {
		return claimant{
			importID: unprovenCluster + "/" + svc,
			identity: cty.ObjectVal(map[string]cty.Value{"cluster": cty.StringVal(unprovenCluster), "name": cty.StringVal(svc)}),
			escaped:  escaped,
			marker:   escaped,
		}
	}
	if _, got := decl.displacedFrom(context.Background(), "aws_ecs_service", escaped, withIdentity(unprovenNewSvc)); got != verdictOwnObject {
		t.Errorf("the declared service with a matching identity object: verdict %d, want verdictOwnObject (%d)", got, verdictOwnObject)
	}
	if _, got := decl.displacedFrom(context.Background(), "aws_ecs_service", escaped, withIdentity(unprovenOldSvc)); got != verdictDisplaced {
		t.Errorf("the old service with its identity object: verdict %d, want verdictDisplaced (%d)", got, verdictDisplaced)
	}

	bucket := instanceAddr("aws_s3_bucket", "data", addrs.NoKey)
	be := markers.EscapeAddress(bucket.String())
	d := declaredOf(identity.Resolution{Addr: bucket, Class: identity.ClassConcrete, ImportID: "b-new"})
	if _, got := d.displacedFrom(context.Background(), "aws_s3_bucket", be, claimant{importID: "b-old", escaped: be}); got != verdictDisplaced {
		t.Errorf("aws_s3_bucket at another name: verdict %d, want verdictDisplaced (%d)", got, verdictDisplaced)
	}
	if _, got := d.displacedFrom(context.Background(), "aws_s3_bucket", be, claimant{importID: "b-new", escaped: be}); got != verdictOwnObject {
		t.Errorf("aws_s3_bucket at its own name: verdict %d, want verdictOwnObject (%d)", got, verdictOwnObject)
	}
}
