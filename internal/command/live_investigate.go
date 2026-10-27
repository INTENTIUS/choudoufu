// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/affected"
	"github.com/intentius/choudoufu/internal/live/investigate"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/cloudcontrol"
	tfversion "github.com/intentius/choudoufu/version"
)

// InvestigateCommand is "choudoufu investigate" (GitHub issue #2280): one
// named, read-only question. The native lookups are answered here; chant's
// investigation templates run through chant search (path) or behold's graph
// (Cypher). The MCP server ([McpCommand]) is a front over the same
// investigations.
type InvestigateCommand struct {
	Meta

	// open is the record store opener for history; nil means
	// [projection.NewRecordStore].
	open recordStoreOpener

	// ownerTags reads one AWS resource's tags for owner; nil means the live
	// read ([liveOwnerTags]).
	ownerTags ownerTagReader
}

// ownerTagReader reads the tags of the resource an ARN names. found is false
// when no resource with that ARN carries a tofu-estate marker. source names
// the API read.
type ownerTagReader func(ctx context.Context, arn cloudcontrol.ARN, id string) (tags map[string]string, found bool, source string, err error)

// InvestigateCommander is investigate's entry in the new CLI's command tree.
func InvestigateCommander() Command {
	return investigateCommander(nil, nil)
}

func investigateCommander(open recordStoreOpener, owner ownerTagReader) Command {
	cmd := Command{
		Name:  "investigate",
		Short: (&InvestigateCommand{}).Synopsis(),
	}
	args := arguments.BindInvestigate(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&InvestigateCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&InvestigateCommand{Meta: meta, open: open, ownerTags: owner}).Execute(args)
	}
	return cmd
}

func (c *InvestigateCommand) Run(rawArgs []string) int {
	return RunCommand(investigateCommander(c.open, c.ownerTags), c.Meta, rawArgs)
}

func investigateTools(n arguments.Neighbours) *investigate.Tools {
	return &investigate.Tools{Chant: n.Chant, Behold: n.Behold, TemplatesFile: n.Templates, Dir: n.Dir}
}

// natives are the four native lookups with this command's functions.
func (c *InvestigateCommand) natives() []investigate.Native {
	ns := investigate.Natives()
	for i := range ns {
		switch ns[i].ID {
		case investigate.NativeOwner:
			ns[i].Run = c.investigateOwner
		case investigate.NativeHistory:
			ns[i].Run = c.investigateHistory
		case investigate.NativeForeign:
			ns[i].Run = investigateForeign
		case investigate.NativeAffected:
			ns[i].Run = investigateAffected
		}
	}
	return ns
}

