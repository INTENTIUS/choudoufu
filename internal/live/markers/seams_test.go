// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The completeness guard of GitHub issue #1118: every function in the tree
// that asks a schema which marker surface it carries, reads a marker off an
// object, names a marker's path, or acts on a Surface value by naming its
// constants must handle every surface this package
// declares, or be listed in surfaceSeamExemptions (or, for what was found
// on the day the guard landed, surfaceSeamUntriaged) with exactly the
// surfaces it handles.
//
// Nothing else here is typed by hand. The surfaces are this package's
// Surface constants; which function belongs to which surface is the
// //markers:surface directive on it; the seams are every non-test function
// under the scanned roots that references a member, directly or through
// one call to a wrapper that does (liveimport's taggable, labelSurface and
// manifestSurface are the reason for the second clause: #1109's hole was a
// dispatch that only ever called wrappers). What a seam handles is what
// its body references, plus what its callees in the same package handle,
// transitively, plus what callees in other packages reference themselves.
//
// The three holes of 2026-09-13 are the ones this has to name, and the
// PR that added it shows it naming each against a scratch revert:
// checkOwnership reading markers.TagsOf and nothing else (#1108),
// ratifyOne asking taggable and labelSurface but not manifestSurface
// (#1109), and live-mv's surfaceOf without its ManifestSurface arm
// (#1104).
//
// It reads source with go/parser alone: no type checking and no build, so
// it costs about a second and sees a package that does not compile. The
// price is that resolution is syntactic: x.m() resolves to every method
// named m in the calling package, and an identifier to the package-level
// function of that name. That can only over-approximate what a seam
// handles. It also cannot see a surface asked without a member - a raw
// block.Attributes["tags"] lookup, or identity.ObjectMetaShape, which is
// a label-shape predicate of its own - which is why the ownership read's
// tag arm asked a member of this package (markers.HasTagsAttribute) when
// this landed. Issue #1589 later deleted that member: the ownership read
// now asks substrate.SurfaceOf like every other caller, once the two
// questions were shown to agree on every admitted type.
// TestSurfaceSeamGuardSeesTheFixture pins every resolution rule the guard
// relies on against a tree whose answers are known.

// surfaceSeamRoots are the directories scanned for seams, relative to the
// module root.
var surfaceSeamRoots = []string{"internal", "pkg", "cmd", "tools"}

// surfaceSeamExemption records a seam that handles some surfaces and not
// others on purpose. Handles is exactly the set the guard measures: an
// entry bounds WHAT is excused, so a seam that gains or loses a surface
// fails until its entry is revisited, and an entry whose seam no longer
// exists fails as stale.
type surfaceSeamExemption struct {
	Handles []Surface
	Why     string
}

// discoveryAWSLeg is the reason the AWS discovery files are exempt
// (GitHub issues #1580, #1614). Their sweep is discovery.TaggingIndexSweep
// (sweeper.go); the Kubernetes sweep is discovery.KubernetesSweep in
// kubernetes.go. Both are discovery.Sweeper legs, and internal/command
// hands Discover the legs in Request.Sweepers by each provider family's
// substrate.Sweep property (sweepLegBuilders and liveSweepLegs in
// live_plan.go), never by marker surface, so the choice of leg is not a
// seam this guard measures.
const discoveryAWSLeg = "an AWS discovery leg (tagging index, list, Cloud Control, direct read), run as discovery.TaggingIndexSweep. The Kubernetes leg is discovery.KubernetesSweep in kubernetes.go; internal/command picks a pass's discovery.Sweeper legs by the provider family's substrate.Sweep property (sweepLegBuilders in internal/command/live_plan.go), not by surface, so the choice is not a seam this guard measures"

// discoveryAzureLeg is the reason the Azure discovery leg is exempt
// (GitHub issue #1120): the discoveryAWSLeg reason, for the Resource Graph
// leg.
const discoveryAzureLeg = "the Azure discovery leg (Resource Graph), run as discovery.ResourceGraphSweep: it reads the ARM tag map case-insensitively and files each resource through the tag sweep's own fileCandidate. internal/command picks a pass's discovery.Sweeper legs by the provider family's substrate.Sweep property (sweepLegBuilders in internal/command/live_plan.go), not by surface, so the choice is not a seam this guard measures"

// discoveryDataPlaneLeg is the reason the Azure data-plane discovery leg is
// exempt (GitHub issue #2205): the discoveryAzureLeg reason, for the Key
// Vault listing that Resource Graph does not index.
const discoveryDataPlaneLeg = "the Azure data-plane discovery leg, run as discovery.DataPlaneSweep: it reads the ARM tag map case-insensitively (markers.CanonicalARMMarkers) off Key Vault keys, secrets and certificates and files each through the tag sweep's own fileCandidate. internal/command picks a pass's discovery.Sweeper legs by the provider family's substrate.Sweep property (sweepLegBuilders in internal/command/live_plan.go), not by surface, so the choice is not a seam this guard measures"

// sweepFieldCredit is why the AWS discovery seams that read a Sweep field
// are measured handling the ARM tag surface too.
const sweepFieldCredit = "credited with the Azure leg's arm-tags arm by name alone: it reads a Sweep field (Request.Sweep or TypeScan.Sweep), which this guard's syntactic resolution takes for every method named Sweep in the package, discovery.ResourceGraphSweep.Sweep among them (GitHub issue #1120). Otherwise " + discoveryAWSLeg

// synthConfigLabelArm is why the synthetic-configuration builders handle
// the label surface alone.
const synthConfigLabelArm = "the synthetic configurations a provider-planned marker write offers (epic #1885): markers.AssertMetadataMaps keeps a label-surface object's metadata labels and annotations claimed under either claim, and answers cfg unchanged for any other shape. A tag-surface write asserts its tags argument through configValue's assertedTagAttr without a markers member, and the manifest shape never plans through the provider"

// armTagFoldHelper is why the case-folding helpers of the ARM tag surface
// handle it alone.
const armTagFoldHelper = "a helper of the ARM tag map's case-insensitive keys (GitHub issue #1120): it reads or writes a marker key under the spelling a configuration or object already uses, and is reached only from a seam's ARM arm or from a tags-map path shared with AWS that passes fold false"

// armBodyHelper is why the azapi body-tags helpers of live-import's adopt
// write and live-mv's rewrite handle the ARM surface alone.
const armBodyHelper = "a helper of an azapi_resource whose configuration keeps its tags in body.tags (GitHub issue #1120 unit 19): azapi sends body.tags and ignores the top-level argument then, so the tag path writes or compares the body there. It is reached only from a tag-path seam's ARM arm, chosen by projection.ARMTagPlacement, and with inBody false it is the tag path's own changedOutsideTags"

