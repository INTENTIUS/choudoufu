# Faults on Azure

`live/kubernetes/FAULTS.md` answers #1110's question for a cluster: which
faults the platform produces that the lane never injected, and whether each
is a gauntlet stage or a claim with a `BREAK` control. This page asks the
same of Azure. Nothing on it has been injected yet. floci-az has never
booted under a scenario, and several of these faults need a surface it
does not serve ([`live/FLOCI.md`](../FLOCI.md#azure-floci-az)).

Each row says what Azure does, what the code answers, and what would
inject it.

| # | Fault | What answers it | Proof today | What would inject it |
|---|---|---|---|---|
| 1 | A create PUT lands on an object that already exists | Refused by name before any create, "Unmarked live object at the declared id would be overwritten", under `declared_untagged` `"refuse"` (the default), `"keep"` and `"report"`; `"adopt"` and `"converge"` admit the object first and the plan writes the markers onto it. Both providers also read the id first and fail the apply (azapi always; azurerm unless `skip_import_check_on_create_and_allow_overwriting_existing_resources`, and not for every type), so the plan's refusal is earlier, and under azurerm sometimes the only guard; `live/azure/COMPATIBILITY.md`, "How you run it" | unit tests in `internal/live/projection`; `az-identity-is-a-tag` scenario, `BREAK=1` arm | that scenario, once floci-az serves Resource Graph (lex00/floci-az#4) |
| 2 | A kill between the create and the after-create tag write | The apply fails naming the object and the `az tag update` line; the next plan's stamp asks for exactly what is stored | `TestARMPostCreateConvergesOnTheNextPlan` | a gauntlet `day2_crash` on an estate declaring `azurerm_app_service_managed_certificate`; no estate does |
| 3 | A kill mid-apply | The created resource carries both tags from its create PUT, so the next plan binds it and proposes the remainder | none | `day2_crash` on `reference-azure`, built and not run |
| 4 | Resource Graph still lists a deleted resource | A record tombstone, read before a lingering marker is called a second claimant (`MarkerOutlivesObject` true) | the shared tombstone tests in `internal/live/discovery`, written against AWS candidates; none is Azure's own | needs an emulator that lags deletes; floci-az serves no Resource Graph |
| 5 | Resource Graph answers 429 | Up to four waits on `Retry-After` and `x-ms-user-quota-resets-after`, none over a minute, then the plan is refused naming the subscription | unit tests in `internal/live/azsweep` | a claim against a throttling fake; no emulator throttles |
| 6 | A subscription the credential cannot read | A warning naming the subscription; the sweep is incomplete, never empty | `TestUnreadableSubscriptionIsAnIncompleteSweepNotAnEmptyOne` | a scenario with two subscriptions and one role assignment |
| 7 | `Microsoft.ResourceGraph` not registered | Refused by name, with the `az provider register` line | `TestResourceGraphLegRefusesAnUnregisteredProvider` | real Azure only |
| 8 | Azure Policy refuses an approved write | ARM's own refusal, printed as the provider reports it | `az-the-tag-is-the-boundary` draft | that draft, on real Azure; floci-az evaluates no Policy rule |
| 9 | The resource group is deleted under the estate | Every child goes with it, untaggable ones included; the next plan proposes recreating what is declared | none | a claim shaped like Kubernetes's deleted-namespace one |
| 10 | AKS or Azure Policy writes a marker this estate did not write onto a declared resource | AKS copies a cluster's tags over those of the existing route table, public IP or virtual network it is given, so the object carries this estate's `tofu-estate` and the cluster's `tofu-address`, or another estate's; a Policy `modify` effect can write tag values too. Refused by name before any create, "Live object marked for another owner at the declared id would be overwritten", naming the marker found, under every `declared_untagged` verb (#2135). `strict { markers "record" }` for the type holds its identity in the record store and the copied tags then decide nothing | `TestAKSRetag_*` in `internal/live/projection`; `TestMarkersRecordAzurermRecordsTheProviderSubscription` | an estate with an AKS cluster given a declared route table, on real Azure; floci-az runs no AKS |

## What to read first

Rows 1, 2 and 10 are the ones that would write a wrong marker or none, which is
the one outcome `HANDOFF.md`'s safety rule forbids. All three have unit tests
that were shown red before the fix, and none has run against ARM. Row 1
is Azure's own: on AWS the same plan proposes a second resource, and on
Kubernetes the API server answers 409. Row 10 is row 1 for an object that
is marked, but not for this block: on AWS and Kubernetes that stays a
warning beside the plan, and on Azure the create would be a PUT over it.

Rows 4 to 7 are about the sweep. Each one ends in a named warning or
refusal and never in an empty listing, because an empty listing reads as
an estate with nothing to remove. Their summaries are in
[`live/LIMITATIONS.md`](../LIMITATIONS.md#azure-sweep-unavailable).

Row 9 is the Azure counterpart of Kubernetes fault 5, the deleted
namespace. A resource group holds everything an estate declares in it, so
it is the shape most worth a claim once the harness can run one.
