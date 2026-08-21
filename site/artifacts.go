package main

// Artifact reading for the progress surface.
//
// Every figure the site shows about how far the project has got is read from
// a committed artifact under live/ at build time. Nothing here computes a
// number, and nothing on the site types one by hand: if a chart is wrong, the
// artifact behind it is wrong and regenerating it fixes the page.
//
// Three artifacts, three different questions, three different denominators.
// They are deliberately not merged into one score.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// bar is one row of a horizontal bar chart. Note is the plain-language gloss
// shown beside the count; the artifacts carry insider class names and the
// site is responsible for saying what they mean to a reader.
type bar struct {
	Label   string
	Note    string
	Value   int
	Percent float64 // of the chart's denominator, for the bar width
	Muted   bool    // render at low emphasis (an empty or not-yet-reached rung)

	// Unmeasured, when set, replaces the bar and its number with this
	// phrase. It is for a row the source does not measure at all, which is
	// not the same as a row it measures as zero. Drawing an empty bar for
	// an unmeasured row would publish a figure nobody produced.
	Unmeasured string
}

// chart is one measured question. Denominator is stated on the page rather
// than implied, because the three charts do not share one.
type chart struct {
	ID          string
	Title       string
	Denominator string
	Bars        []bar
	Href        string
	HrefLabel   string
}

// ---------------------------------------------------------------------------
// Axis 1: does a configuration load and pass lint?
// ---------------------------------------------------------------------------

type refusalsArtifact struct {
	Ladder struct {
		Origins []string `json:"origins"`
		Classes []struct {
			Class   string `json:"class"`
			Configs int    `json:"configs"`
		} `json:"classes"`
		UnadmittedDemand []struct {
			Type    string `json:"type"`
			Configs int    `json:"configs"`
		} `json:"unadmitted_demand"`
	} `json:"ladder"`
	Populations []struct {
		Origin  string `json:"origin"`
		Configs int    `json:"configs"`
		Clean   int    `json:"clean"`
		Blocked int    `json:"blocked"`
		ReadsAs string `json:"reads_as"`
	} `json:"populations"`
}

// ladderGloss maps the artifact's class names onto what they mean for someone
// deciding whether their own configuration is in the working set. The
// artifact keeps its own vocabulary; this is the only place the two are tied
// together, so renaming a rung on the site never touches live/.
var ladderGloss = map[string]string{
	"clean":              "works today",
	"backend-only":       "needs a backend change",
	"admissions-only":    "waiting on a type",
	"data-read-eligible": "works if the read succeeds",
	"language-blocked":   "needs language work",
	"unreadable":         "will not parse",
}

