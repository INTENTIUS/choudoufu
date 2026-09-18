// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ── the per-page --query class (issue #1214) ─────────────────────────────
//
// The guard this file powers replaces the one #1042 left behind:
//
//	regexp.MustCompile(`--query\s+['"]length\(ResourceTagMappingList`)
//
// One service, one result key, one JMESPath function - the instance, not
// the class. #1206 was a different service, a different reduction and a
// different symptom, so nothing matched, and the same root cause shipped a
// third time. #1214's class is: a --query that REDUCES a list to a scalar,
// applied to an AWS CLI call botocore says can paginate.
//
// "Reduces" is where the boundary sits, and it is drawn deliberately one
// notch inside "filters or aggregates":
//
//   - An aggregate (`length(Tags)`, `sort(...)`, `max_by(...)`) collapses a
//     page into a value. Past page one the CLI prints one value per page.
//     That is #1042 exactly.
//   - An index or slice (`| [0]`, `Tags[0].Value`, `[-1]`, `[:1]`) picks
//     one element out of a page. Past page one it prints one element per
//     page, and for a filtered projection the non-matching pages print the
//     literal "None". That is #1206 exactly.
//   - A filter WITHOUT a reduction (`Policies[?starts_with(...)].Arn`) is
//     the union of the per-page answers, which is the right answer. It is
//     also the form #1206's fix adopted on purpose: policy_arn_by_prefix in
//     live/e2e/corpus-iam-policy/run.sh filters, flattens the CLI's
//     one-line-per-page text output, and then asserts it got exactly one
//     arn. Flagging that would mean flagging the reference fix, which is
//     the signal that the class boundary belongs at the reduction.
//
// Everything here reads scripts with os.ReadFile, never `git grep` or the
// shell's grep: live/e2e/corpus-mastino-dns/run.sh contains a legitimate
// embedded NUL byte (a comment illustrating a tombstone key format) and
// grep treats the whole file as binary and reports nothing. PR #1157's
// enumeration counted 23 scripts where there were 24 for exactly that
// reason; #1219 fixed it by reading bytes. corpus-mastino-dns carries call
// sites in this class, so this guard would have had the same hole.

// awsGlobalFlagsWithValue and awsGlobalFlagsWithoutValue are the AWS CLI's
// own global options - the only tokens that may legally sit between the
// binary and the service name. Keeping the set closed is what stops the
// extractor from wandering: `awsl ecs register-task-definition --cpu 256`
// does not yield a service called "256", because by the time --cpu appears
// the service and operation have already been read.
var (
	awsGlobalFlagsWithValue = map[string]bool{
		"--endpoint-url": true, "--output": true, "--query": true,
		"--profile": true, "--region": true, "--color": true,
		"--ca-bundle": true, "--cli-read-timeout": true,
		"--cli-connect-timeout": true, "--cli-binary-format": true,
	}
	awsGlobalFlagsWithoutValue = map[string]bool{
		"--debug": true, "--no-verify-ssl": true, "--no-paginate": true,
		"--no-sign-request": true, "--no-cli-pager": true,
		"--cli-auto-prompt": true, "--no-cli-auto-prompt": true,
		"--version": true,
	}
)

// awsInvocationStart matches the command word of an AWS CLI invocation in
// COMMAND position: the binary itself, or one of the per-script wrappers,
// which all spell themselves with an "aws" prefix (awsl, awslg, awsg,
// awsgreen...).
//
// The command-position prefix is load-bearing rather than decorative. A
// bare \baws\b also matches inside
// `--path-prefix "/aws-service-role/$SERVICE/"`, and that phantom
// invocation then owns the rest of the line and swallows the real call's
// --query - which is how live/e2e/corpus-service-linked-roles/run.sh first
// read as three unparseable lines rather than three call sites.
//
// A wrapper whose name does not start with "aws" is invisible here, which
// is exactly what AnalyzeAWSPageQueries' unattributed return value is for.
var awsInvocationStart = regexp.MustCompile("(?:^|[\\s;&|(){}`])(aws[a-zA-Z0-9_]*)[ \t]")

// shellToken matches one shell word, keeping a single- or double-quoted
// run together so a --query's JMESPath (which is full of spaces) is one
// token.
var shellToken = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'[^']*'|\S+`)