func (c *InvestigateCommand) Execute(args *arguments.Investigate) int {
	ctx := c.CommandContext()
	if ctx == nil {
		ctx = context.Background()
	}
	tools := investigateTools(args.Neighbours)
	natives := c.natives()
	out := views.NewInvestigate(c.View)

	if args.List {
		cat := investigate.LoadCatalog(ctx, tools)
		listing := struct {
			Natives              []investigate.Native          `json:"natives"`
			Templates            []investigate.TemplateListing `json:"templates"`
			TemplatesSource      string                        `json:"templates_source,omitempty"`
			TemplatesUnavailable string                        `json:"templates_unavailable,omitempty"`
		}{Natives: investigate.NativesListing(), Templates: investigate.Listing(cat, tools, nil), TemplatesSource: cat.Source, TemplatesUnavailable: cat.Unavailable}
		if args.JSON {
			b, _ := json.MarshalIndent(listing, "", "  ")
			out.Output(string(b))
			return 0
		}
		var b strings.Builder
		b.WriteString("Native lookups (choudoufu, no graph):\n")
		for _, n := range listing.Natives {
			fmt.Fprintf(&b, "  %-18s %s (reads %s)\n", n.ID, n.Question, n.Reads)
		}
		b.WriteString("\nTemplates (chant's catalog):\n")
		if cat.Unavailable != "" {
			fmt.Fprintf(&b, "  unavailable: %s\n", cat.Unavailable)
		}
		for _, t := range listing.Templates {
			fmt.Fprintf(&b, "  %-18s %s (%s, reads %s)\n", t.ID, t.Question, t.Query.Kind, t.Reads)
			if t.Unavailable != "" {
				fmt.Fprintf(&b, "  %-18s unavailable: %s\n", "", t.Unavailable)
			}
		}
		out.Output(b.String())
		return 0
	}

	var ans investigate.Answer
	if n, ok := findNative(natives, args.Name); ok {
		inputs := copyInputs(args.Inputs)
		if args.Primary != "" {
			if _, dup := inputs[n.Inputs[0].Name]; dup {
				return c.investigateUsage(fmt.Sprintf("%s is given both bare and as %s=.", n.Inputs[0].Name, n.Inputs[0].Name))
			}
			inputs[n.Inputs[0].Name] = args.Primary
		}
		ans = investigate.RunNative(ctx, n, inputs)
	} else {
		cat := investigate.LoadCatalog(ctx, tools)
		if cat.Unavailable != "" {
			return c.investigateUsage(fmt.Sprintf("%q is not a native lookup, and the templates are unavailable: %s", args.Name, cat.Unavailable))
		}
		tpl, ok := cat.Template(args.Name)
		if !ok {
			return c.investigateUsage(fmt.Sprintf("There is no investigation %q. choudoufu investigate -list names them.", args.Name))
		}
		inputs := copyInputs(args.Inputs)
		if args.Primary != "" {
			if _, dup := inputs[tpl.Resource()]; dup {
				return c.investigateUsage(fmt.Sprintf("%s is given both bare and as %s=.", tpl.Resource(), tpl.Resource()))
			}
			inputs[tpl.Resource()] = args.Primary
		}
		ans = investigate.RunTemplate(ctx, tools, tpl, inputs, nil)
	}

	if args.JSON {
		b, err := json.MarshalIndent(ans, "", "  ")
		if err != nil {
			c.View.Diagnostics(tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot render the answer", err.Error())))
			return 1
		}
		out.Output(string(b))
	} else {
		out.Output(answerText(ans))
	}
	if ans.Refused != "" || ans.Unavailable != "" {
		return 1
	}
	return 0
}

func (c *InvestigateCommand) investigateUsage(detail string) int {
	c.View.Diagnostics(tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot investigate", detail)))
	return 1
}

func findNative(ns []investigate.Native, id string) (investigate.Native, bool) {
	for _, n := range ns {
		if n.ID == id {
			return n, true
		}
	}
	return investigate.Native{}, false
}

func copyInputs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func answerText(a investigate.Answer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s, %s) via %s\n", a.Investigation, a.Kind, a.Method, a.Via)
	fmt.Fprintf(&b, "  reads:     %s\n  cost:      %s\n  freshness: %s\n", a.Reads, a.Cost, a.Freshness)
	if a.ObservedAt != "" {
		fmt.Fprintf(&b, "  observed:  %s\n", a.ObservedAt)
	}
	if a.Query != "" {
		fmt.Fprintf(&b, "  query:     %s\n", a.Query)
	}
	switch {
	case a.Unavailable != "":
		fmt.Fprintf(&b, "\nUnavailable: %s\n", a.Unavailable)
	case a.Refused != "":
		fmt.Fprintf(&b, "\nRefused: %s\n", a.Refused)
	}
	if len(a.Result) > 0 {
		var v any
		if json.Unmarshal(a.Result, &v) == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ")
			fmt.Fprintf(&b, "\n%s\n", pretty)
		}
	}
	return b.String()
}

// investigateOwnerAnswer is owner's answer. It carries the marker values
// only, never the resource's other tags.
type investigateOwnerAnswer struct {
	CloudID string `json:"cloud_id"`
	// Owned is true when the resource carries a tofu-estate marker.
	Owned   bool   `json:"owned"`
	Estate  string `json:"estate,omitempty"`
	Address string `json:"address,omitempty"`
	Slot    string `json:"slot,omitempty"`
	HeldBy  string `json:"held_by,omitempty"`
	Source  string `json:"source"`
	Text    string `json:"text"`
}

