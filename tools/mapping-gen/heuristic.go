// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The two-pass acronym-aware PascalCase-to-snake_case conversion: insert an
// underscore before a capitalized word that follows any character (DBInstance
// -> DB_Instance), then before a capital that follows a lowercase letter or
// digit (the AllCap pass catches what the first pass's trailing [a-z]+
// requirement missed), then lowercase. Gets ordinary words and acronym runs
// right (VPC -> vpc, DBInstance -> db_instance) but not every liberty TF's
// own naming took (AutoScalingGroup -> auto_scaling_group, not TF's
// autoscaling_group) - the gap the alias overlay exists to cover.
var (
	matchFirstCap = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
	matchAllCap   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

func pascalToSnake(s string) string {
	s = matchFirstCap.ReplaceAllString(s, "${1}_${2}")
	s = matchAllCap.ReplaceAllString(s, "${1}_${2}")
	return strings.ToLower(s)
}

// splitCFNType splits "AWS::Service::Resource" into its service and
// resource segments.
func splitCFNType(cfnType string) (svc, resource string, ok bool) {
	parts := strings.Split(cfnType, "::")
	if len(parts) != 3 || parts[0] != "AWS" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// nameCandidates returns the TF type name(s) the heuristic guesses for a
// CFN type: the service segment cased two ways - snake_case with word
// boundaries, and a plain lowercase run with none - joined to the
// resource segment, which is always snake_case. Two service casings because
// CloudFront must produce cloudfront, not cloud_front: pascalToSnake sees a
// word boundary neither AWS's own naming nor TF's provider does. Deduplicated,
// so a service whose two casings agree (most of them: S3, IAM, EC2 all
// collapse to one string either way) contributes a single candidate.
func nameCandidates(cfnType string) []string {
	svc, resource, ok := splitCFNType(cfnType)
	if !ok {
		return nil
	}
	resourceSnake := pascalToSnake(resource)
	snakeSvc := pascalToSnake(svc)
	lowerSvc := strings.ToLower(svc)

	seen := map[string]bool{}
	var out []string
	for _, svcCasing := range [...]string{snakeSvc, lowerSvc} {
		candidate := "aws_" + svcCasing + "_" + resourceSnake
		if !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

// buildNameIndex maps every TF candidate name the heuristic derives from the
// CFN roster back to the single CFN type that produced it. When two
// different CFN types independently produce the identical candidate name -
// an ambiguous reverse mapping, not merely two casings of the same type
// agreeing - first-hit-wins does not apply: per the Name heuristic's own
// rule, a collision is an error, reported by name rather than silently
// resolved.
func buildNameIndex(cfnTypes []string) (index map[string]string, err error) {
	contributors := map[string]map[string]bool{} // candidate -> set of contributing CFN types
	for _, cfn := range cfnTypes {
		for _, cand := range nameCandidates(cfn) {
			if contributors[cand] == nil {
				contributors[cand] = map[string]bool{}
			}
			contributors[cand][cfn] = true
		}
	}

	index = make(map[string]string, len(contributors))
	var collisions []string
	for cand, cfns := range contributors {
		if len(cfns) > 1 {
			var list []string
			for c := range cfns {
				list = append(list, c)
			}
			sort.Strings(list)
			collisions = append(collisions, fmt.Sprintf("%s <- %s", cand, strings.Join(list, ", ")))
			continue
		}
		for c := range cfns {
			index[cand] = c
		}
	}
	if len(collisions) > 0 {
		sort.Strings(collisions)
		return nil, fmt.Errorf("name heuristic collisions (%d candidate names, each claimed by more than one CFN type): %s",
			len(collisions), strings.Join(collisions, "; "))
	}
	return index, nil
}
