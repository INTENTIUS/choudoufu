// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// This file reads two things a generated resource's id format leaves to
// the code around it (#2145 item 7).
//
// The first is what the Create does to {{location}} when the schema marks
// it optional and computed. ReplaceVars substitutes d.GetOkExists, so an
// unset location renders empty unless something sets it before the id is
// built. Three shapes occur at v8.6.0:
//
//   - nothing sets it: the location is configuration's, and an instance
//     that leaves it out has none (artifact_registry_rule);
//   - an encoder sets it from tpgresource.GetRegion when it is unset
//     (artifact_registry_repository and project_config);
//   - something else sets it, from another argument's value or the zone,
//     which a row does not compose yet and is named in the refusal.
//
// The second is what ReplaceVars substitutes for a placeholder the resource
// has no attribute for: a field of transport_tpg.Config by that exact name,
// else the empty string (tpgresource.BuildReplacementFunc).

// LocationSet values ([Source.LocationSet]).
const (
	// LocationSetNone: neither the Create nor the encoder it calls sets
	// location before the id.
	LocationSetNone = "none"
	// LocationSetRegion: the encoder sets an unset location from
	// tpgresource.GetRegion, and nothing else sets it.
	LocationSetRegion = "provider-region"
	// LocationSetElsewhere: the Create does not build its id from the id
	// format at all (the importer's ReplaceVars was what matched).
	LocationSetElsewhere = "create-elsewhere"
)

var (
	locationSetRe = regexp.MustCompile(`d\.Set\("location", [^)\n]*\)`)
	// extractLocationRe is an encoder taking location from another
	// argument's value: `location, err = tpgresource.ExtractFieldByPattern("location", location, bucket, "...")`.
	extractLocationRe = regexp.MustCompile(`location, err :?= tpgresource\.ExtractFieldByPattern\("location", location, \w+, "[^"\n]*"\)`)
	// regionDefaultRe is the encoder's whole location rule:
	//
	//	if _, ok := d.GetOk("location"); !ok {
	//		location, err := tpgresource.GetRegion(d, config)
	//		if err != nil {
	//			return nil, fmt.Errorf("Cannot determine location: ...")
	//		}
	//		if err := d.Set("location", location); err != nil {
	regionDefaultRe = regexp.MustCompile(`if _, ok := d\.GetOk\("location"\); !ok \{\s*location, err := tpgresource\.GetRegion\(d, config\)\s*if err != nil \{[^}]*\}\s*if err := d\.Set\("location", location\)`)
)

// readLocationSet records [Source.LocationSet] and where, for a generated
// resource whose id format names {{location}}.
func readLocationSet(s *Source, text string) {
	if !containsStr(placeholders(s.IDFormat), "location") {
		return
	}
	fns := createFuncRe.FindAllStringSubmatchIndex(text, -1)
	if len(fns) != 1 {
		s.LocationSet = LocationSetElsewhere
		return
	}
	prefix := text[fns[0][2]:fns[0][3]]
	bodyStart := fns[0][4]
	body := text[bodyStart:fns[0][5]]
	if m := idFormatRe.FindStringSubmatch(body); m == nil || m[2] != s.IDFormat {
		s.LocationSet = LocationSetElsewhere
		return
	}
	type set struct {
		at   int
		text string
	}
	var sets []set
	for _, loc := range locationSetRe.FindAllStringIndex(body, -1) {
		sets = append(sets, set{bodyStart + loc[0], body[loc[0]:loc[1]]})
	}
	encoder := ""
	if strings.Contains(body, "obj, err = "+prefix+"Encoder(d, meta, obj)") {
		if loc := regexp.MustCompile(`\nfunc ` + regexp.QuoteMeta(prefix) + `Encoder\(`).FindStringIndex(text); loc != nil {
			encoder = funcBody(text, loc[0]+1)
			for _, l := range locationSetRe.FindAllStringIndex(encoder, -1) {
				sets = append(sets, set{loc[0] + 1 + l[0], encoder[l[0]:l[1]]})
			}
		}
	}
	switch {
	case len(sets) == 0:
		s.LocationSet = LocationSetNone
	case len(sets) == 1 && regionDefaultRe.MatchString(encoder):
		s.LocationSet = LocationSetRegion
		s.LocationSetLine = lineOf(text, sets[0].at)
	default:
		s.LocationSet = strings.TrimSpace(sets[0].text)
		s.LocationSetLine = lineOf(text, sets[0].at)
		if encoder != "" {
			if loc := extractLocationRe.FindStringIndex(encoder); loc != nil {
				// The value set is what this statement extracted.
				encAt := strings.Index(text, encoder)
				s.LocationSet = encoder[loc[0]:loc[1]]
				s.LocationSetLine = lineOf(text, encAt+loc[0])
			}
		}
	}
}