// armTagsNeedNoSeed is why the projection's read preparation handles every
// surface but the ARM one.
const armTagsNeedNoSeed = "its surface handling is the per-surface seed of the prior state a read is given: configuredTagsSeed's AWS default_tags (tags_all) merge, the label shape's metadata seed and the manifest shape's partial seed. Neither azurerm nor azapi has a tags_all attribute or provider-level default tags merged into one, so an ARM tags map has nothing to seed, and configuredTagsSeed already answers false for every ARM schema (GitHub issue #1120)"

// armTagsAreTaggable is why the seams that ask markers.Taggable need no
// ARM arm of their own.
const armTagsAreTaggable = "markers.ARMTagSurface is markers.TagSurface with a no-tags_all clause added (markers/armtags.go), so every ARM-surface type answers markers.Taggable and takes this seam's tag arm, with the behaviour azurerm had before it had a family (GitHub issue #1120)"

// surfaceSeamExemptions is keyed by a file, relative to the module root,
// which excuses every seam in that file with the same Handles, or by
// "<file>:<Func>" / "<file>:<Recv>.<Func>" for one seam. The unit is the
// file or the function and never the package: #1108's hole sat in a
// package whose other files handled every surface.
var surfaceSeamExemptions = map[string]surfaceSeamExemption{
	"internal/live/discovery/directread.go":                                                    {Handles: []Surface{SurfaceTags}, Why: discoveryAWSLeg},
	"internal/live/discovery/discovery.go":                                                     {Handles: []Surface{SurfaceTags}, Why: discoveryAWSLeg},
	"internal/live/discovery/lookalikerelist.go":                                               {Handles: []Surface{SurfaceTags}, Why: discoveryAWSLeg},
	"internal/live/discovery/reconcile.go":                                                     {Handles: []Surface{SurfaceTags}, Why: discoveryAWSLeg},
	"internal/live/discovery/sweepconcurrency.go":                                              {Handles: []Surface{SurfaceTags}, Why: discoveryAWSLeg},
	"internal/live/discovery/tagging.go":                                                       {Handles: []Surface{SurfaceTags}, Why: discoveryAWSLeg},
	"internal/live/discovery/dataplane.go":                                                     {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: discoveryDataPlaneLeg},
	"internal/live/discovery/resourcegraph.go":                                                 {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: discoveryAzureLeg},
	"internal/live/discovery/cloudcontrol.go:scanTypeCloudControl":                             {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/directread.go:directReadFallback":                                 {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/discovery.go:Discover":                                            {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/discovery.go:discover":                                            {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/result.go:TypeScan.String":                                        {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/sweeper.go:TaggingIndexSweep.Sweep":                               {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/discovery.go:scanType":                                            {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/discovery.go:scanTypeReporting":                                   {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/lookalikerelist.go:relistForLookalikes":                           {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/recordorphan_read.go:recordOrphanReadSweep":                       {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/servicelist.go:scanTypeServiceList":                               {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/tagindexfallback.go:sweepTagIndexFallback":                        {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/tagging.go:sweepViaTagging":                                       {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: sweepFieldCredit},
	"internal/live/discovery/conelegs.go:ResourceGraphSweep.sweepCone":                         {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: "the Azure cone leg (INTENTIUS/choudoufu#2260): the Resource Graph query of " + discoveryAzureLeg + ", narrowed to the cone's ARM types for live-plan -resource. Its GCP and Kubernetes siblings in this file are GCPAssetSweep.sweepCone and KubernetesSweep.sweepCone, picked the same way"},
	"internal/live/discovery/conelegs.go:coneMark.settle":                                      {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: "a cone leg's bookkeeping (INTENTIUS/choudoufu#2260), shared by the Kubernetes and Azure cone legs: it keeps the leg's scans of the cone's types and drops the orphans and sweep coverage the leg filed, and reads no marker. It is credited with the ARM and tag arms by name alone, since it sets TypeScan.Sweep, which this guard's syntactic resolution takes for every method named Sweep in the package (discovery.ResourceGraphSweep.Sweep, discovery.TaggingIndexSweep.Sweep)"},
	"internal/command/live_plan_resourcegraph.go":                                              {Handles: []Surface{SurfaceARMTags}, Why: "the Resource Graph leg's untaggable-type list (GitHub issue #2119): it classifies only declared azurerm and azapi types, the family's substrate.ProviderTypes, chosen by the family's substrate.Sweep property in sweepLegBuilders, never by surface; it is classified here rather than in discovery.ResourceGraphSweep.Sweep because this guard resolves x.Sweep() to every method named Sweep"},
	"internal/command/live_ls_resourcegraph.go":                                                {Handles: []Surface{SurfaceARMTags}, Why: "live-ls's Azure listing (GitHub issue #1120), the inventory beside the Resource Graph leg: it lists only through azurerm and azapi provider configurations, chosen by the family's substrate.Sweep property in liveLsSubstrates, never by surface"},
	"internal/command/live_apply_kubernetes_held.go":                                           {Handles: []Surface{SurfaceManifest}, Why: "the Kubernetes sweep's type universe; its object-metadata arm asks identity.ObjectMetaShape, a label-shape predicate outside this package that the guard cannot see (named in the #1118 follow-up)"},
	"internal/command/live_plan_kubernetes_fieldowners.go":                                     {Handles: []Surface{SurfaceManifest}, Why: "the Kubernetes field-granular boundary (#1191, #1885); a whole object's identity is read off a typed resource's metadata block by the schema directly, or off a manifest-surface value - a tag-surface type never reaches a cluster"},
	"internal/command/live_plan_kubernetes_dryrun.go":                                          {Handles: []Surface{SurfaceLabels, SurfaceManifest}, Why: "the Kubernetes server-side dry run (#1101); a tag-surface type never reaches a cluster"},
	"internal/live/substrate/identity.go:kubernetes.SynthesizeIdentity":                        {Handles: []Surface{SurfaceManifest}, Why: "the Kubernetes family's natural-key identity (#1586), not a marker read: its object-metadata arm asks substrate.ObjectMetaShape, a label-shape predicate the guard cannot see, and a tag-surface type is aws.SynthesizeIdentity's, asked after it in substrate.All"},
	"internal/live/substrate/identity.go:SynthesizeIdentity":                                   {Handles: []Surface{SurfaceARMTags, SurfaceManifest}, Why: "the dispatch of the admission question over substrate.All (#1742 moved it here from internal/live/identity's synthesizeTypeIdentity): it asks each family's SynthesizeIdentity and is credited with kubernetes.SynthesizeIdentity's manifest arm and azure.SynthesizeIdentity's azapi_resource shape test (#1120, markers.ARMTaggable), but it reads no marker; every surface's type is answered, by its own family or by aws.SynthesizeIdentity's catch-all, which TestTheCatchAllFamilyIsLast holds last"},
	"internal/live/liveimport/labels.go":                                                       {Handles: []Surface{SurfaceLabels}, Why: "the label carrier, reached only through ratifyOne's and stamp.go's surface dispatch"},
	"internal/live/liveimport/manifest.go":                                                     {Handles: []Surface{SurfaceManifest}, Why: "the manifest carrier (#1109), reached only through ratifyOne's surface dispatch"},
	"internal/live/liveimport/tags.go":                                                         {Handles: []Surface{SurfaceTags}, Why: "the tag carrier, reached only through ratifyOne's and stamp.go's surface dispatch"},
	"internal/live/liveimport/stamp.go:approveOne":                                             {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "its manifest arm is approveManifest, which reads and patches the live object through the cluster API (unstructured maps, kubesweep.PatchMarkers) and so names no markers member the guard can see; #1639's address annotation made the label arm visible here, and #1120's case-insensitive keys the ARM arm"},
	"internal/live/liveimport/stamp.go:Ratification.entryWork":                                 {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "credited with approveOne's arms; see that entry for why its manifest arm is invisible"},
	"internal/live/liveimport/stamp.go:eligible.markerTags":                                    {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: "the tag carrier's reader, reached only from approveOne's tag path, which the AWS and ARM tag maps share (#1120)"},
	"internal/live/mv/mv.go:Move":                                                              {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "sends SurfaceManifest to mv/manifest.go's rewriteManifest, the API merge patch (#1104), which names no markers member the guard can see; the ARM tag map takes the tag path with its keys folded (#1120)"},
	"internal/live/mv/mv.go:mover.sweep":                                                       {Handles: []Surface{SurfaceARMTags}, Why: "credited with listedMarkerTags' ARM arm: the list sweep folds marker keys on the ARM tag map only (#2133 finding 3); on every other surface it reads the listed map exactly as before"},
	"internal/live/mv/rewrite.go:mover.listedMarkerTags":                                       {Handles: []Surface{SurfaceARMTags}, Why: "the sweep's reader: folds marker keys to their canonical spelling on the ARM tag map, where keys are case-insensitive (#2133 finding 3), and leaves every other surface's map as listed"},
	"internal/live/mv/rewrite.go:mover.markerTags":                                             {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: "the tag path's reader, reached only on the AWS and ARM tag maps (#1120)"},
	"internal/live/mv/rewrite.go:mover.verify":                                                 {Handles: []Surface{SurfaceARMTags, SurfaceTags}, Why: "the tag path's read-back, reached only on the AWS and ARM tag maps; the label path verifies in mv/label.go"},
	"internal/live/mv/rewrite.go:deleteMarkerTag":                                              {Handles: []Surface{SurfaceARMTags}, Why: armTagFoldHelper},
	"internal/live/projection/nodestamp.go:NodeResolver.stampMarkerElems":                      {Handles: []Surface{SurfaceARMTags}, Why: armTagFoldHelper},
	"internal/live/projection/nodestamp.go:markerKeySpellings":                                 {Handles: []Surface{SurfaceARMTags}, Why: armTagFoldHelper},
	"internal/live/projection/nodestamp.go:setMarkerElem":                                      {Handles: []Surface{SurfaceARMTags}, Why: armTagFoldHelper},
	"internal/live/untag/release.go:writer.lookup":                                             {Handles: []Surface{SurfaceARMTags}, Why: armTagFoldHelper},
	"internal/live/projection/build.go:builder.prepareRead":                                    {Handles: []Surface{SurfaceGoogleLabels, SurfaceLabels, SurfaceManifest, SurfaceTags}, Why: armTagsNeedNoSeed},
	"internal/live/projection/readconcurrency.go:builder.readFor":                              {Handles: []Surface{SurfaceGoogleLabels, SurfaceLabels, SurfaceManifest, SurfaceTags}, Why: "credited with prepareRead's seeds; " + armTagsNeedNoSeed},
	"internal/live/projection/readconcurrency.go:builder.startReadPrefetch":                    {Handles: []Surface{SurfaceGoogleLabels, SurfaceLabels, SurfaceManifest, SurfaceTags}, Why: "credited with prepareRead's seeds; " + armTagsNeedNoSeed},
	"internal/command/live_ls.go:liveLsArmRung":                                                {Handles: []Surface{SurfaceARMTags}, Why: "live-ls's rung for the Azure family (GitHub issue #1120): liveLsRung calls it only for an instance whose block's provider substrate.ForProvider puts in substrate.Azure, chosen by provider and never by surface, so it asks the stamp's own ARM questions (markers.ARMTaggable, projection.ARMTagPlacement) and the other surfaces stay in liveLsRung's own arms"},
	"internal/live/mv/rewrite.go:mover.checkPlan":                                              {Handles: []Surface{SurfaceARMTags, SurfaceLabels}, Why: "judges a provider plan: the label arm's changedOutsideLabels reads markers.AnnotationsChangedBesides (#1639), the tag arm's changedOutsideARMTags compares attributes without a markers member on AWS and leaves an azapi body's tags out of the comparison on ARM (#1120 unit 19), and the manifest shape never plans through the provider (mv/manifest.go judges a server dry run instead)"},
	"internal/live/mv/rewrite.go:changedOutsideARMTags":                                        {Handles: []Surface{SurfaceARMTags}, Why: armBodyHelper},
	"internal/live/mv/rewrite.go:withARMBodyTags":                                              {Handles: []Surface{SurfaceARMTags}, Why: armBodyHelper},
	"internal/live/liveimport/tags.go:changedOutsideARMTags":                                   {Handles: []Surface{SurfaceARMTags}, Why: armBodyHelper},
	"internal/live/liveimport/tags.go:withARMBodyTags":                                         {Handles: []Surface{SurfaceARMTags}, Why: armBodyHelper},
	"internal/live/liveimport/stamp.go:notATagsOnlyPlan":                                       {Handles: []Surface{SurfaceARMTags}, Why: "judges approveOne's tag-path plan, AWS and ARM alike; its ARM arm is changedOutsideARMTags, which leaves an azapi body's tags out of the comparison (#1120 unit 19). The label path judges its own plan (labels.go's changedOutsideLabels) and the manifest shape never plans through the provider"},
	"internal/live/mv/mv.go:mover.find":                                                        {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "credited with locateByIdentity's arms; the manifest shape goes to mv/manifest.go's rewriteManifest from Move before a find runs (#1104)"},
	"internal/live/mv/mv.go:mover.locateByRecord":                                              {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "credited with locateByIdentity's arms; the manifest shape goes to mv/manifest.go's rewriteManifest from Move before a locate runs (#1104)"},
	"internal/live/projection/ownership.go:builder.checkOwnershipAt":                           {Handles: []Surface{SurfaceARMTags}, Why: "its one named surface is the azapi untaggable-type test (azapiCarriesNoMarker, #1120): an azapi_resource whose ARM type takes no tags at its API version is admitted when a record binds it; every surface, that one included, is then read through substrate.MarkersOf, which dispatches over all four"},
	"internal/live/projection/ownership.go:builder.checkOwnership":                             {Handles: []Surface{SurfaceARMTags}, Why: "credited with checkOwnershipAt's azapi untaggable-type test; see that entry"},
	"internal/live/substrate/azure_record.go":                                                  {Handles: []Surface{SurfaceARMTags}, Why: "the Azure family's record answers (#2119): what an azapi_resource's record keeps beside its id, read by AzapiResourceShape's schema test, and whether its ARM type took a marker; a record of any other family is that family's"},
	"internal/live/mv/label.go:mover.relabel":                                                  {Handles: []Surface{SurfaceARMTags, SurfaceLabels}, Why: "the label path; credited with planAndApply's ARM arm, which it never takes: mover.armInBody is set only by rewrite's tag path for an azapi body (#1120 unit 19)"},
	"internal/live/liveimport/googlelabels.go:approveGoogleLabels":                             {Handles: []Surface{SurfaceGoogleLabels, SurfaceLabels}, Why: "the GCP labels carrier (#1119), reached only through approveOne's surface dispatch; the label arm it is credited with is syntheticConfigs' markers.AssertMetadataMaps, which answers a non-metadata schema unchanged"},
	"internal/live/liveimport/googlelabels.go":                                                 {Handles: []Surface{SurfaceGoogleLabels}, Why: "the GCP labels carrier (#1119), reached only through ratifyOne's and stamp.go's surface dispatch"},
	"internal/live/liveimport/slot.go:Ratification.migrationSlots":                             {Handles: []Surface{SurfaceGoogleLabels, SurfaceTags}, Why: "tofu-slot exists on the AWS tags and the GCP labels only; a Kubernetes marker carries no slot (#1073), and the label and manifest carriers block their sets by name"},
	"internal/live/mv/label.go":                                                                {Handles: []Surface{SurfaceLabels}, Why: "the label path, reached only when relabels answered true for the surface surfaceOf read"},
	"internal/live/mv/rewrite.go:mover.rewrite":                                                {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "mv.go's Move sends SurfaceManifest to mv/manifest.go's rewriteManifest, the API merge patch (#1104), before rewrite runs; the ARM tag map takes the tag path with its keys folded (#1120)"},
	"internal/live/mv/mv.go:mover.locateByIdentity":                                            {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "the manifest shape goes to mv/manifest.go's rewriteManifest from Move in this same file before a locate runs, and reads its markers off the cluster's own object (#1104); the ARM tag map is read on the tag path with its keys folded (#1120)"},
	"internal/live/mv/googlelabels.go":                                                         {Handles: []Surface{SurfaceGoogleLabels}, Why: "the GCP labels path (#1119), reached only when relabelsGoogle answered true for the surface surfaceOf read"},
	"internal/live/mv/googlelabels.go:mover.relabelGoogle":                                     {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels}, Why: "the GCP labels write (#1119), reached only when relabelsGoogle answered true; the label arm it is credited with is planAndApply's syntheticConfigs, whose markers.AssertMetadataMaps answers a non-metadata schema unchanged"},
	"internal/live/mv/googlelabels.go:mover.findGoogle":                                        {Handles: []Surface{SurfaceARMTags, SurfaceGoogleLabels, SurfaceLabels, SurfaceTags}, Why: "credited with locateByRecord's and checkDestinationFree's arms, which it calls for a GCP resource; the manifest shape goes to mv/manifest.go's rewriteManifest from Move before a find runs (#1104)"},
	"internal/live/mv/rewrite.go:tagsFromObject":                                               {Handles: []Surface{SurfaceTags}, Why: "the tag path's reader, reached only on SurfaceTags"},
	"internal/live/liveimport/tags.go:syntheticConfigs":                                        {Handles: []Surface{SurfaceLabels}, Why: synthConfigLabelArm},
	"internal/live/mv/rewrite.go:syntheticConfigs":                                             {Handles: []Surface{SurfaceLabels}, Why: synthConfigLabelArm},
	"internal/live/mv/rewrite.go:mover.planAndApply":                                           {Handles: []Surface{SurfaceARMTags, SurfaceLabels}, Why: synthConfigLabelArm + "; its ARM arm is armBodyConfig, an azapi body's tags asserted in the configuration sent (#1120 unit 19)"},
	"internal/live/untag/tags.go:syntheticConfigs":                                             {Handles: []Surface{SurfaceLabels}, Why: synthConfigLabelArm},
	"internal/live/projection/nodestamp_computedmeta.go:NodeResolver.AdjustConfigValueToPrior": {Handles: []Surface{SurfaceLabels}, Why: "carries the server-set keys of an Optional+Computed typed metadata map into the stamped configuration (epic #1885, kubernetes_job_v1's labels): only a typed metadata block has a schema attribute that can be Computed; a tags argument is stamped as a whole map the configuration owns, and a manifest's computed metadata is computed_fields', mirrored by nodestamp_manifest.go"},
	"internal/live/projection/manifestkeys.go":                                                 {Handles: []Surface{SurfaceManifest}, Why: "the manifest shape's declared-key lookup (#1079)"},
	"internal/live/projection/manifestpartialseed.go":                                          {Handles: []Surface{SurfaceManifest}, Why: "the manifest shape's partial seed"},
	"internal/live/projection/metadataseed.go":                                                 {Handles: []Surface{SurfaceLabels}, Why: "the label shape's metadata seed (#1885): hashicorp/kubernetes's typed read keeps a *.kubernetes.io label or annotation only when the prior names it, so the configured metadata maps stand in for a state file's prior; the manifest shape has its own seed and a tag-surface type has no metadata block"},
	"internal/live/projection/nodestamp_manifest.go":                                           {Handles: []Surface{SurfaceManifest}, Why: "the manifest shape's computed-fields mirror"},
	"internal/live/projection/residue.go:residueStubIdentityAttrs":                             {Handles: []Surface{SurfaceManifest}, Why: "the manifest shape's identity attributes on a residue stub, not a marker read"},
	"internal/live/projection/build.go:readImported":                                           {Handles: []Surface{SurfaceGoogleLabels, SurfaceManifest}, Why: "the import read-back, not a marker read: the manifest shape's computed-fields mirror and the GCP labels mirror (mirrorGoogleMarkerLabels), each of which puts back what a non-authoritative read drops"},
	"internal/live/projection/nodestamp_googlelabels.go":                                       {Handles: []Surface{SurfaceGoogleLabels}, Why: "the GCP half of the node stamp (#1119): the labels write, its read-side mirror (hashicorp/google's labels read keeps only the keys the prior names), and the refusal of a marker written into an instance template's propagated labels; each is reached only for a type the google-labels predicates answer"},
	"internal/live/projection/build.go:configuredTagsSeed":                                     {Handles: []Surface{SurfaceTags}, Why: "AWS default_tags: the tags_all merge exists only on the tag surface"},
	"internal/live/substrate/aws.go":                                                           {Handles: []Surface{SurfaceTags}, Why: "the AWS substrate's own answers (#1118); the dispatch over substrate.All in substrate.go is the seam, and it handles every surface"},
	"internal/live/substrate/azure.go":                                                         {Handles: []Surface{SurfaceARMTags}, Why: "the Azure substrate's own answers (#1120); the dispatch over substrate.All in substrate.go is the seam, and it handles every surface"},
	"internal/live/substrate/kubernetes.go":                                                    {Handles: []Surface{SurfaceLabels, SurfaceManifest}, Why: "the Kubernetes substrate's own answers (#1118); the dispatch over substrate.All in substrate.go is the seam, and it handles every surface"},
	"internal/live/substrate/google.go":                                                        {Handles: []Surface{SurfaceGoogleLabels}, Why: "the GCP substrate's own answers (#1119); the dispatch over substrate.All in substrate.go is the seam, and it handles every surface"},
	"internal/live/check/nodestamp.go":                                                         {Handles: []Surface{SurfaceTags}, Why: "NodeStampUnmarkedApply asks the provider family whether a needs-discovery block carries a marker and why not (substrate.SurfaceOf, substrate.NotACarrier), so a server-named google type with a labels map reads as carried and one without reads as refused by google's own reason. Its one tags arm is markers.RefusedTagSurface, the warning for an AWS tags map whose own vocabulary refuses a marker, reached only for a block that carries no surface: a google type there has no tags attribute, so it answers false, and no label or manifest type reaches needs-discovery at all, since both shapes take the Kubernetes family's synthesized identity (kubernetes.SynthesizeIdentity in substrate/identity.go), namespace and name from configuration, never server-assigned, with no cloud component and no *_prefix sibling; generate_name is refused by lint (RuleGenerateName), a name read off another resource resolves parent-derived, and an unresolvable one refuses at the node (#1539). TestNoKubernetesTypeNeedsDiscovery in internal/live/check pins that. nodeStampMarkerConflicts is the offline live-check reading a hand-written tags argument; a hand-written estate label is refused online by the node stamp's label and manifest arms (projection/nodestamp_labels.go, markerConflictDiag), and a hand-written GCP marker, in a labels map or in an instance template's propagated labels, by the node stamp's google-labels arm (the shared markerConflictDiag, and propagatedMarkerDiag in projection/nodestamp_googlelabels.go)"},
	"internal/command/live_plan.go:googleLabelSplit":                                           {Handles: []Surface{SurfaceGoogleLabels}, Why: "the GCP leg's TYPE_NOT_TAGGABLE list (#2119 D3): which hashicorp/google types carry the google-labels surface and which do not, asked only of a provider whose family sweeps by asset search; no other family's types reach it"},
	"internal/command/live_plan.go:projectionProviders.assetSearchLeg":                         {Handles: []Surface{SurfaceGoogleLabels}, Why: "builds the GCP sweep leg, credited with googleLabelSplit; reached only through sweepLegBuilders' asset-search entry"},
	"internal/command/live_plan.go:liveUnmarkedApplyGaps":                                      {Handles: []Surface{SurfaceTags}, Why: "reaches the markers package only through check.NodeStampUnmarkedApply; see internal/live/check/nodestamp.go's entry"},
	"internal/live/identity/resolve.go:resolver.manifestObjectKeyPart":                         {Handles: []Surface{SurfaceManifest}, Why: "not a marker read: it resolves kubernetes_manifest.x.object.metadata.name or .namespace, or the same keys through manifest (#1616), from the parent's manifest argument (#1116)."},
	"tools/estate-gen/gen.go":                                                                  {Handles: []Surface{SurfaceTags}, Why: "a generator over the AWS provider's survey; it never reads a live marker"},
	"tools/survey-gen/classify.go":                                                             {Handles: []Surface{SurfaceTags}, Why: "a generator over the AWS provider's survey; it never reads a live marker"},
	"tools/survey-gen/governance_render.go":                                                    {Handles: []Surface{SurfaceTags}, Why: "a generator over the AWS provider's survey; it never reads a live marker"},
	"tools/survey-gen/parent_render.go":                                                        {Handles: []Surface{SurfaceTags}, Why: "a generator over the AWS provider's survey; it never reads a live marker"},
	"tools/survey-gen/untaggable_render.go":                                                    {Handles: []Surface{SurfaceTags}, Why: "a generator over the AWS provider's survey; it never reads a live marker"},
}

// surfaceSeamUntriaged is the rest of what the guard measured on the day
// it landed (2026-09-26): seams that handle a subset of the surfaces and
// that nobody has yet ruled deliberate or a hole. Several read like holes
// of #1108's shape - the node stamp's diagnostics explain a Kubernetes
// type as having "no tags map", and the record-fallback route asks only
// Taggable of types that include kubernetes_config_map - which is exactly
// what this guard exists to surface. Each moves to surfaceSeamExemptions
// with a reason, or is fixed, under GitHub issue #1565; the
// list is checked exactly like an exemption, so it can only shrink by a
// deliberate edit, and it never grows: a new partial seam fails. #1565
// emptied it: every seam it named is exempted above with its reason, or
// now asks substrate.SurfaceOf (the adoption ledger and the roster).
var surfaceSeamUntriaged = map[string][]Surface{}

func TestEverySurfaceSeamHandlesEverySurface(t *testing.T) {
	root := moduleRoot(t)
	g, err := measureSurfaceSeams(root, "github.com/intentius/choudoufu", "internal/live/markers", surfaceSeamRoots)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range g.declProblems {
		t.Error(p)
	}
	if len(g.surfaces) < 3 {
		t.Fatalf("read %d surfaces off internal/live/markers (%v); expected at least tags, labels and manifest - the guard is reading the wrong package or none", len(g.surfaces), g.surfaces)
	}
	if len(g.seams) < 20 {
		t.Fatalf("measured %d seams; the tree has had more than 40 since #1118 - the scan has gone blind", len(g.seams))
	}

	complete := 0
	for _, s := range g.seams {
		if len(s.missing(g.surfaces)) == 0 {
			complete++
		}
	}
	t.Logf("surfaces %v; %d seams measured, %d handle every surface", g.surfaces, len(g.seams), complete)

	seen := map[string]bool{}
	for _, s := range g.seams {
		missing := s.missing(g.surfaces)

		// An exemption for this one function bounds it exactly, complete
		// or not: a seam that learned a surface has outgrown its entry.
		fnKey := s.file + ":" + s.name
		if handles, ok := exemptionFor(fnKey); ok {
			seen[fnKey] = true
			if want := surfaceSet(handles); want != s.handledString() {
				t.Errorf("%s (%s): exempted by %q as handling %s, measured handling %s. Revisit the exemption: a seam that learned or lost a surface is no longer the seam it excuses.", s.key, s.pos, fnKey, want, s.handledString())
			}
			continue
		}
		if len(missing) == 0 {
			continue
		}

		// A file's exemption speaks for the partial seams in it that
		// handle exactly what it says, and for nothing else.
		note := ""
		if handles, ok := exemptionFor(s.file); ok {
			seen[s.file] = true
			want := surfaceSet(handles)
			if want == s.handledString() {
				continue
			}
			note = fmt.Sprintf(" (the exemption for %s covers seams handling exactly %s)", s.file, want)
		}
		t.Errorf("%s (%s) handles the %s marker surface but never %s%s. Handle it, or exempt it in surfaceSeamExemptions with the reason it does not need to.", s.key, s.pos, s.handledString(), joinSurfaces(missing), note)
	}
	for key := range surfaceSeamExemptions {
		if !seen[key] {
			t.Errorf("surfaceSeamExemptions[%q] names no seam the guard measured; remove it", key)
		}
	}
	for key := range surfaceSeamUntriaged {
		if !seen[key] {
			t.Errorf("surfaceSeamUntriaged[%q] names no seam the guard measured; remove it", key)
		}
		if _, both := surfaceSeamExemptions[key]; both {
			t.Errorf("%q is both exempted and untriaged; it is one or the other", key)
		}
	}
}

func exemptionFor(key string) ([]Surface, bool) {
	if ex, ok := surfaceSeamExemptions[key]; ok {
		return ex.Handles, true
	}
	hs, ok := surfaceSeamUntriaged[key]
	return hs, ok
}

// TestSurfaceSeamGuardSeesTheFixture runs the same measurement over a
// small tree whose holes are known, so that a guard which has gone blind
// cannot also report the real tree green.
func TestSurfaceSeamGuardSeesTheFixture(t *testing.T) {
	g, err := measureSurfaceSeams("testdata/seamguard", "example.com/fixture", "markers", []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range g.declProblems {
		t.Log(p)
	}
	wantProblems := []string{
		`markers.Orphan: exported, takes or returns a marker value, and carries no //markers:surface directive`,
		`markers.Mystery: //markers:surface names "gcp", which is not a Surface constant`,
		`surface "annotations" has no predicate (a //markers:surface function taking a *configschema.Block and returning a bool)`,
	}
	if got := strings.Join(g.declProblems, "\n"); got != strings.Join(wantProblems, "\n") {
		t.Errorf("declaration problems:\n%s\nwant:\n%s", got, strings.Join(wantProblems, "\n"))
	}

	got := map[string]string{}
	for _, s := range g.seams {
		got[s.key] = s.handledString()
	}
	want := map[string]string{
		// A dispatch that forgot one surface (#1104's shape).
		"seams.SurfaceOf": "{labels,tags}",
		// A complete dispatch, directly.
		"seams.complete": "{annotations,labels,manifest,tags}",
		// A reader of one surface only (#1108's shape).
		"seams.ownership": "{tags}",
		// A dispatch through wrappers alone (#1109's shape). The wrappers
		// themselves (taggable, labelled, manifested) are not seams.
		"seams.ratify": "{labels,tags}",
		// A dispatch completed through a method on its own receiver.
		"seams.reader.read":   "{annotations,labels,manifest,tags}",
		"seams.reader.labels": "{annotations,labels,manifest}",
		// Reached through a package-qualified call from a package that
		// never imports markers.
		"seams/caller.viaOtherPackage": "{labels,tags}",
		// Deep handles manifest through its own package's wrapper; a
		// caller elsewhere takes only what Deep references itself.
		"seams.Deep":         "{manifest}",
		"seams/caller.mixed": "{labels,tags}",
		// A caller acting on a Surface value by naming its constants
		// (#1118's Substrate seam returns one).
		"seams.actOn": "{labels,tags}",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("fixture seam %s: measured %q, want %q", k, got[k], w)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("fixture seam %s measured but not expected (handles %s)", k, got[k])
		}
	}
}

// seamGraph is one measurement.
type seamGraph struct {
	surfaces     []Surface
	declProblems []string
	seams        []*seamFunc
}

type seamFunc struct {
	key     string
	file    string // relative to the scanned root, slash-separated
	name    string // Func or Recv.Func
	pos     string
	pkg     string
	decl    *ast.FuncDecl
	src     *seamFile
	direct  map[Surface]bool
	callees []*seamFunc
	handled map[Surface]bool
}

func (s *seamFunc) missing(all []Surface) []Surface {
	var out []Surface
	for _, x := range all {
		if !s.handled[x] {
			out = append(out, x)
		}
	}
	return out
}

func (s *seamFunc) handledString() string {
	var hs []Surface
	for x := range s.handled {
		hs = append(hs, x)
	}
	return surfaceSet(hs)
}

func surfaceSet(xs []Surface) string {
	ss := make([]string, len(xs))
	for i, x := range xs {
		ss[i] = string(x)
	}
	sort.Strings(ss)
	return "{" + strings.Join(ss, ",") + "}"
}

func joinSurfaces(xs []Surface) string {
	ss := make([]string, len(xs))
	for i, x := range xs {
		ss[i] = string(x)
	}
	return strings.Join(ss, " or ")
}

type seamFile struct {
	path    string
	ast     *ast.File
	imports map[string]string // local name -> import path
}

type seamPkg struct {
	dir     string // relative to the scanned root, slash-separated
	path    string // import path
	files   []*seamFile
	funcs   map[string]*seamFunc
	methods map[string][]*seamFunc
}

// measureSurfaceSeams reads the surfaces and their members off the markers
// package at markersDir, then every seam under roots, all relative to root.
func measureSurfaceSeams(root, modulePath, markersDir string, roots []string) (*seamGraph, error) {
	g := &seamGraph{}
	markersPath := modulePath + "/" + markersDir
	members, err := readSurfaceDecls(filepath.Join(root, markersDir), g)
	if err != nil {
		return nil, err
	}

	// Every package directory under the roots. Not only the importers of
	// this package: a dispatch that asks only wrappers (#1109's shape) may
	// sit in a package that never imports markers itself.
	fset := token.NewFileSet()
	pkgDirs := map[string]bool{}
	for _, r := range roots {
		err := filepath.WalkDir(filepath.Join(root, r), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") && p != filepath.Join(root, r) {
					return filepath.SkipDir
				}
				if rel, _ := filepath.Rel(root, p); filepath.ToSlash(rel) == markersDir || strings.HasPrefix(filepath.ToSlash(rel), markersDir+"/") {
					return filepath.SkipDir
				}
				return nil
			}
			if isSeamSource(p) {
				pkgDirs[filepath.Dir(p)] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	pkgs := map[string]*seamPkg{} // by import path
	var dirs []string
	for d := range pkgDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		rel, _ := filepath.Rel(root, d)
		rel = filepath.ToSlash(rel)
		pkg := &seamPkg{dir: rel, path: modulePath + "/" + rel, funcs: map[string]*seamFunc{}, methods: map[string][]*seamFunc{}}
		if rel == "." {
			pkg.path = modulePath
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			if e.IsDir() || !isSeamSource(p) {
				continue
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", p, err)
			}
			sf := &seamFile{path: p, ast: f, imports: map[string]string{}}
			for _, im := range f.Imports {
				path, _ := strconv.Unquote(im.Path.Value)
				name := path[strings.LastIndex(path, "/")+1:]
				if im.Name != nil {
					name = im.Name.Name
				}
				sf.imports[name] = path
			}
			pkg.files = append(pkg.files, sf)
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fn := &seamFunc{pkg: pkg.path, decl: fd, src: sf, direct: map[Surface]bool{}}
				pos := fset.Position(fd.Pos())
				relFile, _ := filepath.Rel(root, pos.Filename)
				fn.file = filepath.ToSlash(relFile)
				fn.pos = fmt.Sprintf("%s:%d", fn.file, pos.Line)
				fn.name = fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					fn.name = recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
					pkg.methods[fd.Name.Name] = append(pkg.methods[fd.Name.Name], fn)
				} else if fd.Name.Name != "init" && fd.Name.Name != "_" {
					pkg.funcs[fd.Name.Name] = fn
				}
				fn.key = rel + "." + fn.name
			}
		}
		pkgs[pkg.path] = pkg
	}

	var all []*seamFunc
	for _, d := range dirs {
		rel, _ := filepath.Rel(root, d)
		rel = filepath.ToSlash(rel)
		path := modulePath + "/" + rel
		if rel == "." {
			path = modulePath
		}
		pkg := pkgs[path]
		for _, sf := range pkg.files {
			for _, decl := range sf.ast.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fn := lookupDecl(pkg, fd)
				resolveSeamRefs(fn, pkg, pkgs, markersPath, members)
				all = append(all, fn)
			}
		}
	}

	// What each function handles: the surfaces its own body references,
	// and everything its callees handle, to a fixed point.
	for _, fn := range all {
		fn.handled = map[Surface]bool{}
		for s := range fn.direct {
			fn.handled[s] = true
		}
	}
	//
	// The closure runs through callees in the seam's own package only. A
	// callee in another package counts for what it references itself (and
	// a wrapper there for its member), not for everything behind it: that
	// is a service the seam uses, and following it lets an unrelated use
	// of a member stand in for the seam's own answer. The case that set
	// this: before #1109's fix, liveimport's ratifyOne reached
	// ManifestSurface through identity's manifest-shape identity synthesis,
	// which says nothing about carrying a marker.
	for _, fn := range all {
		for _, c := range fn.callees {
			if c.pkg != fn.pkg {
				for s := range c.direct {
					fn.handled[s] = true
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range all {
			for _, c := range fn.callees {
				if c.pkg != fn.pkg {
					continue
				}
				for s := range c.handled {
					if !fn.handled[s] {
						fn.handled[s] = true
						changed = true
					}
				}
			}
		}
	}

	// A seam is a function that references a member itself, or calls one
	// that does. The closure above is what it handles; the membership
	// stops at one hop so that the whole call graph above a seam does not
	// become seams in turn.
	//
	// A wrapper - a statement or two, referencing one surface's members and
	// nothing else a seam could be - is that member under another name
	// (liveimport's taggable, identity.ManifestShape): its callers are
	// seams through it, and it is not one itself.
	for _, fn := range all {
		seam := len(fn.direct) > 0
		for _, c := range fn.callees {
			if len(c.direct) > 0 {
				seam = true
			}
		}
		if seam && !isSurfaceWrapper(fn, markersPath, members) {
			g.seams = append(g.seams, fn)
		}
	}
	sort.Slice(g.seams, func(i, j int) bool { return g.seams[i].key < g.seams[j].key })
	return g, nil
}

func isSurfaceWrapper(fn *seamFunc, markersPath string, members map[string]Surface) bool {
	// At most two statements (`_, ok := markers.LabelSurface(b); return
	// ok`), and the only call anywhere in them is to one member. A body
	// that calls anything else is doing its own work with the answer, and
	// is a seam - #1108's checkOwnership read markers.TagsOf and went on.
	if len(fn.decl.Body.List) > 2 || len(fn.direct) != 1 {
		return false
	}
	calls, memberCalls := 0, 0
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		calls++
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && fn.src != nil && fn.src.imports[id.Name] == markersPath {
				if _, ok := members[sel.Sel.Name]; ok {
					memberCalls++
				}
			}
		}
		return true
	})
	return calls == 1 && memberCalls == 1
}

func isSeamSource(p string) bool {
	return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go")
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func lookupDecl(pkg *seamPkg, fd *ast.FuncDecl) *seamFunc {
	if fd.Recv == nil {
		if fn := pkg.funcs[fd.Name.Name]; fn != nil && fn.decl == fd {
			return fn
		}
	}
	for _, fn := range pkg.methods[fd.Name.Name] {
		if fn.decl == fd {
			return fn
		}
	}
	// init and blank functions are not indexed by name; they still get
	// measured, but only for what they reference.
	return &seamFunc{pkg: pkg.path, decl: fd, key: pkg.dir + "." + fd.Name.Name, name: fd.Name.Name, direct: map[Surface]bool{}}
}

// resolveSeamRefs fills fn.direct and fn.callees from its body. Every
// reference counts, not only calls: a predicate handed around as a value
// is still the seam asking it.
func resolveSeamRefs(fn *seamFunc, pkg *seamPkg, pkgs map[string]*seamPkg, markersPath string, members map[string]Surface) {
	var imports map[string]string
	if fn.src != nil {
		imports = fn.src.imports
	}
	addCallee := func(c *seamFunc) {
		if c != nil && c != fn {
			fn.callees = append(fn.callees, c)
		}
	}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if path, ok := imports[id.Name]; ok {
					if path == markersPath {
						if s, ok := members[x.Sel.Name]; ok {
							fn.direct[s] = true
						}
					} else if other := pkgs[path]; other != nil {
						addCallee(other.funcs[x.Sel.Name])
					}
					return false
				}
			}
			for _, m := range pkg.methods[x.Sel.Name] {
				addCallee(m)
			}
			ast.Inspect(x.X, visit)
			return false
		case *ast.Ident:
			addCallee(pkg.funcs[x.Name])
		}
		return true
	}
	ast.Inspect(fn.decl.Body, visit)
}

