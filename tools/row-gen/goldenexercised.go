// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// identityGoldenRel is internal/live/check's own golden fixture, read here
// as data rather than imported as a package - this file only ever reads its
// rendered addresses, never its resolution logic.
const identityGoldenRel = "internal/live/check/testdata/identity-golden.txt"

// goldenExercisedTypes is schemafirst.go's own safety net: every
// provider-local resource type that appears anywhere in
// internal/live/check's identity golden - the fixture TestIdentityGolden
// holds byte-identical, analyzed with NO provider schemas at all (see that
// test's own doc comment).
//
// Dropping a row from the emitted identity table makes
// [identity.SynthesizeTypeIdentity] the type's only remaining resolution
// path, and that function refuses outright the moment it is handed no
// schemas (synthesize.go's own noSchemasRefusal). The golden's analysis is
// exactly that schema-less case, by design, so a row this offline
// approximation calls "reproduced" but that the golden actually exercises
// would not merely render differently - the instance disappears from the
// golden's output entirely, which is a real drop in what the golden's own
// deliberately-cheap, deliberately-offline instrument can see, whatever the
// real behaviour is once real schemas are loaded at plan time.
//
// So a candidate schemaFirstReproduced names is only actually dropped from
// the emitted table when it names no type this function returns - see
// [schemaFirstReproduced]'s own caller in emit.go and ratified.go.
// Measured at the commit this file first landed: of 134 offline-reproducible
// candidates, 97 are exercised somewhere in the golden and stay in the
// table; the remaining 37 carry no fixture anywhere under internal/live or
// live and are the ones this pass actually removes. That gap - the 97 - is
// the concrete, load-bearing evidence that "the schema reproduces the row"
// and "dropping the row is safe today" are two different claims: the first
// is a fact about the provider's own schema, and the second additionally
// depends on schemas being loaded wherever the drop is exercised, which the
// golden's own harness deliberately never does.
func goldenExercisedTypes(path string) (map[string]bool, error) {
	f, err := os.Open(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only, nothing to lose on close failure

	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 2 {
			continue
		}
		if t := addressType(cols[1]); t != "" {
			out[t] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", identityGoldenRel, err)
	}
	return out, nil
}

// addressType reads the resource type off a rendered resource-instance
// address, stripping any number of leading "module.<instance>." segments
// first - a golden address like "module.counted[0].aws_eip.pool[0]" names
// aws_eip, not "module".
func addressType(addr string) string {
	parts := strings.Split(addr, ".")
	for len(parts) >= 2 && parts[0] == "module" {
		parts = parts[2:]
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
