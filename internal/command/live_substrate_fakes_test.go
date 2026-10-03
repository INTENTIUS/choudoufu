// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/live/substrate"
)

// GitHub issue #1742 item 8: this package's provider-family fakes, held to
// [substrate.Substrate]. Both embed the interface, so the assertions below
// catch a re-signed method (the fake's own method shadows the embedded one
// and the fake stops satisfying the interface) and the test catches a
// method the interface has dropped, which still compiles.
var (
	_ substrate.Substrate = bindingFamily{}
	_ substrate.Substrate = fakeThirdSubstrate{}
)

// TestFamilyFakesDeclareOnlySubstrateMethods: every method a family fake
// has must be one [substrate.Substrate] names.
func TestFamilyFakesDeclareOnlySubstrateMethods(t *testing.T) {
	iface := reflect.TypeOf((*substrate.Substrate)(nil)).Elem()
	for _, fake := range []substrate.Substrate{bindingFamily{}, fakeThirdSubstrate{}} {
		typ := reflect.TypeOf(fake)
		for i := 0; i < typ.NumMethod(); i++ {
			if name := typ.Method(i).Name; !hasMethod(iface, name) {
				t.Errorf("%T declares %s, which substrate.Substrate does not name: delete it, or the interface lost a method this fake still stubs", fake, name)
			}
		}
	}
}

func hasMethod(iface reflect.Type, name string) bool {
	_, ok := iface.MethodByName(name)
	return ok
}
