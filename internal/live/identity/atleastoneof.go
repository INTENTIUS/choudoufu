// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

// emptyAtLeastOneOf reports a [Component.AtLeastOneOf] group every member
// of which was omitted on this instance: no component of the row reading
// one of the group's arguments contributed a segment. It returns the group
// so the refusal can name it. A member that no component of the row reads
// is never counted as omitted, since this row cannot see it.
func emptyAtLeastOneOf(comps []Component, segs []segment) ([]string, bool) {
	emitted := make(map[int]bool, len(segs))
	for _, sg := range segs {
		emitted[sg.comp] = true
	}
	for _, c := range comps {
		if len(c.AtLeastOneOf) == 0 {
			continue
		}
		all := true
		for _, name := range c.AtLeastOneOf {
			if !omittedArg(comps, emitted, name) {
				all = false
				break
			}
		}
		if all {
			return c.AtLeastOneOf, true
		}
	}
	return nil, false
}

// omittedArg is true when some OmitIfAbsent component of the row reads
// name and none of the components that read it contributed a segment.
func omittedArg(comps []Component, emitted map[int]bool, name string) bool {
	found := false
	for i, c := range comps {
		if len(c.Attrs) == 0 || !containsString(c.Attrs, name) {
			continue
		}
		if !c.OmitIfAbsent || emitted[i] {
			return false
		}
		found = true
	}
	return found
}
