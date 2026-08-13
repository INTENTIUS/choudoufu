// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

// admittedTypesMedia is the media cohort's slice of [admittedTypesV0]:
// the types the media ratification batch admitted. Registered into
// admittedTypesV0 by init below; see contributing/LIVE-TABLES.md.
var admittedTypesMedia = map[string]struct{}{
	// ---- Registry-ratified (#40, #44, #65): media services
	// ---- (MediaLive's Multiplex pair, MediaPackage v1 and v2, IVS, and
	// ---- IVSChat). Same tools/row-gen pipeline as the batches above,
	// ---- cross-checked against the AWS provider's documented
	// ---- Argument/Attribute/Import sections at the pinned v6.58.0 tag
	// ---- (live/import-grammar.json), not accepted on the registry's
	// ---- classification alone. Four of row-gen's proposals in this
	// ---- batch's scope are deferred rather than ratified — MediaLive's
	// ---- Channel, Input and InputSecurityGroup and MediaConvert's Queue
	// ---- all map to a CloudFormation Registry entry whose handlers block
	// ---- is create/read/update/delete/list **all false** (three of them
	// ---- "some registry-laggard" MediaLive rows a prior sweep already
	// ---- flagged), the same "supplies no real evidence, whatever its
	// ---- primaryIdentifier claims" standard the streaming batch's
	// ---- aws_appsync_api_cache/aws_appsync_api_key rejections set — see
	// ---- internal/live/identity/table.go's own comment for the four
	// ---- deferred rows' evidence (including a hand-verified correction
	// ---- for the Queue, left unratified anyway for consistency) and
	// ---- live/e2e/estates/media/README.md for the full account. MediaStore
	// ---- (both its Container and the Container's policy) is deliberately
	// ---- absent for a different reason: AWS discontinued the service
	// ---- November 13, 2025 (already past), and the pinned provider's own
	// ---- docs carry a deprecation notice on both types — moved to
	// ---- live/residue.go's DeprecatedServices instead of ratified; see
	// ---- the README's "MediaStore: deprecated-service, not ratified"
	// ---- section for the evidence. MediaTailor and MediaConnect never
	// ---- entered scope at all: the pinned v6.58.0 AWS provider ships no
	// ---- aws_mediatailor_*/aws_mediaconnect_* resources whatsoever,
	// ---- despite both services being fully modeled in the CloudFormation
	// ---- Registry. Cohort estate: live/e2e/estates/media.
	"aws_medialive_multiplex":           {},
	"aws_medialive_multiplex_program":   {},
	"aws_media_package_channel":         {},
	"aws_media_packagev2_channel_group": {},
	"aws_ivs_channel":                   {},
	"aws_ivs_playback_key_pair":         {},
	"aws_ivs_recording_configuration":   {},
	"aws_ivschat_logging_configuration": {},
	"aws_ivschat_room":                  {},
}

func init() { registerCohortAdmitted(admittedTypesMedia) }
