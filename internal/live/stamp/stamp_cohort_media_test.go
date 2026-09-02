// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The media cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
//
// Two of the batch's original nine types left the admission on measured
// un-round-trippability (#124's acceptance run, 2026-08-14; rulings in
// tools/row-gen/rejected.json): aws_ivs_playback_key_pair (public_key is
// Required, ForceNew and write-only) and aws_medialive_multiplex_program
// (the pinned provider's import leaves multiplex_id unset) - their pins
// left with them, the same way #125 removed aws_iam_access_key's.
var taggableMedia = []string{
	// Registry-ratified media services batch (#40, #44, issue #65).
	// See live/e2e/estates/media/README.md, "Untaggable types".
	"aws_medialive_multiplex",
	"aws_media_package_channel",
	"aws_media_packagev2_channel_group",
	"aws_ivs_channel",
	"aws_ivs_recording_configuration",
	"aws_ivschat_logging_configuration",
	"aws_ivschat_room",
}

var untaggableMedia = []string{}

func init() {
	registerCohortStamp(taggableMedia, untaggableMedia, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified media services batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type.
			"aws_medialive_multiplex":           taggedSchema("id", "arn", "name"),
			"aws_media_package_channel":         taggedSchema("id", "arn", "channel_id"),
			"aws_media_packagev2_channel_group": taggedSchema("arn", "name"),
			"aws_ivs_channel":                   taggedSchema("id", "arn"),
			"aws_ivs_recording_configuration":   taggedSchema("id", "arn"),
			"aws_ivschat_logging_configuration": taggedSchema("id", "arn"),
			"aws_ivschat_room":                  taggedSchema("id", "arn", "name"),
		})
	})
}
