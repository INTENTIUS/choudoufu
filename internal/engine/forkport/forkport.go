// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package forkport is the registry of choudoufu's fork ports on OpenTofu's
// new runtime (#2376, unit F0 of #2256).
//
// A fork port fills a gap in the new runtime (internal/engine) with the
// fork's own code until upstream's lands, and is built to be deleted:
//
//   - its code is one package, internal/engine/forkport/<name>, plus call
//     sites in upstream code, each commented
//     "choudoufu fork port (<issue>): remove when upstream <#N> lands";
//   - it is listed in [Ports] below, with the gap rows it fills in
//     internal/live/newruntime/gaps.json and the upstream issues whose
//     closing expires it;
//   - each of those rows names it in "fork_port" and its switch in "switch",
//     and lists the same upstream issues;
//   - the port's code, and the gap table's checks for what the port covers,
//     ask [Enabled] whether it is on. Off, the row that refused the
//     construct before the port refuses it again.
//
// internal/live/newruntime's TestForkPortRegistry holds the registry, the
// packages and the gap rows to each other. scripts/upstream-sync.sh
// --report prints each port's expiry issues and flags
// "EXPIRE: remove port <name>" once all of them have closed.
package forkport

import (
	"fmt"
	"os"
	"strings"
)

// AllOff turns every fork port off when set to any non-empty value.
const AllOff = "CHOUDOUFU_NEW_RUNTIME_NO_FORK_PORTS"

// switchPrefix, followed by a port's name in upper case, turns that one
// port off when set to any non-empty value. See [Switch].
const switchPrefix = "CHOUDOUFU_NEW_RUNTIME_NO_PORT_"

// Port is one registered fork port.
type Port struct {
	// Name is the port's package name, internal/engine/forkport/<Name>.
	Name string
	// Rows are the ids of the gap rows the port fills.
	Rows []string
	// Expiry lists the upstream opentofu/opentofu issues, as "#NNNN", that
	// end the port: when every one has closed, upstream's code replaces it.
	// Empty when no upstream issue is filed: the port goes when its rows close.
	Expiry []string
}

// Ports is the registry. Adding a port is one entry here, its package, its
// call sites and its gap row.
var Ports = []Port{
	{Name: "inrunestates", Rows: []string{"in-run-estates"}, Expiry: []string{"#4058"}},
	{Name: "conditions", Rows: []string{"variable-validation"}},
}

// Lookup returns the registered port with name, and whether there is one.
func Lookup(name string) (Port, bool) {
	for _, p := range Ports {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

// Switch is the variable that turns the named port off:
// CHOUDOUFU_NEW_RUNTIME_NO_PORT_<NAME>.
func Switch(name string) string {
	return switchPrefix + strings.ToUpper(name)
}

// Enabled reports whether the named port is on: neither [AllOff] nor its
// own [Switch] is set. This is the only place either is read. It panics on
// a name that is not registered, so a port cannot be consulted before it
// is listed.
func Enabled(name string) bool {
	if _, ok := Lookup(name); !ok {
		panic(fmt.Sprintf("forkport: %q is not a registered fork port", name))
	}
	return os.Getenv(AllOff) == "" && os.Getenv(Switch(name)) == ""
}
