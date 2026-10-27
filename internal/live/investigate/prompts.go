// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package investigate

import (
	"fmt"
	"strings"
)

// PromptArgument is one argument of a guided recipe.
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// Prompt is a guided recipe: a fixed list of deterministic investigations
// plus the one step that needs judgment, served as an MCP prompt. The text
// marks each step as deterministic or judgment, so the answer can say which
// was which.
type Prompt struct {
	Name        string
	Description string
	Arguments   []PromptArgument
	Steps       []PromptStep
}

// PromptStep is one step of a recipe.
type PromptStep struct {
	// Deterministic is false for the judgment step.
	Deterministic bool
	// Tool is the tool a deterministic step calls, with its arguments in
	// Args ({{name}} is the prompt's argument).
	Tool string
	Args string
	Text string
}

// Prompts are the guided recipes this server serves.
func Prompts() []Prompt {
	return []Prompt{
		{
			Name:        "safe-to-delete",
			Description: "Is it safe to delete this cloud resource? Owner, dependents and cross-estate readers, then one judgment step.",
			Arguments: []PromptArgument{
				{Name: "cloud_id", Description: "The resource's cloud ID (an AWS ARN).", Required: true},
			},
			Steps: []PromptStep{
				{Deterministic: true, Tool: "owner", Args: `{"cloud_id": "{{cloud_id}}"}`, Text: "Find which estate and address own it. A resource no estate owns is foreign: deleting it is outside choudoufu, and the answer says so."},
				{Deterministic: true, Tool: "investigate", Args: `{"template": "dependents-all", "node": "{{cloud_id}}"}`, Text: "List what depends on it across every source behold has loaded. Unavailable without behold: say so, and do not treat it as \"nothing depends on it\"."},
				{Deterministic: true, Tool: "investigate", Args: `{"template": "readers", "node": "{{cloud_id}}"}`, Text: "List which other estate or root reads it. Same rule when unavailable."},
				{Deterministic: false, Text: "Judgment: weigh the three answers. Name each dependent or reader that would break, and anything unanswered (an unavailable step, a sweep that did not cover the type). Recommend; do not delete. Deleting is a configuration change and a reviewed plan, never a tool here."},
			},
		},
	}
}

// Render is the prompt's text for the arguments given.
func (p Prompt) Render(args map[string]string) (string, error) {
	for _, a := range p.Arguments {
		v := strings.TrimSpace(args[a.Name])
		if a.Required && v == "" {
			return "", fmt.Errorf("prompt %s needs %s", p.Name, a.Name)
		}
		if strings.ContainsAny(v, "\"\\{}\n") {
			return "", fmt.Errorf("%s is one ID, with no quotes, braces or newlines", a.Name)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nFollow these steps in order and report, for each, whether it was deterministic or judgment.\n", p.Description)
	for i, s := range p.Steps {
		kind := "judgment"
		if s.Deterministic {
			kind = "deterministic"
		}
		fmt.Fprintf(&b, "\n%d. [%s] %s", i+1, kind, s.Text)
		if s.Tool != "" {
			call := s.Args
			for _, a := range p.Arguments {
				call = strings.ReplaceAll(call, "{{"+a.Name+"}}", strings.TrimSpace(args[a.Name]))
			}
			fmt.Fprintf(&b, "\n   Call %s with %s.", s.Tool, call)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