// readSurfaceDecls reads the Surface constants and the //markers:surface
// directives off the markers package's own source, recording anything
// inconsistent in g.declProblems. It returns member name -> surface: the
// directive-carrying functions and the Surface constants.
func readSurfaceDecls(dir string, g *seamGraph) (map[string]Surface, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	consts := map[Surface]bool{}
	constNames := map[string]Surface{}
	type member struct {
		name      string
		surface   Surface
		predicate bool
	}
	var members []member
	var untagged []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() || !isSeamSource(p) {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Surface" {
						continue
					}
					for i, v := range vs.Values {
						if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							s, _ := strconv.Unquote(lit.Value)
							consts[Surface(s)] = true
							if i < len(vs.Names) {
								constNames[vs.Names[i].Name] = Surface(s)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				var surface string
				if d.Doc != nil {
					for _, c := range d.Doc.List {
						if rest, ok := strings.CutPrefix(c.Text, "//markers:surface "); ok {
							surface = strings.TrimSpace(rest)
						}
					}
				}
				if surface == "" {
					if carriesMarkerValue(d.Type) {
						untagged = append(untagged, d.Name.Name)
					}
					continue
				}
				members = append(members, member{name: d.Name.Name, surface: Surface(surface), predicate: isSurfacePredicate(d.Type)})
			}
		}
	}

	sort.Strings(untagged)
	for _, name := range untagged {
		g.declProblems = append(g.declProblems, fmt.Sprintf("markers.%s: exported, takes or returns a marker value, and carries no //markers:surface directive", name))
	}
	out := map[string]Surface{}
	hasPredicate := map[Surface]bool{}
	sort.Slice(members, func(i, j int) bool { return members[i].name < members[j].name })
	for _, m := range members {
		if !consts[m.surface] {
			g.declProblems = append(g.declProblems, fmt.Sprintf("markers.%s: //markers:surface names %q, which is not a Surface constant", m.name, m.surface))
			continue
		}
		out[m.name] = m.surface
		if m.predicate {
			hasPredicate[m.surface] = true
		}
	}
	// A Surface constant is a member of its surface too. Since #1118's
	// Substrate seam, a dispatch answers with a Surface value and its
	// callers act on it by naming the constants: a caller that switches
	// on SurfaceTags and SurfaceLabels and forgets SurfaceManifest is the
	// #1104 shape one layer down, and without this it would be invisible.
	for name, s := range constNames {
		out[name] = s
	}
	for s := range consts {
		g.surfaces = append(g.surfaces, s)
	}
	sort.Slice(g.surfaces, func(i, j int) bool { return g.surfaces[i] < g.surfaces[j] })
	for _, s := range g.surfaces {
		if !hasPredicate[s] {
			g.declProblems = append(g.declProblems, fmt.Sprintf("surface %q has no predicate (a //markers:surface function taking a *configschema.Block and returning a bool)", s))
		}
	}
	return out, nil
}

// carriesMarkerValue is the shape an exported function has when it acts
// for one surface: it takes a schema block or an object value, or returns
// a path into one. Such a function without a directive would be a member
// the guard cannot see.
func carriesMarkerValue(ft *ast.FuncType) bool {
	for _, f := range ft.Params.List {
		if isNamedType(f.Type, "configschema", "Block") || isNamedType(f.Type, "cty", "Value") {
			return true
		}
	}
	if ft.Results != nil {
		for _, f := range ft.Results.List {
			if isNamedType(f.Type, "cty", "Path") {
				return true
			}
		}
	}
	return false
}

func isSurfacePredicate(ft *ast.FuncType) bool {
	if len(ft.Params.List) == 0 || !isNamedType(ft.Params.List[0].Type, "configschema", "Block") {
		return false
	}
	if ft.Results == nil || len(ft.Results.List) == 0 {
		return false
	}
	last := ft.Results.List[len(ft.Results.List)-1].Type
	id, ok := last.(*ast.Ident)
	return ok && id.Name == "bool"
}

func isNamedType(e ast.Expr, pkg, name string) bool {
	if st, ok := e.(*ast.StarExpr); ok {
		e = st.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
