// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"fmt"
	"strings"
)

// EnvVar is the kill switch. Unset or empty, every registered override is
// on. "off" turns all of them off. A comma-separated list of "-name"
// entries turns off only those.
const EnvVar = "CHOUDOUFU_PROVIDER_OVERRIDES"

// Enabled resolves the kill switch's value to the overrides that are on.
// A name that is not registered, or an entry without its leading "-", is an
// error: a typo in a kill switch must not leave the override it meant to
// stop running.
func Enabled(value string) ([]Override, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return All(), nil
	}
	if value == "off" {
		return nil, nil
	}
	off := map[string]bool{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, ok := strings.CutPrefix(entry, "-")
		if !ok {
			return nil, fmt.Errorf("%s: %q is not \"off\" or a -name entry; every override is on unless turned off, so the only entries are -name", EnvVar, entry)
		}
		if _, known := Lookup(name); !known {
			return nil, fmt.Errorf("%s: no provider override is named %q; the registered names are %s", EnvVar, name, strings.Join(names(All()), ", "))
		}
		off[name] = true
	}
	var on []Override
	for _, o := range All() {
		if !off[o.Name] {
			on = append(on, o)
		}
	}
	return on, nil
}

func names(set []Override) []string {
	out := make([]string, len(set))
	for i, o := range set {
		out[i] = o.Name
	}
	return out
}
