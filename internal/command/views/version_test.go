// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/tfdiags"
	tfversion "github.com/intentius/choudoufu/version"
)

func TestVersionViews(t *testing.T) {
	tests := map[string]struct {
		viewType   arguments.ViewType
		viewCall   func(v Version)
		wantStdout string
		wantStderr string
	}{
		"human printVersion with fips disabled": {
			viewType: arguments.ViewHuman,
			viewCall: func(v Version) {
				v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, map[string]string{
					"registry.opentofu.org/test/test": "0.2.0",
				})
			},
			wantStdout: `OpenTofu v0.1.0-dev
on darwin_arm64
+ provider registry.opentofu.org/test/test v0.2.0
`,
			wantStderr: "",
		},
		"human printVersion with fips enabled": {
			viewType: arguments.ViewHuman,
			viewCall: func(v Version) {
				v.PrintVersion("0.1.0", "dev", "darwin_arm64", true, map[string]string{
					"registry.opentofu.org/test/test": "0.2.0",
				})
			},
			wantStdout: `OpenTofu v0.1.0-dev
on darwin_arm64
running in FIPS 140-3 mode (not yet supported)
+ provider registry.opentofu.org/test/test v0.2.0
`,
			wantStderr: "",
		},
		"human printVersion with unversioned provider": {
			viewType: arguments.ViewHuman,
			viewCall: func(v Version) {
				v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, map[string]string{
					"registry.opentofu.org/test/test": "0.0.0",
				})
			},
			wantStdout: `OpenTofu v0.1.0-dev
on darwin_arm64
+ provider registry.opentofu.org/test/test (unversioned)
`,
			wantStderr: "",
		},
		"json printVersion with fips disabled": {
			viewType: arguments.ViewJSON,
			viewCall: func(v Version) {
				v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, map[string]string{
					"registry.opentofu.org/test/test": "0.2.0",
				})
			},
			wantStdout: `{
  "choudoufu_version": "",
  "terraform_version": "0.1.0-dev",
  "platform": "darwin_arm64",
  "provider_selections": {
    "registry.opentofu.org/test/test": "0.2.0"
  }
}
`,
			wantStderr: "",
		},
		"json printVersion with fips enabled": {
			viewType: arguments.ViewJSON,
			viewCall: func(v Version) {
				v.PrintVersion("0.1.0", "dev", "darwin_arm64", true, map[string]string{
					"registry.opentofu.org/test/test": "0.2.0",
				})
			},
			wantStdout: `{
  "choudoufu_version": "",
  "terraform_version": "0.1.0-dev",
  "platform": "darwin_arm64",
  "fips140": true,
  "provider_selections": {
    "registry.opentofu.org/test/test": "0.2.0"
  }
}
`,
			wantStderr: "",
		},
		"json printVersion with unversioned provider": {
			viewType: arguments.ViewJSON,
			viewCall: func(v Version) {
				v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, map[string]string{
					"registry.opentofu.org/test/test": "0.0.0",
				})
			},
			wantStdout: `{
  "choudoufu_version": "",
  "terraform_version": "0.1.0-dev",
  "platform": "darwin_arm64",
  "provider_selections": {
    "registry.opentofu.org/test/test": "0.0.0"
  }
}
`,
			wantStderr: "",
		},
		// Diagnostics
		"warning": {
			viewType: arguments.ViewHuman,
			viewCall: func(v Version) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Warning, "A warning occurred", "foo bar"),
				}
				v.Diagnostics(diags)
			},
			wantStdout: withNewline("\nWarning: A warning occurred\n\nfoo bar"),
			wantStderr: "",
		},
		"error": {
			viewType: arguments.ViewHuman,
			viewCall: func(v Version) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Error, "An error occurred", "foo bar"),
				}
				v.Diagnostics(diags)
			},
			wantStdout: "",
			wantStderr: withNewline("\nError: An error occurred\n\nfoo bar"),
		},
		"multiple diagnostics": {
			viewType: arguments.ViewHuman,
			viewCall: func(v Version) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Warning, "A warning", "foo bar warning"),
					tfdiags.Sourceless(tfdiags.Error, "An error", "foo bar error"),
				}
				v.Diagnostics(diags)
			},
			wantStdout: withNewline("\nWarning: A warning\n\nfoo bar warning"),
			wantStderr: withNewline("\nError: An error\n\nfoo bar error"),
		},
		"multiple diagnostics in json": {
			viewType: arguments.ViewJSON,
			viewCall: func(v Version) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Warning, "A warning", "foo bar warning"),
					tfdiags.Sourceless(tfdiags.Error, "An error", "foo bar error"),
				}
				v.Diagnostics(diags)
			},
			// The JSON view type does not apply to the diagnostics
			wantStdout: withNewline("\nWarning: A warning\n\nfoo bar warning"),
			wantStderr: withNewline("\nError: An error\n\nfoo bar error"),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			testVersionHuman(t, tc.viewType, tc.viewCall, tc.wantStdout, tc.wantStderr)
		})
	}
}

// TestVersionViews_fork covers release builds of choudoufu, which set
// tfversion.Fork to the release tag via linker flags. The human output names
// the fork release; the JSON output keeps the upstream-shaped
// "terraform_version" and carries the fork tag beside it as
// "choudoufu_version" - the same key [LivePlanDocument] uses, derived the
// same way and, like that document, written unconditionally rather than with
// "omitempty" (#968).
//
// The two JSON cases below are the reason the field carries no "omitempty":
// on a development build the key is PRESENT and empty, so a caller checking a
// version floor before it spawns a verb can tell that build apart from a
// binary old enough not to have the field at all, where the key is absent. An
// "omitempty" field would render both as absent and leave that caller back to
// pattern-matching the human line, which is what #968 was filed to stop.
func TestVersionViews_fork(t *testing.T) {
	prior := tfversion.Fork
	t.Cleanup(func() { tfversion.Fork = prior })

	t.Run("human on a release build", func(t *testing.T) {
		tfversion.Fork = "v0.2.0"
		testVersionHuman(t, arguments.ViewHuman, func(v Version) {
			v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, nil)
		}, "choudoufu v0.2.0 (based on OpenTofu v0.1.0-dev)\non darwin_arm64\n", "")
	})

	t.Run("json on a release build", func(t *testing.T) {
		tfversion.Fork = "v0.2.0"
		testVersionHuman(t, arguments.ViewJSON, func(v Version) {
			v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, nil)
		}, `{
  "choudoufu_version": "v0.2.0",
  "terraform_version": "0.1.0-dev",
  "platform": "darwin_arm64",
  "provider_selections": null
}
`, "")
	})

	t.Run("json on a development build", func(t *testing.T) {
		tfversion.Fork = ""
		testVersionHuman(t, arguments.ViewJSON, func(v Version) {
			v.PrintVersion("0.1.0", "dev", "darwin_arm64", false, nil)
		}, `{
  "choudoufu_version": "",
  "terraform_version": "0.1.0-dev",
  "platform": "darwin_arm64",
  "provider_selections": null
}
`, "")
	})
}

func testVersionHuman(t *testing.T, viewType arguments.ViewType, call func(v Version), wantStdout, wantStderr string) {
	view, done := testView(t)
	v := NewVersion(arguments.ViewOptions{ViewType: viewType}, view)
	call(v)
	output := done(t)
	if diff := cmp.Diff(wantStderr, output.Stderr()); diff != "" {
		t.Errorf("invalid stderr (-want, +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantStdout, output.Stdout()); diff != "" {
		t.Errorf("invalid stdout (-want, +got):\n%s", diff)
	}
}
