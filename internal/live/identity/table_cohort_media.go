// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// identityTableMedia is the media cohort's slice of [DefaultTable]:
// the identity rows the media ratification batch added. Registered into
// DefaultTable by init below; see contributing/LIVE-TABLES.md.
var identityTableMedia = buildTable(
	// ---- Sixth registry-ratified batch (#40, #44, #65): media services.
	// ---- See live/e2e/estates/media/README.md for the full account,
	// ---- including the MediaStore deprecated-service call
	// ---- (live/residue.go's DeprecatedServices) and why MediaTailor,
	// ---- MediaConnect, Elastic Transcoder and the ElementalInference
	// ---- family never entered scope at all.
	//
	// Four of row-gen's proposals in this batch's scope are deferred, not
	// ratified — each maps to a CloudFormation Registry entry whose
	// handlers block is create/read/update/delete/list all false
	// (live/registry.json), the same "supplies no real evidence, whatever
	// its primaryIdentifier claims" standard the streaming batch's
	// aws_appsync_api_cache/aws_appsync_api_key rejections set above:
	//   - aws_medialive_channel, aws_medialive_input and
	//     aws_medialive_input_security_group: row-gen proposed all three
	//     server-assigned, and the real provider docs agree with no
	//     correction needed at all (terraform import
	//     aws_medialive_channel.example 1234567,
	//     aws_medialive_input.example 12345678,
	//     aws_medialive_input_security_group.example 123456, each a plain
	//     server-assigned numeric id). The registry's handler-less entry
	//     is the only thing standing between these three and ratification
	//     — a future registry-laggard sweep should expect to admit all
	//     three unchanged from row-gen's own rows.
	//   - aws_media_convert_queue: row-gen proposed server-assigned off
	//     the registry's primaryIdentifier=[Id], but the provider's own
	//     docs import it "using the queue name" (terraform import
	//     aws_media_convert_queue.test tf-test-queue) and its Attribute
	//     Reference states id is "The same as name" — client-named, not
	//     server-assigned. Left unratified anyway, same consistency call
	//     as the three MediaLive rows above.

	serverAssigned("aws_medialive_multiplex",
		"MediaLive assigns the multiplex's own id at create time; name is required and client-chosen but names the multiplex, not its identity — the provider's Attribute Reference exports only arn (a distinct, longer string) alongside it. Confirmed against the documented import command (terraform import aws_medialive_multiplex.example 12345678), which imports by that server-assigned id directly, not by name or arn.",
		"ID", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[ProgramName, MultiplexId],
		// composite, no separator in any schema — row-gen filed this
		// "needs hand separator." The provider's own Import section
		// supplies it directly: "using the id, or a combination of
		// `program_name`/`multiplex_id`" (terraform import
		// aws_medialive_multiplex_program.example example_program/1234567,
		// live/import-grammar.json's evidence_excerpt for this type).
		// Both segments are Required arguments already in configuration;
		// multiplex_id names the parent aws_medialive_multiplex ratified
		// above.
		Type: "aws_medialive_multiplex_program",
		Components: []Component{
			attr("program_name"),
			sep("/"),
			attr("multiplex_id"),
		},
		ImportSyntax: "PROGRAM_NAME/MULTIPLEX_ID",
		// The MultiplexProgram's own id ("ID of the MultiplexProgram" per
		// the provider's Attribute Reference) is a distinct server-assigned
		// value the docs never equate with the program_name/multiplex_id
		// pair the import string is built from — not listed here, the
		// same standard aws_route_table_association holds above.
		IdentityAttrs: nil,
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[Id], in createOnlyProperties,
		// not in readOnlyProperties — row-gen filed this evidence-only
		// (its own argument-name guess, "id", was unconfirmed, GUESSED
		// rather than backed by a schema). The provider's Import section
		// and Attribute Reference resolve it directly: "channel_id -
		// (Required) A unique identifier describing the channel" and
		// "id - The same as `channel_id`" (terraform import
		// aws_media_package_channel.kittens kittens-channel). Client-named,
		// promoted from evidence-only the same way the ec2-networking and
		// storage batches promoted their own GUESSED rows once the real
		// docs confirmed them.
		Type:          "aws_media_package_channel",
		Components:    []Component{attr("channel_id")},
		ImportSyntax:  "CHANNEL_ID",
		IdentityAttrs: []string{"id", "channel_id"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[Arn], read-only — row-gen
		// proposed server-assigned off it. The provider disagrees: its
		// Import section states the resource is imported "using the
		// channel group's name" (terraform import
		// aws_media_packagev2_channel_group.example example), and name is
		// a Required argument already in configuration; the Attribute
		// Reference lists no arn-shaped identity export the import command
		// actually uses. Corrected client-named, the same
		// registry-vs-provider mismatch the storage batch's
		// aws_backup_framework/aws_backup_report_plan and the
		// ec2-networking batch's aws_vpc_dhcp_options_association found.
		Type:          "aws_media_packagev2_channel_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	serverAssigned("aws_ivs_channel",
		"IVS mints the channel's own ARN at create time; name is optional and client-chosen but does not reconstruct the ARN. The pinned v6.58.0 release ships this type a real resource identity schema (Required: arn) and its documented import command (terraform import aws_ivs_channel.example arn:aws:ivs:us-west-2:326937407773:channel/0Y1lcs4U7jk5) matches it exactly.",
		"ARN", "arn"),
	serverAssigned("aws_ivs_playback_key_pair",
		"IVS mints the playback key pair's own ARN at create time from the client-supplied public_key material; no argument reconstructs it. Real v6.58.0 identity schema (Required: arn) and documented import command (terraform import aws_ivs_playback_key_pair.example arn:aws:ivs:us-west-2:326937407773:playback-key/KDJRJNQhiQzA) agree.",
		"ARN", "arn"),
	serverAssigned("aws_ivs_recording_configuration",
		"IVS mints the recording configuration's own ARN at create time; name is optional and client-chosen but does not reconstruct the ARN. Real v6.58.0 identity schema (Required: arn) and documented import command (terraform import aws_ivs_recording_configuration.example arn:aws:ivs:us-west-2:326937407773:recording-configuration/KAk1sHBl2L47) agree.",
		"ARN", "arn"),

	serverAssigned("aws_ivschat_logging_configuration",
		"IVS Chat mints the logging configuration's own ARN at create time; name is optional and client-chosen but does not reconstruct the ARN. Real v6.58.0 identity schema (Required: arn) and documented import command (terraform import aws_ivschat_logging_configuration.example arn:aws:ivschat:us-west-2:326937407773:logging-configuration/MMUQc8wcqZmC) agree.",
		"ARN", "arn"),
	serverAssigned("aws_ivschat_room",
		"IVS Chat mints the room's own ARN at create time; name is optional and client-chosen but does not reconstruct the ARN. Real v6.58.0 identity schema (Required: arn) and documented import command (terraform import aws_ivschat_room.example arn:aws:ivschat:us-west-2:326937407773:room/GoXEXyB4VwHb) agree.",
		"ARN", "arn"),
)

func init() { registerCohortTable(identityTableMedia) }
