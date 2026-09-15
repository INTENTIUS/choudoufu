// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command cost-project projects what standing an estate up on real AWS would
// cost, before anyone authorises the run.
//
// The maintainer's rule is that heavy and paid runs are theirs, by hand. This
// tool exists to make that decision on a number rather than an impression:
// point it at a plan and it says what the estate would cost for a run of a
// given length, itemised, in the region and at the prices live/aws-prices.json
// names.
//
// # The rule this tool is built around
//
// An unpriced type is never treated as free. A cost projector that silently
// prices what it does not know at zero is worse than no projector, because it
// produces a small, confident number that someone then acts on. So: every type
// in the plan is either priced by name in live/aws-prices.json, or reported as
// unpriced, and while any are unpriced the total is labelled a LOWER BOUND and
// the exit code is non-zero. `-strict` turns that into a refusal to print a
// total at all.
//
// Two modelling facts the table carries and this tool honours:
//
//   - Hourly resources are prorated by the run's length. A NAT gateway for
//     three hours is three hours of NAT gateway.
//   - Monthly resources are not always prorated. A Route 53 hosted zone is
//     billed for the whole month even if it lives for an hour, so a
//     three-hour run pays the same $0.50 as a thirty-day one. Getting that
//     backwards understates a short run by a factor of two hundred.
//
// What it does not model, and says so on every run: data transfer, request
// charges, storage, and instance sizing. `aws_instance` is priced as
// t3.micro because the table does not read instance_type. Those exclusions
// are printed, not buried, because the number is a projection and not a quote.
//
// # Input
//
// A plan in JSON, which is exact and available before anything is created:
//
//	terraform plan -out=tfplan && terraform show -json tfplan > plan.json
//	go run ./tools/cost-project -plan plan.json -hours 3
//
// Or a type/count inventory, for projecting a shape that has no plan yet:
//
//	go run ./tools/cost-project -counts aws_nat_gateway=3,aws_kms_key=4 -hours 3
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PricesPath is the hand-maintained table. It lives beside the other things a
// human curates deliberately, not under tools/, because it is data about the
// world rather than part of this program.
const PricesPath = "live/aws-prices.json"

type price struct {
	Free       bool     `json:"free"`
	HourlyUSD  *float64 `json:"hourly_usd"`
	MonthlyUSD *float64 `json:"monthly_usd"`
	Prorated   *bool    `json:"prorated"`
	Note       string   `json:"note"`
}

type table struct {
	Region   string           `json:"region"`
	PricedOn string           `json:"priced_on"`
	Types    map[string]price `json:"types"`
}

// hoursPerMonth is what a monthly charge is prorated against. 730 is the
// conventional AWS figure (365*24/12), not 720.
const hoursPerMonth = 730.0

type line struct {
	Type  string
	Count int
	USD   float64
	How   string
}

func main() {
	planPath := flag.String("plan", "", "path to `terraform show -json` output")
	counts := flag.String("counts", "", "comma-separated type=count pairs, for a shape with no plan yet")
	hours := flag.Float64("hours", 3, "how long the estate would stand, in hours")
	strict := flag.Bool("strict", false, "refuse to print a total at all while any type is unpriced")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	tbl, err := loadTable(filepath.Join(root, PricesPath))
	if err != nil {
		fatal(err)
	}

	var inv map[string]int
	switch {
	case *planPath != "" && *counts != "":
		fatal(fmt.Errorf("-plan and -counts are alternatives; give one"))
	case *planPath != "":
		inv, err = inventoryFromPlan(*planPath)
	case *counts != "":
		inv, err = inventoryFromCounts(*counts)
	default:
		fatal(fmt.Errorf("give -plan <file> or -counts <type=n,...>"))
	}
	if err != nil {
		fatal(err)
	}
	if len(inv) == 0 {
		fatal(fmt.Errorf("the plan creates nothing; there is no cost to project"))
	}

	priced, free, unpriced, total := project(tbl, inv, *hours)
	report(tbl, priced, free, unpriced, total, inv, *hours, *strict)

	if len(unpriced) > 0 {
		os.Exit(1)
	}
}

