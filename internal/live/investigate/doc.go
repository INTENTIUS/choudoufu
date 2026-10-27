// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package investigate is "choudoufu investigate" and the choudoufu MCP
// server (GitHub issue #2280, ruled 2026-10-26).
//
// An investigation is a named, read-only question. There are two kinds:
//
//   - native lookups that choudoufu answers itself and that need no graph:
//     the owner of a cloud ID, a record's past versions, the live resources
//     nobody declares, and which roots a git range touches ([Natives]; the
//     command package supplies each one's function);
//   - chant's investigation templates (INTENTIUS/chant#4531), read as data
//     from an installed chant ([LoadCatalog]). A path template runs through
//     chant search; a Cypher template goes to behold's graph through behold
//     MCP's ask, and is unavailable without behold.
//
// The MCP server ([NewServer]) serves both, fronts behold's MCP (ask, show,
// shot, capabilities under "behold:") when a behold binary is present, and
// passes chant's Ops through under "chant:" when a chant project and binary
// are, with op-approve left out unless configured. The only tool that
// changes anything is chant's op-run, through chant's gates. The
// investigation path starts no child but chant search and behold mcp
// ([investigationChild]), and imports nothing that plans or applies.
//
// No answer carries a record's payload.
package investigate