// reducingQuery matches the JMESPath shapes that collapse a list.
//
//   - `| [0]`, `| [-1]`, `| [0:1]`, `| [*]` - a pipe into an index or slice
//   - `[0]`, `[-1]`, `[1:3]` anywhere - an index into a projection
//   - a call to one of JMESPath's list-consuming built-ins
//
// `[]` and `[*]` on their own (a flatten or wildcard projection, with no
// index and no pipe) are NOT here: they project rather than reduce, and the
// per-page projections union to the right answer.
var reducingQuery = regexp.MustCompile(
	`\|\s*\[[-0-9:*]*\]` + // | [0], | [-1], | [:1], | [*]
		`|\[-?[0-9]+(?::-?[0-9]*)?\]` + // [0], [-1], [1:3]
		`|\b(?:length|max|min|sum|avg|sort|sort_by|reverse|join|max_by|min_by|to_array|not_null|merge)\s*\(`,
)

// AWSQueryUse is one `aws <service> <operation> ... --query <jmespath>`
// call site found in a shell script.
type AWSQueryUse struct {
	// Line is the 1-based line the invocation STARTS on. A backslash-
	// continued command reports its first line, which is where a reader
	// looking for it will be.
	Line      int
	Service   string // the CLI service token, e.g. "iam" or "s3api"
	Operation string // the CLI operation token, e.g. "list-policy-tags"
	Query     string // the --query argument with its outer quotes removed
	Reducing  bool   // the query collapses a list to a scalar
}

// FindingReason says which way a call site landed in the class.
type FindingReason string

const (
	// ReasonPaginates: the operation is in botocore's paginator data.
	ReasonPaginates FindingReason = "paginates"
	// ReasonUnattributed: the --query reduces, but no `aws <service>
	// <operation>` could be read for it - the invocation arrives through a
	// wrapper, as in corpus-service-linked-roles' `tag_of() { "$@"
	// --query "Tags[?Key=='$key'].Value | [0]" ... }`. It is counted in
	// the class rather than dropped: "I cannot tell" must not read the
	// same as "it is safe", and a shell indirection is not evidence.
	ReasonUnattributed FindingReason = "unattributed"
)

// AWSPageQueryFinding is one call site in #1214's class.
type AWSPageQueryFinding struct {
	AWSQueryUse
	Reason FindingReason
	// APIOperation is the API operation name behind Operation, as botocore
	// spells it ("DescribeDBInstances" for "describe-db-instances"). Empty
	// for a ReasonUnattributed finding, which has no operation.
	APIOperation string
}

func (f AWSPageQueryFinding) String() string {
	if f.Reason == ReasonUnattributed {
		return fmt.Sprintf("line %d: --query %q reduces a list to a scalar on a command this guard cannot resolve to an `aws <service> <operation>` pair (a shell wrapper hides it) - counted in the class until the call it wraps is shown not to paginate (issues #1042, #1206, #1214)",
			f.Line, f.Query)
	}
	return fmt.Sprintf("line %d: `aws %s %s --query %q` reduces a list to a scalar, and %s paginates - the AWS CLI applies --query to each page before merging, so past page one this reads one page at a time (issues #1042, #1206, #1214)",
		f.Line, f.Service, f.Operation, f.Query, f.APIOperation)
}

// logicalLine is a shell command with its backslash continuations joined,
// remembering the line it started on. #1206's own call spans three physical
// lines with the --query on the second, so a line-at-a-time scanner cannot
// see the service and the query together.
type logicalLine struct {
	start int
	text  string
}

// commentOnly matches a whole-line shell comment. These are stripped
// (blanked, so line numbers stay true) BEFORE continuations are joined,
// because the scripts document the broken idioms in prose: the comment
// block above policy_arn_by_prefix quotes #1206's original call verbatim,
// and a scanner that reads comments would report the warning as the
// defect. The cost is a call site hidden behind code plus a trailing `#`,
// which no script in this tree writes.
var commentOnly = regexp.MustCompile(`^\s*#`)

