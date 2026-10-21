// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"
)

// TestEmptySegmentExtraction reads the two schema facts the empty-segment
// rule needs out of a resource file's schema map: any Default, string or
// not, and an AtLeastOneOf group. A non-string Default rules the argument
// out exactly as a string one does.
func TestEmptySegmentExtraction(t *testing.T) {
	text := `
		Schema: map[string]*schema.Schema{
			"fqdn": {
				Type:         schema.TypeString,
				Optional:     true,
				AtLeastOneOf: []string{"fqdn", "ip_address"},
			},
			"ip_address": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"port": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  443,
			},
		},
`
	if schemaHasDefault(text, "ip_address") || !schemaHasDefault(text, "port") {
		t.Errorf("schemaHasDefault: ip_address %v, port %v; want false, true", schemaHasDefault(text, "ip_address"), schemaHasDefault(text, "port"))
	}
	if _, ok := schemaDefault(text, "port"); ok {
		t.Errorf("a non-string Default read as a string one")
	}
	if got := schemaAtLeastOneOf(text, "fqdn"); !reflect.DeepEqual(got, []string{"fqdn", "ip_address"}) {
		t.Errorf("schemaAtLeastOneOf(fqdn) = %v", got)
	}

	d := Decision{importFormats: [][]string{{`(?P<g>[^/]+)/(?P<port>[^/]*)`}}}
	if emptySegment(d, Source{OtherDefaults: []string{"port"}}, "O", "port") {
		t.Errorf("an argument with a non-string Default renders empty")
	}
	if !emptySegment(d, Source{}, "O", "port") {
		t.Errorf("an optional argument with no default that every format captures with [^/]* does not render empty")
	}
	if emptySegment(d, Source{}, "OC", "port") {
		t.Errorf("an optional, computed argument renders empty")
	}
	d.importFormats = append(d.importFormats, []string{`(?P<g>[^/]+)/(?P<port>[^/]+)`})
	if emptySegment(d, Source{}, "O", "port") {
		t.Errorf("an argument one provider's format captures with [^/]+ renders empty")
	}
}