func (c *InvestigateCommand) investigateOwner(ctx context.Context, in map[string]string) (any, string, error) {
	id := strings.TrimSpace(in["cloud_id"])
	arn, ok := cloudcontrol.ParseARN(id)
	if !ok {
		return nil, "", investigate.Refusef("%q is not an AWS ARN. The owner lookup reads AWS markers today; an Azure resource ID, a GCP resource name or a Kubernetes UID is not read yet. behold's ownership template answers from the graph for those.", id)
	}
	read := c.ownerTags
	if read == nil {
		read = liveOwnerTags
	}
	tags, found, source, err := read(ctx, arn, id)
	observed := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		return nil, observed, err
	}
	ans := investigateOwnerAnswer{CloudID: id, Source: source}
	if !found || tags[markers.TagEstate] == "" {
		ans.Text = "No tofu-estate marker was found on it, so no choudoufu estate owns it. The tagging index does not tell an unmarked resource from one that does not exist."
		return ans, observed, nil
	}
	item := liveLsItemFromTags(id, tags, source)
	ans.Owned = true
	ans.Estate = tags[markers.TagEstate]
	ans.Address = item.Address
	ans.Slot = item.Slot
	ans.HeldBy = item.HeldBy
	if ans.Address == "" {
		ans.Text = fmt.Sprintf("Estate %s owns it, but its tofu-address marker is missing or unreadable.", ans.Estate)
	} else {
		ans.Text = fmt.Sprintf("Estate %s owns it, as %s.", ans.Estate, ans.Address)
	}
	return ans, observed, nil
}

// liveOwnerTags is owner's live read: iam:ListRoleTags for an IAM role,
// which the tagging index does not cover on a real account, else the
// Resource Groups Tagging API filtered to the ARN's service and the
// tofu-estate key, matched by ARN. Both are reads. The endpoints are the
// ones every live command uses (cloudControlTarget, AWS_ENDPOINT_URL*).
func liveOwnerTags(ctx context.Context, arn cloudcontrol.ARN, id string) (map[string]string, bool, string, error) {
	ep, on := cloudControlTarget()
	if !on {
		return nil, false, "", investigate.Refusef("TOFU_LIVE_CLOUDCONTROL is off, which turns off the live marker read owner is built on")
	}
	if arn.Service == "iam" && arn.ResourceType == "role" {
		awsCfg, err := liveLsAWSConfig(ctx, "")
		if err != nil {
			return nil, false, "iam", fmt.Errorf("loading AWS configuration: %w", err)
		}
		name := path.Base(arn.ResourceID)
		out, err := iam.NewFromConfig(awsCfg).ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: aws.String(name)})
		if err != nil {
			return nil, false, "iam", fmt.Errorf("iam:ListRoleTags for %s: %w", name, err)
		}
		tags := iamTagMap(out.Tags)
		return tags, tags[markers.TagEstate] != "", "iam", nil
	}
	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: ep, Region: arn.Region})
	res, err := tagging.GetResources(ctx, []string{arn.Service}, []cloudcontrol.TagFilter{{Key: markers.TagEstate}})
	if err != nil {
		return nil, false, "tagging", fmt.Errorf("the Resource Groups Tagging API's GetResources: %w", err)
	}
	for _, r := range res {
		if r.ResourceARN == id {
			return r.Tags, true, "tagging", nil
		}
	}
	return nil, false, "tagging", nil
}

func (c *InvestigateCommand) investigateHistory(ctx context.Context, in map[string]string) (any, string, error) {
	dir := in["dir"]
	if dir == "" {
		dir = "."
	}
	h, diags := (&LiveHistoryCommand{Meta: c.Meta, open: c.open}).read(ctx, dir, in["estate"], in["address"])
	observed := time.Now().UTC().Format(time.RFC3339)
	if diags.HasErrors() {
		return nil, observed, investigate.Refusef("%s", diags.Err())
	}
	return h, observed, nil
}