func lineOf(text string, at int) int {
	return strings.Count(text[:at], "\n") + 1
}

// configFieldRe is one field line of `type Config struct`.
var configFieldRe = regexp.MustCompile(`(?m)^\t(\w+)\s`)

// configFields reads the field names of transport_tpg.Config, which
// ReplaceVars substitutes by reflection for a placeholder the resource has
// no value for. An unreadable struct is an error, never an empty list: an
// empty list would read as "no placeholder names a field".
func configFields(dir string) ([]string, error) {
	path := filepath.Join(sourceRoot(dir), "transport", "config.go")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	start := strings.Index(text, "\ntype Config struct {\n")
	if start < 0 {
		return nil, fmt.Errorf("%s declares no Config struct", path)
	}
	body := text[start+len("\ntype Config struct {\n"):]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		return nil, fmt.Errorf("%s: the Config struct does not close", path)
	}
	var out []string
	for _, m := range configFieldRe.FindAllStringSubmatch(body[:end+1], -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no field read from the Config struct", path)
	}
	sort.Strings(out)
	return out, nil
}

// readConfigPlaceholders records [Source.ConfigPlaceholders]: each
// placeholder of the id format that names a Config field, or ends in
// BasePath, which ReplaceVars fills from the provider configuration when
// the resource has no value for it.
func readConfigPlaceholders(s *Source, fields []string) {
	for _, name := range placeholders(s.IDFormat) {
		if strings.HasSuffix(name, "BasePath") || containsStr(fields, name) {
			s.ConfigPlaceholders = append(s.ConfigPlaceholders, name)
		}
	}
}

// replaceVarsSpecial are the placeholders BuildReplacementFunc answers
// before it reads the resource at all.
var replaceVarsSpecial = map[string]bool{
	"project": true, "project_id_or_project": true, "region": true, "zone": true,
}

// rendersEmpty reports whether ReplaceVars substitutes the empty string for
// placeholder name of src's id format on every instance: the resource has
// no attribute by that name (flag ""), it is none of the placeholders
// ReplaceVars answers itself, and it names no Config field.
func rendersEmpty(src Source, flag, name string) bool {
	return flag == "" && !replaceVarsSpecial[name] && !containsStr(src.ConfigPlaceholders, name)
}

// locationRefusal is the reason a {{location}} the Create sets in a way a
// row does not compose is refused, or "" when src.LocationSet is a shape
// decideResource composes.
func locationRefusal(src Source) string {
	switch src.LocationSet {
	case LocationSetNone, LocationSetRegion:
		return ""
	case "":
		return "the id format names {{location}}, which the schema marks optional and computed, and the extract recorded nothing about how the Create sets it"
	case LocationSetElsewhere:
		return "the id format names {{location}}, but the Create does not build its id from that format (the importer's ReplaceVars is what was read), so which location it uses is not this template's"
	}
	return fmt.Sprintf("the Create sets an unset {{location}} with `%s` (%s:%d), from a value a row does not compose yet", src.LocationSet, filepath.Base(src.File), src.LocationSetLine)
}
