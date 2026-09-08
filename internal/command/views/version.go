// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/tfdiags"
	tfversion "github.com/intentius/choudoufu/version"
)

type Version interface {
	Diagnostics(diags tfdiags.Diagnostics)
	// PrintVersion returns true if the printing has been done successfully and false otherwise.
	PrintVersion(version string, versionPrerelease string, platform string, fipsEnabled bool, providerVersions map[string]string) bool
}

// NewVersion returns an initialized Version implementation for the given ViewType.
// This view behaves differently from the general approach since the JSON format is not meant to follow
// the general JSON format.
// Instead, the view that is returned will always print diagnostics in human format while
// [Version.PrintVersion] will return different results based on the [arguments.ViewOptions#ViewType].
func NewVersion(args arguments.ViewOptions, view *View) Version {
	return &VersionMixed{view: view, json: args.ViewType == arguments.ViewJSON}
}

type VersionMixed struct {
	view *View
	// In the case of this command, we don't use the [JSONView], but we only marshal the result and print it directly
	json bool
}

var _ Version = (*VersionMixed)(nil)

func (v *VersionMixed) Diagnostics(diags tfdiags.Diagnostics) {
	v.view.Diagnostics(diags)
}

func (v *VersionMixed) PrintVersion(version string, versionPrerelease string, platform string, fipsEnabled bool, providerVersions map[string]string) bool {
	if v.json {
		return v.printJsonVersion(version, versionPrerelease, platform, fipsEnabled, providerVersions)
	}
	return v.printHumanVersion(version, versionPrerelease, platform, fipsEnabled, providerVersions)
}

func (v *VersionMixed) printJsonVersion(version string, versionPrerelease string, platform string, fipsEnabled bool, providerVersions map[string]string) bool {
	finalVersion := version
	if versionPrerelease != "" {
		finalVersion = fmt.Sprintf("%s-%s", finalVersion, versionPrerelease)
	}

	output := versionOutput{
		ChoudoufuVersion:   tfversion.Fork,
		Version:            finalVersion,
		Platform:           platform,
		ProviderSelections: providerVersions,
		FIPS140Enabled:     fipsEnabled,
	}
	jsonOutput, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		_, _ = v.view.streams.Eprintln(fmt.Sprintf("\nError marshalling JSON: %s", err))
		return false
	}
	_, _ = v.view.streams.Println(string(jsonOutput))
	return true
}

func (v *VersionMixed) printHumanVersion(version string, versionPrerelease string, platform string, fipsEnabled bool, providerVersions map[string]string) bool {
	formattedVersion := fmt.Sprintf("OpenTofu v%s", version)
	if versionPrerelease != "" {
		formattedVersion = fmt.Sprintf("%s-%s", formattedVersion, versionPrerelease)
	}
	// Release builds of choudoufu set tfversion.Fork to the release tag via
	// linker flags; name that release while keeping the upstream base version
	// visible for tooling that looks for it.
	if tfversion.Fork != "" {
		formattedVersion = fmt.Sprintf("choudoufu %s (based on %s)", tfversion.Fork, formattedVersion)
	}
	_, _ = v.view.streams.Println(formattedVersion)
	_, _ = v.view.streams.Println(fmt.Sprintf("on %s", platform))
	if fipsEnabled {
		_, _ = v.view.streams.Println("running in FIPS 140-3 mode (not yet supported)")
	}

	providerAddrs := slices.Collect(maps.Keys(providerVersions))
	sort.Strings(providerAddrs)
	for _, provAddr := range providerAddrs {
		provVers, ok := providerVersions[provAddr]
		if !ok {
			continue
		}
		if provVers == "0.0.0" {
			_, _ = v.view.streams.Println(fmt.Sprintf("+ provider %s (unversioned)", provAddr))
		} else {
			_, _ = v.view.streams.Println(fmt.Sprintf("+ provider %s v%s", provAddr, provVers))
		}
	}
	return true
}

type versionOutput struct {
	// ChoudoufuVersion is this fork's own release tag (tfversion.Fork),
	// empty on a development build - the same key, from the same source,
	// that [LivePlanDocument] carries, and deliberately written with no
	// "omitempty" for the same reason that document does not use one: a
	// caller checking a version floor before it spawns a verb needs to
	// tell a development build of a current binary (the key is present
	// and empty) from a binary too old to carry the key at all (the key
	// is absent). With "omitempty" both render as absent and that caller
	// is back to pattern-matching the human line, which is what this
	// field was added to stop (#968).
	ChoudoufuVersion string `json:"choudoufu_version"`

	// Version is the upstream OpenTofu base version this build's engine
	// is, under upstream's own key so that tooling written against stock
	// keeps reading it. [LivePlanDocument] calls the same value
	// "upstream_version"; a choudoufu release tag and the OpenTofu
	// release its engine forked from are two different numbers, and this
	// document, like that one, prints both.
	Version            string            `json:"terraform_version"`
	Platform           string            `json:"platform"`
	FIPS140Enabled     bool              `json:"fips140,omitempty"`
	ProviderSelections map[string]string `json:"provider_selections"`
}
