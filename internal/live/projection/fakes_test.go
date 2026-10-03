// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/live/substrate"
)

// GitHub issue #1742 item 8: the fakes this package's tests hand the code
// under test, held to the interfaces they stand in for. A fake reached
// through an interface value drifts silently when the interface changes:
// a renamed or re-signed method leaves the fake's old one behind as a
// method nothing calls, and the test goes on passing against the
// embedded zero value instead of the stub it was written with.
var (
	_ MarkerWriter = (*fakeObjectWriter)(nil)
	_ MarkerWriter = failingWriter{}
	_ MarkerTagger = (*fakeTagger)(nil)

	_ substrate.Substrate = bindingFamily{}
	_ substrate.Substrate = widgetFamily{}
	_ substrate.Substrate = gadgetFamily{}
	_ substrate.Substrate = silentFamily{}
	_ substrate.Substrate = bareFamily{}
	_ substrate.Substrate = annotatedBareFamily{}
	_ substrate.Substrate = alwaysWithholdLabels{}
)

// TestFamilyFakesDeclareOnlySubstrateMethods is the half of the above a
// compile-time assertion cannot do. Each family fake embeds
// [substrate.Substrate], so it satisfies the interface whatever it
// declares, and a method the interface has dropped (OwnershipSurfaceOf
// was one, left on four fakes) still compiles. Every method a fake has
// must be one the interface names.
func TestFamilyFakesDeclareOnlySubstrateMethods(t *testing.T) {
	for _, fake := range []substrate.Substrate{
		bindingFamily{}, widgetFamily{}, gadgetFamily{}, silentFamily{},
		bareFamily{}, annotatedBareFamily{}, alwaysWithholdLabels{},
	} {
		for _, stray := range strayMethods(reflect.TypeOf(fake)) {
			t.Errorf("%T declares %s, which substrate.Substrate does not name: delete it, or the interface lost a method this fake still stubs", fake, stray)
		}
	}
}

// strayMethods is every method in typ's method set that
// [substrate.Substrate] does not declare.
func strayMethods(typ reflect.Type) []string {
	iface := reflect.TypeOf((*substrate.Substrate)(nil)).Elem()
	var stray []string
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if _, ok := iface.MethodByName(name); !ok {
			stray = append(stray, name)
		}
	}
	return stray
}