func logicalLines(src string) []logicalLine {
	var out []logicalLine
	var buf strings.Builder
	start := 0
	for i, raw := range strings.Split(src, "\n") {
		line := raw
		if commentOnly.MatchString(line) {
			line = ""
		}
		if buf.Len() == 0 {
			start = i + 1
		}
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, `\`) {
			buf.WriteString(strings.TrimSuffix(trimmed, `\`))
			buf.WriteString(" ")
			continue
		}
		buf.WriteString(line)
		out = append(out, logicalLine{start: start, text: buf.String()})
		buf.Reset()
	}
	if buf.Len() > 0 {
		out = append(out, logicalLine{start: start, text: buf.String()})
	}
	return out
}

var cliWord = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// queryFlag matches a --query and its argument.
var queryFlag = regexp.MustCompile(`--query[ \t]+("(?:[^"\\]|\\.)*"|'[^']*'|\S+)`)

// AnalyzeAWSPageQueries walks a shell script's source and returns every
// AWS CLI invocation that carries a --query, plus the 1-based lines of any
// --query it could not attribute to an `aws <service> <operation>` pair.
//
// That second return value is the point of the design. A sweep that
// silently skips what it does not understand is the shape this backlog
// keeps filing against, so an unattributable --query is reported and the
// guard turns it into a failure.
func AnalyzeAWSPageQueries(src string) (uses []AWSQueryUse, unparsed []int) {
	for _, ll := range logicalLines(src) {
		if !strings.Contains(ll.text, "--query") {
			continue
		}
		starts := awsInvocationStart.FindAllStringSubmatchIndex(ll.text, -1)
		// Each aws-ish command word owns the text from itself up to the
		// next one.
		type span struct {
			from, to int
			use      AWSQueryUse
			ok       bool
		}
		spans := make([]span, 0, len(starts))
		for i, loc := range starts {
			to := len(ll.text)
			if i+1 < len(starts) {
				to = starts[i+1][2]
			}
			s := span{from: loc[2], to: to}
			s.use, s.ok = parseInvocation(ll.text[loc[3]:to])
			spans = append(spans, s)
		}
		for _, q := range queryFlag.FindAllStringSubmatchIndex(ll.text, -1) {
			at := q[0]
			owner := -1
			for i, s := range spans {
				if at >= s.from && at < s.to {
					owner = i
				}
			}
			var use AWSQueryUse
			if owner >= 0 && spans[owner].ok {
				use = spans[owner].use
			} else {
				unparsed = append(unparsed, ll.start)
			}
			use.Line = ll.start
			use.Query = unquote(ll.text[q[2]:q[3]])
			use.Reducing = reducingQuery.MatchString(use.Query)
			uses = append(uses, use)
		}
	}
	return uses, unparsed
}

// parseInvocation reads the tail of a span that begins just after an
// aws-ish command word and pulls out the service and the operation.
func parseInvocation(rest string) (AWSQueryUse, bool) {
	tokens := shellToken.FindAllString(rest, -1)
	var use AWSQueryUse
	for i := 0; i < len(tokens); {
		tok := tokens[i]
		if strings.HasPrefix(tok, "-") {
			switch {
			case strings.Contains(tok, "="):
				i++
			case awsGlobalFlagsWithValue[tok]:
				i += 2
			case awsGlobalFlagsWithoutValue[tok]:
				i++
			default:
				// Not a global option, so the service should already have
				// been read. It has not been, so this is not an invocation
				// shape this guard understands - say so rather than guess.
				return use, false
			}
			continue
		}
		if !cliWord.MatchString(tok) {
			return use, false
		}
		if use.Service == "" {
			use.Service = tok
			i++
			continue
		}
		use.Operation = tok
		break
	}
	if use.Service == "" || use.Operation == "" {
		return use, false
	}
	return use, true
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// PageQueryFindings classifies a script's --query call sites against the
// vendored paginator snapshot.
//
// unknown lists the CLI service tokens the snapshot could not resolve.
// They are returned rather than counted either way, because "I do not know
// this service" must not read the same as "this operation does not page";
// the caller turns them into a refusal that names the token, so a new CLI
// alias stops a human instead of quietly classifying its calls as safe.
//
// A reducing --query that could not be attributed to an invocation at all
// is a finding (ReasonUnattributed), for the same reason pointed the same
// way: a wrapper hiding the call is not evidence the call is safe.
func (p *PaginatingOperations) PageQueryFindings(src string) (findings []AWSPageQueryFinding, unknown []string, unparsedNonReducing []int) {
	uses, _ := AnalyzeAWSPageQueries(src)
	seen := map[string]bool{}
	for _, u := range uses {
		if u.Service == "" {
			// Unattributed. A non-reducing one is out of the class
			// whatever it turns out to be calling, so it is not reported.
			if u.Reducing {
				findings = append(findings, AWSPageQueryFinding{AWSQueryUse: u, Reason: ReasonUnattributed})
			} else {
				unparsedNonReducing = append(unparsedNonReducing, u.Line)
			}
			continue
		}
		if !u.Reducing {
			continue
		}
		api, err := p.APIName(u.Service, u.Operation)
		if err != nil {
			if e, ok := err.(ErrUnknownCLIService); ok && !seen[e.Service] {
				seen[e.Service] = true
				unknown = append(unknown, e.Service)
			}
			continue
		}
		if api != "" {
			findings = append(findings, AWSPageQueryFinding{AWSQueryUse: u, Reason: ReasonPaginates, APIOperation: api})
		}
	}
	sort.Strings(unknown)
	return findings, unknown, unparsedNonReducing
}