func project(tbl *table, inv map[string]int, hours float64) (priced, free []line, unpriced []line, total float64) {
	for typeName, n := range inv {
		p, ok := tbl.Types[typeName]
		if !ok {
			unpriced = append(unpriced, line{Type: typeName, Count: n})
			continue
		}
		switch {
		case p.Free:
			free = append(free, line{Type: typeName, Count: n, How: "free" + noteSuffix(p.Note)})
		case p.HourlyUSD != nil:
			usd := *p.HourlyUSD * float64(n) * hours
			total += usd
			priced = append(priced, line{Type: typeName, Count: n, USD: usd,
				How: fmt.Sprintf("$%.4f/h x %.1fh", *p.HourlyUSD, hours)})
		case p.MonthlyUSD != nil:
			prorated := p.Prorated != nil && *p.Prorated
			usd := *p.MonthlyUSD * float64(n)
			how := fmt.Sprintf("$%.2f/mo, NOT prorated", *p.MonthlyUSD)
			if prorated {
				usd = usd * hours / hoursPerMonth
				how = fmt.Sprintf("$%.2f/mo prorated over %.1fh", *p.MonthlyUSD, hours)
			}
			total += usd
			priced = append(priced, line{Type: typeName, Count: n, USD: usd, How: how})
		default:
			// An entry with no price and no free flag is a half-written row,
			// and guessing which it meant is the whole failure this tool
			// exists to avoid.
			unpriced = append(unpriced, line{Type: typeName, Count: n})
		}
	}
	sort.Slice(priced, func(i, j int) bool { return priced[i].USD > priced[j].USD })
	sort.Slice(free, func(i, j int) bool { return free[i].Count > free[j].Count })
	sort.Slice(unpriced, func(i, j int) bool { return unpriced[i].Type < unpriced[j].Type })
	return priced, free, unpriced, total
}

func report(tbl *table, priced, free, unpriced []line, total float64, inv map[string]int, hours float64, strict bool) {
	objects := 0
	for _, n := range inv {
		objects += n
	}
	fmt.Printf("projected cost for %d objects across %d types, standing for %.1fh\n", objects, len(inv), hours)
	fmt.Printf("prices: %s, on-demand list, dated %s (%s)\n\n", tbl.Region, tbl.PricedOn, PricesPath)

	if len(priced) > 0 {
		fmt.Println("charged:")
		for _, l := range priced {
			fmt.Printf("  %-34s %6d   %-34s $%8.2f\n", l.Type, l.Count, l.How, l.USD)
		}
		fmt.Println()
	}
	if len(free) > 0 {
		fmt.Println("no charge:")
		for _, l := range free {
			fmt.Printf("  %-34s %6d   %s\n", l.Type, l.Count, l.How)
		}
		fmt.Println()
	}

	if len(unpriced) > 0 {
		n := 0
		names := make([]string, 0, len(unpriced))
		for _, l := range unpriced {
			n += l.Count
			names = append(names, fmt.Sprintf("%s (%d)", l.Type, l.Count))
		}
		fmt.Printf("UNPRICED - %d type(s), %d object(s), NOT counted in the total:\n", len(unpriced), n)
		for _, s := range names {
			fmt.Printf("  %s\n", s)
		}
		fmt.Printf("\nAdd them to %s before trusting a total. An absent type is unchecked, never free.\n\n", PricesPath)
		if strict {
			fmt.Println("no total printed: -strict and the inventory contains unpriced types")
			return
		}
		fmt.Printf("LOWER BOUND: $%.2f for %.1fh\n", total, hours)
	} else {
		fmt.Printf("TOTAL: $%.2f for %.1fh\n", total, hours)
	}

	fmt.Println()
	fmt.Println("not modelled, and excluded from every figure above: data transfer,")
	fmt.Println("request and invocation charges, stored data, and instance sizing")
	fmt.Println("(aws_instance is priced as t3.micro; this table does not read")
	fmt.Println("instance_type). A projection, not a quote.")
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " - " + note
}

func inventoryFromPlan(path string) (map[string]int, error) {
	b, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path, the point of the flag
	if err != nil {
		return nil, err
	}
	var plan struct {
		ResourceChanges []struct {
			Type   string `json:"type"`
			Mode   string `json:"mode"`
			Change struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return nil, fmt.Errorf("parsing %s as `terraform show -json` output: %w", path, err)
	}
	inv := map[string]int{}
	for _, rc := range plan.ResourceChanges {
		if rc.Mode != "managed" {
			continue
		}
		for _, a := range rc.Change.Actions {
			if a == "create" {
				inv[rc.Type]++
				break
			}
		}
	}
	return inv, nil
}

func inventoryFromCounts(spec string) (map[string]int, error) {
	inv := map[string]int{}
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not type=count", pair)
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%q does not name a count", pair)
		}
		inv[strings.TrimSpace(k)] += n
	}
	return inv, nil
}

func loadTable(path string) (*table, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a fixed path under the repo root
	if err != nil {
		return nil, err
	}
	var t table
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if t.Region == "" || t.PricedOn == "" {
		return nil, fmt.Errorf("%s must name a region and a priced_on date - a price with no region or date is not checkable", path)
	}
	return &t, nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "cost-project: %v\n", err)
	os.Exit(2)
}
