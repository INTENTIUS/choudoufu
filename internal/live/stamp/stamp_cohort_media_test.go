// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

// The media cohort's slice of internal/live/stamp's three pinned test
// collections: which of the cohort's admitted types carry tags, which do
// not, and the caricature schema each one is checked against. Registered by
// init below; see contributing/LIVE-TABLES.md.
var taggableMedia = []string{
	// Registry-ratified media services batch (#40, #44, issue #65).
	// aws_medialive_multiplex_program is this batch's one untaggable
	// type, below. See live/e2e/estates/media/README.md, "Untaggable
	// types".
	"aws_medialive_multiplex",
	"aws_media_package_channel",
	"aws_media_packagev2_channel_group",
	"aws_ivs_channel",
	"aws_ivs_playback_key_pair",
	"aws_ivs_recording_configuration",
	"aws_ivschat_logging_configuration",
	"aws_ivschat_room",
}

var untaggableMedia = []string{
	// Registry-ratified media services batch (#40, #44, issue #65):
	// aws_medialive_multiplex_program's Argument Reference names no
	// tags block at all, and live/registry.json's own
	// AWS::MediaLive::Multiplexprogram tagging.taggable is false. See
	// live/e2e/estates/media/README.md, "Untaggable types".
	"aws_medialive_multiplex_program",
}

func init() {
	registerCohortStamp(taggableMedia, untaggableMedia, func(s testSchemaSource) {
		mergeCohortSchemas(s, testSchemaSource{
			// Registry-ratified media services batch (#40, #44, issue #65).
			// Taggable/untaggable per the real provider's documented Argument
			// Reference for each type: aws_medialive_multiplex_program's is
			// this batch's one untaggable row.
			"aws_medialive_multiplex":           taggedSchema("id", "arn", "name"),
			"aws_medialive_multiplex_program":   untaggedSchema("id", "program_name", "multiplex_id"),
			"aws_media_package_channel":         taggedSchema("id", "arn", "channel_id"),
			"aws_media_packagev2_channel_group": taggedSchema("arn", "name"),
			"aws_ivs_channel":                   taggedSchema("id", "arn"),
			"aws_ivs_playback_key_pair":         taggedSchema("id", "arn", "public_key"),
			"aws_ivs_recording_configuration":   taggedSchema("id", "arn"),
			"aws_ivschat_logging_configuration": taggedSchema("id", "arn"),
			"aws_ivschat_room":                  taggedSchema("id", "arn", "name"),
		})
	})
}