// investigateForeignAnswer is foreign's answer: the plan's foreign report,
// and the types its sweep covered, since nothing is said about a type the
// sweep did not enumerate.
type investigateForeignAnswer struct {
	Estate     string                  `json:"estate"`
	ObservedAt string                  `json:"observed_at,omitempty"`
	Swept      []string                `json:"swept"`
	Type       string                  `json:"type,omitempty"`
	Count      int                     `json:"count"`
	Foreign    []views.LivePlanForeign `json:"foreign"`
	Text       string                  `json:"text"`
}

func investigateForeign(_ context.Context, in map[string]string) (any, string, error) {
	data, err := os.ReadFile(in["plan"])
	if err != nil {
		return nil, "", investigate.Refusef("cannot read the live-plan document: %s. Write one with choudoufu live-plan -json > plan.json.", err)
	}
	var doc struct {
		Estate     string                   `json:"estate"`
		ObservedAt string                   `json:"observed_at"`
		Foreign    *[]views.LivePlanForeign `json:"foreign"`
		Swept      []string                 `json:"swept"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, "", investigate.Refusef("%s is not a live-plan -json document: %s", in["plan"], err)
	}
	if doc.Foreign == nil {
		return nil, "", investigate.Refusef("%s has no foreign section, so it is not a live-plan -json document", in["plan"])
	}
	ans := investigateForeignAnswer{Estate: doc.Estate, ObservedAt: doc.ObservedAt, Swept: doc.Swept, Type: in["type"], Foreign: []views.LivePlanForeign{}}
	if ans.Swept == nil {
		ans.Swept = []string{}
	}
	for _, f := range *doc.Foreign {
		if ans.Type == "" || f.TypeName == ans.Type {
			ans.Foreign = append(ans.Foreign, f)
		}
	}
	sort.Slice(ans.Foreign, func(i, j int) bool {
		if ans.Foreign[i].TypeName != ans.Foreign[j].TypeName {
			return ans.Foreign[i].TypeName < ans.Foreign[j].TypeName
		}
		return ans.Foreign[i].LiveID < ans.Foreign[j].LiveID
	})
	ans.Count = len(ans.Foreign)
	switch {
	case ans.Type != "" && !contains(ans.Swept, ans.Type):
		ans.Text = fmt.Sprintf("The sweep behind this plan did not enumerate %s, so nothing is said about foreign resources of that type.", ans.Type)
	case ans.Count == 0:
		ans.Text = "No live resource of the swept types is undeclared."
	default:
		ans.Text = fmt.Sprintf("%d live resource(s) of the swept types nobody declares. Report only: nothing here deletes them.", ans.Count)
	}
	return ans, doc.ObservedAt, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func investigateAffected(ctx context.Context, in map[string]string) (any, string, error) {
	dir := in["dir"]
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, "", err
		}
		dir = wd
	}
	res, err := affected.Compute(ctx, affected.Options{RepoDir: dir, Spec: in["range"]})
	if err != nil {
		return nil, "", investigate.Refusef("%s", err)
	}
	return res, "", nil
}

func (c *InvestigateCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] investigate [options] [NAME [INPUT] [name=value ...]]

  Answers one named, read-only question about an estate. With no NAME, or
  with -list, lists the investigations and whether each can run here.

  Native lookups, answered by choudoufu with no graph:

    owner CLOUD_ID       which estate and address own this cloud ID (an AWS
                         ARN): one live read of its markers
    history ADDRESS      a record's past versions, from the record store
                         [estate=name] [dir=path]
    foreign PLAN         the live resources nobody declares, from a
                         live-plan -json document [type=name]
    affected RANGE       which estate roots a git range touches [dir=path]

  Every other NAME is one of chant's investigation templates
  (@intentius/chant/capabilities/templates.json), read from an installed
  chant: chant search --capabilities --json, or the templates.json the
  chant package ships. A path template runs through chant search in
  -project; a Cypher template goes to behold's graph through behold mcp's
  ask, and without a behold binary it is reported unavailable. A template's
  first input is its resource, given bare; the others are name=value.

  Every answer says what it read, what that cost and how fresh it is. No
  answer carries a record's payload. Nothing here plans, applies, runs an
  Op or writes.

Options:

  -list              List the investigations.
  -json              The answer as one JSON document.
  -chant=path        The chant binary; none for no chant.
  -behold=path       The behold binary; none for no behold.
  -templates=file    A templates.json to read instead of chant's.
  -project=dir       Where chant search and behold mcp run.
`)
}