func loadLintLadder(root string) (chart, error) {
	var a refusalsArtifact
	if err := readJSON(filepath.Join(root, "live", "corpus-refusals.json"), &a); err != nil {
		return chart{}, err
	}

	total := 0
	for _, c := range a.Ladder.Classes {
		total += c.Configs
	}
	if total == 0 {
		return chart{}, fmt.Errorf("live/corpus-refusals.json: ladder has no configs")
	}

	c := chart{
		ID:          "lint",
		Title:       "Passes live-check",
		Denominator: fmt.Sprintf("%d deployments nobody here wrote, checked offline with no credentials", total),
		Href:        "progress.html",
		HrefLabel:   "what stops the rest",
	}
	for _, cl := range a.Ladder.Classes {
		note := ladderGloss[cl.Class]
		if note == "" {
			note = cl.Class
		}
		c.Bars = append(c.Bars, bar{
			Label:   note,
			Note:    cl.Class,
			Value:   cl.Configs,
			Percent: pct(cl.Configs, total),
			Muted:   cl.Configs == 0,
		})
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// Axis 2: does it round-trip when it is actually run?
// ---------------------------------------------------------------------------

type crossingArtifact struct {
	StageOrder []string `json:"stage_order"`
	Totals     struct {
		Estates int `json:"estates"`
	} `json:"totals"`
	RawTotals map[string]json.RawMessage `json:"-"`
	Estates   []struct {
		Dir    string            `json:"dir"`
		Source string            `json:"source"`
		Lane   string            `json:"lane"`
		Stages map[string]string `json:"stages"`
		Notes  string            `json:"notes"`
	} `json:"estates"`
}

// stageGloss turns the pipeline's stage names into what each one proves.
var stageGloss = map[string]string{
	"cold_deploy":      "stands up from nothing",
	"migrate":          "adopts its markers",
	"test_plan":        "replans empty, no state file",
	"test_apply":       "applies again cleanly",
	"drift_reconverge": "drifts and reconverges",
}

func loadCrossingLadder(root string) (chart, []estate, error) {
	var a crossingArtifact
	path := filepath.Join(root, "live", "corpus-crossing-manifest.json")
	if err := readJSON(path, &a); err != nil {
		return chart{}, nil, err
	}
	total := len(a.Estates)
	if total == 0 {
		return chart{}, nil, fmt.Errorf("%s: no estates", path)
	}

	c := chart{
		ID:          "crossing",
		Title:       "Survives a real run",
		Denominator: fmt.Sprintf("%d estates run end to end", total),
		Href:        "estates.html",
		HrefLabel:   "all estates",
	}
	// Count passes per stage from the estates themselves rather than the
	// artifact's totals block, so the bars and the estate table can never
	// disagree about the same run.
	for _, stage := range a.StageOrder {
		pass := 0
		for _, e := range a.Estates {
			if e.Stages[stage] == "pass" {
				pass++
			}
		}
		note := stageGloss[stage]
		if note == "" {
			note = stage
		}
		c.Bars = append(c.Bars, bar{
			Label:   note,
			Note:    stage,
			Value:   pass,
			Percent: pct(pass, total),
			Muted:   pass == 0,
		})
	}

	var estates []estate
	for _, e := range a.Estates {
		reached := ""
		for _, stage := range a.StageOrder {
			if e.Stages[stage] == "pass" {
				reached = stageGloss[stage]
			}
		}
		if reached == "" {
			reached = "does not stand up"
		}
		estates = append(estates, estate{
			Name:     strings.TrimPrefix(filepath.Base(e.Dir), "corpus-"),
			Dir:      e.Dir,
			Source:   e.Source,
			Lane:     e.Lane,
			Reached:  reached,
			Complete: allPass(a.StageOrder, e.Stages),
		})
	}
	sort.SliceStable(estates, func(i, j int) bool {
		if estates[i].Complete != estates[j].Complete {
			return estates[i].Complete
		}
		return estates[i].Name < estates[j].Name
	})
	return c, estates, nil
}

// estate is one row of the estates table and, later, one page.
type estate struct {
	Name     string
	Dir      string
	Source   string
	Lane     string
	Reached  string
	Complete bool
}

func allPass(order []string, stages map[string]string) bool {
	for _, s := range order {
		if stages[s] != "pass" {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Axis 3: can IAM govern a resource at all?
// ---------------------------------------------------------------------------
//
// This one is not a measure of this project. It is AWS's own coverage of the
// condition key the whole governance story rests on, read from their Service
// Authorization Reference. Where it is low, nothing choudoufu does can raise
// it, and a reader deciding whether per-resource scoping will work for their
// estate needs to see it.

type iamArtifact struct {
	Counts struct {
		Services                   int `json:"services"`
		Resolved                   int `json:"resolved"`
		ServicesListingResourceTag int `json:"services_listing_resource_tag"`
	} `json:"counts"`
	Rows []struct {
		Service   string `json:"service"`
		IAMPrefix string `json:"iam_prefix"`

		ActionsTotal int `json:"actions_total"`

		// ActionsTag is a *pointer* on purpose. The artifact writes null,
		// not 0, for a service whose reference never names aws:ResourceTag
		// as a condition key. Those are different claims: "supports it on
		// zero actions" versus "does not list it at all". Rendering null as
		// 0 invents a measurement AWS never published, so the site has to
		// carry the third state through to the page.
		ActionsTag *int `json:"actions_listing_resource_tag"`

		ListsResourceTag bool `json:"lists_resource_tag"`
	} `json:"rows"`
}

// iamHeadline is the service set an estate is actually made of. Showing the
// whole 180-service roster on the front page would bury the answer; these are
// the ones a reader's estate will lean on.
var iamHeadline = []string{"ec2", "s3", "iam", "lambda", "rds", "ecs", "elasticloadbalancing", "kms"}

func loadIAMReach(root string) (chart, error) {
	var a iamArtifact
	if err := readJSON(filepath.Join(root, "live", "iam-reference.json"), &a); err != nil {
		return chart{}, err
	}
	// The artifact carries duplicate rows for some prefixes (rds and
	// elasticloadbalancing each appear more than once). Fold them, and treat
	// disagreement between duplicates as fatal rather than letting map order
	// decide which figure the site publishes.
	type reach struct {
		name  string
		total int
		tag   *int
		lists bool
	}
	byPrefix := map[string]reach{}
	for _, r := range a.Rows {
		if r.ActionsTotal == 0 {
			continue
		}
		got := reach{name: r.Service, total: r.ActionsTotal, tag: r.ActionsTag, lists: r.ListsResourceTag}
		if prev, seen := byPrefix[r.IAMPrefix]; seen {
			if prev.total != got.total || !sameIntPtr(prev.tag, got.tag) {
				return chart{}, fmt.Errorf(
					"live/iam-reference.json: duplicate rows for %q disagree", r.IAMPrefix)
			}
			continue
		}
		byPrefix[r.IAMPrefix] = got
	}

	c := chart{
		ID:    "iam",
		Title: "IAM can scope it",
		Denominator: fmt.Sprintf("aws:ResourceTag actions. %d of %d AWS services name the key.",
			a.Counts.ServicesListingResourceTag, a.Counts.Services),
		Href:      "governance.html",
		HrefLabel: "full roster",
	}
	for _, p := range iamHeadline {
		r, ok := byPrefix[p]
		if !ok {
			continue
		}
		name := r.name
		if name == "" {
			name = p
		}
		// No count published: AWS's reference does not name the condition
		// key for this service. Say that, rather than drawing a zero.
		if r.tag == nil {
			c.Bars = append(c.Bars, bar{
				Label:      name,
				Note:       fmt.Sprintf("of %d actions", r.total),
				Unmeasured: "key not named",
				Muted:      true,
			})
			continue
		}
		c.Bars = append(c.Bars, bar{
			Label:   name,
			Note:    fmt.Sprintf("%d of %d actions", *r.tag, r.total),
			Value:   *r.tag,
			Percent: pct(*r.tag, r.total),
			Muted:   *r.tag == 0,
		})
	}
	if len(c.Bars) == 0 {
		return chart{}, fmt.Errorf("live/iam-reference.json: none of the headline services resolved")
	}
	return c, nil
}

func sameIntPtr(a, b *int) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// ---------------------------------------------------------------------------

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

func pct(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// ---------------------------------------------------------------------------
// Data for the destination pages.
//
// The landing page shows a headline slice of each artifact. Progress,
// Governance and Estates show the whole thing, so they get their own loaders
// rather than re-reading what the charts already narrowed.
// ---------------------------------------------------------------------------

// population is one origin in the corpus, with the manifest's own ruling on
// whether its blocked count may be read as a rate. Publishing the ruling
// beside the number is the point: only one of these populations is written by
// people outside this project, and only that one measures the promise.
type population struct {
	Origin  string
	Configs int
	Clean   int
	Blocked int
	ReadsAs string
	IsRate  bool
}

// demand is one unadmitted resource type, ranked by how many configurations
// it blocks. This is the build-next list, weighted by real demand rather than
// by what is convenient to add.
type demand struct {
	Type    string
	Configs int
}

// service is one row of the full IAM reach roster.
type service struct {
	Name       string
	Prefix     string
	Total      int
	Tag        int
	Percent    float64
	Unmeasured bool
}

// progressData backs progress.html.
type progressData struct {
	Populations []population
	Demand      []demand
	TotalDemand int

	// OtherProvider counts demand entries for a provider this project does
	// not support. They are dropped from the list rather than shown, since
	// no amount of work here admits them.
	OtherProvider int
}

func loadProgress(root string) (progressData, error) {
	var a refusalsArtifact
	if err := readJSON(filepath.Join(root, "live", "corpus-refusals.json"), &a); err != nil {
		return progressData{}, err
	}
	var d progressData
	for _, p := range a.Populations {
		d.Populations = append(d.Populations, population{
			Origin:  p.Origin,
			Configs: p.Configs,
			Clean:   p.Clean,
			Blocked: p.Blocked,
			ReadsAs: p.ReadsAs,
			IsRate:  p.ReadsAs == "rate",
		})
	}
	// The corpus contains configurations using other providers, so the raw
	// demand list carries types like google_service_account. This project is
	// AWS only, so those will never be admitted and listing them as pending
	// work would promise something that is not coming. Count them, drop
	// them, and say so on the page.
	for _, u := range a.Ladder.UnadmittedDemand {
		if !strings.HasPrefix(u.Type, "aws_") {
			d.OtherProvider++
			continue
		}
		d.TotalDemand++
		if len(d.Demand) < 20 {
			d.Demand = append(d.Demand, demand{Type: u.Type, Configs: u.Configs})
		}
	}
	return d, nil
}

// governanceData backs governance.html: the whole IAM roster, split by
// whether AWS names the condition key for that service at all.
type governanceData struct {
	Named    []service
	Unnamed  []service
	Services int
	Resolved int
}

func loadGovernance(root string) (governanceData, error) {
	var a iamArtifact
	if err := readJSON(filepath.Join(root, "live", "iam-reference.json"), &a); err != nil {
		return governanceData{}, err
	}
	g := governanceData{Services: a.Counts.Services, Resolved: a.Counts.Resolved}
	seen := map[string]bool{}
	for _, r := range a.Rows {
		if r.ActionsTotal == 0 || seen[r.IAMPrefix] {
			continue
		}
		seen[r.IAMPrefix] = true
		name := r.Service
		if name == "" {
			name = r.IAMPrefix
		}
		s := service{Name: name, Prefix: r.IAMPrefix, Total: r.ActionsTotal}
		if r.ActionsTag == nil {
			s.Unmeasured = true
			g.Unnamed = append(g.Unnamed, s)
			continue
		}
		s.Tag = *r.ActionsTag
		s.Percent = pct(s.Tag, s.Total)
		g.Named = append(g.Named, s)
	}
	sort.SliceStable(g.Named, func(i, j int) bool { return g.Named[i].Tag > g.Named[j].Tag })
	sort.SliceStable(g.Unnamed, func(i, j int) bool { return g.Unnamed[i].Total > g.Unnamed[j].Total })
	return g, nil
}

// dataPages is everything the artifact-backed pages render from, loaded once
// so Progress, Governance and Estates read the same numbers the landing
// charts do.
type dataPages struct {
	Lint     chart
	Crossing chart
	IAM      chart
	Estates  []estate
	Progress progressData
	Gov      governanceData
}

func loadDataPages(root string) (dataPages, error) {
	var d dataPages
	var err error
	if d.Lint, err = loadLintLadder(root); err != nil {
		return d, err
	}
	if d.Crossing, d.Estates, err = loadCrossingLadder(root); err != nil {
		return d, err
	}
	if d.IAM, err = loadIAMReach(root); err != nil {
		return d, err
	}
	if d.Progress, err = loadProgress(root); err != nil {
		return d, err
	}
	if d.Gov, err = loadGovernance(root); err != nil {
		return d, err
	}
	return d, nil
}