func (c *InvestigateCommand) Synopsis() string {
	return "Answer a named, read-only question about an estate"
}

// McpCommand is "choudoufu mcp" (GitHub issue #2280): a stdio MCP server
// over the same investigations, fronting behold's MCP and passing chant's
// Ops through.
type McpCommand struct {
	Meta

	// stdin and stdout default to the process's.
	stdin  io.Reader
	stdout io.Writer
}

// McpCommander is mcp's entry in the new CLI's command tree.
func McpCommander() Command {
	return mcpCommander(nil, nil)
}

// mcpCommander builds the command over the given streams (nil for the
// process's), so Run keeps the streams set on its McpCommand.
func mcpCommander(stdin io.Reader, stdout io.Writer) Command {
	cmd := Command{
		Name:  "mcp",
		Short: (&McpCommand{}).Synopsis(),
	}
	args := arguments.BindMcp(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&McpCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&McpCommand{Meta: meta, stdin: stdin, stdout: stdout}).Execute(args)
	}
	return cmd
}

func (c *McpCommand) Run(rawArgs []string) int {
	return RunCommand(mcpCommander(c.stdin, c.stdout), c.Meta, rawArgs)
}

func (c *McpCommand) Execute(args *arguments.Mcp) int {
	ctx := c.CommandContext()
	if ctx == nil {
		ctx = context.Background()
	}
	inv := &InvestigateCommand{Meta: c.Meta}
	srv := investigate.NewServer(ctx, investigate.ServerConfig{
		Tools:          investigateTools(args.Neighbours),
		Natives:        inv.natives(),
		AllowOpApprove: args.AllowOpApprove,
		Version:        tfversion.String(),
	})
	defer srv.Close()
	in, out := c.stdin, c.stdout
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if err := srv.Serve(ctx, in, out); err != nil {
		c.View.Diagnostics(tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "The MCP server stopped", err.Error())))
		return 1
	}
	return 0
}

func (c *McpCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] mcp [options]

  A stdio MCP server, the one server a choudoufu user configures:

    claude mcp add choudoufu -- choudoufu mcp

  It serves choudoufu's native lookups (owner, history, foreign,
  affected) and chant's investigation templates (templates lists them,
  investigate runs one), the same investigations as choudoufu investigate.

  With a behold binary present, it starts behold mcp and re-exports its
  ask, show, shot and capabilities under behold:. Without one, none
  appear, and Cypher templates are reported unavailable.

  With a chant project and a chant binary present, it starts chant serve
  mcp and passes op-list, op-status, op-report, lifecycle-diff and op-run
  through under chant:. op-run is the only tool here that changes
  anything, and it goes through chant's gates. op-approve is not passed
  through unless -allow-op-approve is given, so an agent cannot run an Op
  and approve its own gate in one session. Without chant, no chant: tool
  appears.

  Every tool carries readOnlyHint or destructiveHint. The server runs with
  the caller's credentials and adds no permission layer of its own.

Options:

  -allow-op-approve  Pass chant's op-approve through.
  -chant=path        The chant binary; none for no chant.
  -behold=path       The behold binary; none for no behold.
  -templates=file    A templates.json to read instead of chant's.
  -project=dir       The chant project or workspace the children run in.
`)
}

func (c *McpCommand) Synopsis() string {
	return "Serve investigations, behold's graph and chant's Ops over MCP"
}
