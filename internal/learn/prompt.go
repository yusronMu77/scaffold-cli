package learn

import (
	"encoding/json"
	"fmt"
	"strings"
)

// toolName is the function/tool the model is forced to call, regardless of provider shape.
const toolName = "emit_learned_template"

// systemPrompt teaches the model the one convention `learn`'s output depends on: one variable per
// concept, in its most natural canonical form, with every other casing expressed as a Sprig
// template filter already available to every rendered file (kebabcase/camelcase/snakecase, from
// github.com/Masterminds/sprig - the engine's existing template funcmap) rather than a second
// variable. This keeps the generated jig.yaml small and lets `create` regenerate deterministically
// with zero further AI calls.
const systemPrompt = `You are analyzing one example folder (a single already-written instance of
a code pattern - e.g. a controller, a CDK stack) to learn a reusable template from it.

Separate INVARIANT structure (kept exactly as-is) from VARIABLE parts (names, paths, fields that
would differ for another instance of the same pattern).

Rules for variables:
- Declare exactly ONE variable per concept, named in its most natural canonical form as it appears
  in the example (e.g. a Java class name in PascalCase: "Order").
- Every OTHER casing of that same concept found in the example (kebab-case, camelCase, snake_case,
  UPPER_CASE, lower case, plural forms) must be expressed in the templated output as that one
  variable piped through a template filter, not as a second variable. Available filters:
  "kebabcase", "camelcase", "snakecase", "upper", "lower", "title". Example: if the variable is
  Name = "Order" and the example also contains "order-controller" and "orderService", emit
  "{{ .Name | kebabcase }}-controller" and "{{ .Name | camelcase }}Service" - the same syntax Go's
  text/template plus Sprig already supports everywhere else in this engine.
- A variable's "default" must be the literal value found in the example (so the draft, used
  unmodified, reproduces the example exactly).

Rules for files:
- Return every file that should be part of the template, each with:
  - "path": the file's path relative to the template root. This path IS the literal on-disk
    filename/directory structure of the draft, and it may itself contain "{{ .Name }}" wherever
    the example's real path varies by concept (e.g. "src/main/java/.../{{ .Name }}Controller.java").
    Files that don't vary keep their exact original path.
    IMPORTANT: a path may only reference a variable with PLAIN "{{ .Name }}" syntax, never a piped
    filter like "{{ .Name | kebabcase }}" - Windows forbids the "|" character in filenames, so a
    piped expression cannot be part of a physical path. If the path needs a casing other than a
    variable's own canonical form, declare a "computed" entry instead (see below) and reference
    the plain "{{ .ComputedName }}" in the path.
  - "content": the file's content with every occurrence of a variable concept replaced by the
    matching "{{ .Name }}" expression, piped through a filter when the casing differs
    (e.g. "{{ .Name | kebabcase }}") - piped filters are fine in content, only paths forbid them.
    Content that doesn't vary is copied verbatim.
- Do not invent files that were not in the example, and do not omit files that should regenerate
  with the instance.

Rules for computed variables (only needed when a *path*, not content, requires a non-canonical
casing):
- "computed" entries have a "name" (a new identifier, distinct from every variable name) and a
  "value" (a template expression building on a variable, e.g. "{{ .Name | kebabcase }}").
- Reference a computed entry the same way as a variable: plain "{{ .ComputedName }}", in either a
  path or content.

Call the ` + toolName + ` tool exactly once with the complete result. Do not include any other
commentary.`

// inputSchema is the JSON Schema the model's tool call must satisfy, shared verbatim across every
// provider shape - only how it's embedded in the request body differs.
func inputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Short, kebab-case name for the learned template",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "One sentence describing what this template produces",
			},
			"variables": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Canonical-case identifier for this concept, e.g. \"Order\"",
						},
						"prompt": map[string]any{
							"type":        "string",
							"description": "Short help text describing what this variable fills in",
						},
						"default": map[string]any{
							"type":        "string",
							"description": "The literal value found in the example",
						},
						"required": map[string]any{
							"type": "boolean",
						},
					},
					"required": []string{"name", "default"},
				},
			},
			"computed": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "A new identifier, distinct from every variable name",
						},
						"value": map[string]any{
							"type":        "string",
							"description": "A template expression building on a variable, e.g. \"{{ .Name | kebabcase }}\"",
						},
					},
					"required": []string{"name", "value"},
				},
			},
			"files": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Output-relative path, may contain {{ }} template syntax",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "File content with variable occurrences templated",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		"required": []string{"name", "variables", "files"},
	}
}

// buildUserContent renders the example folder as a single prompt block: a file tree followed by
// every file's content, clearly delimited so the model can't confuse a path for content.
func buildUserContent(files []SourceFile) string {
	var b strings.Builder
	b.WriteString("Example folder contents:\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "=== FILE: %s ===\n%s\n\n", f.Path, f.Content)
	}
	return b.String()
}

// rawDraft mirrors inputSchema's shape for unmarshalling a tool call's arguments, regardless of
// which provider produced them.
type rawDraft struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Variables   []struct {
		Name     string `json:"name"`
		Prompt   string `json:"prompt"`
		Default  string `json:"default"`
		Required bool   `json:"required"`
	} `json:"variables"`
	Computed []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"computed"`
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

// parseDraft decodes one provider's tool-call arguments (already-parsed JSON object bytes) into a
// Draft.
func parseDraft(raw []byte) (*Draft, error) {
	var rd rawDraft
	if err := json.Unmarshal(raw, &rd); err != nil {
		return nil, fmt.Errorf("model returned a malformed draft: %w", err)
	}
	if rd.Name == "" {
		return nil, fmt.Errorf("model returned a draft with no name")
	}
	if len(rd.Files) == 0 {
		return nil, fmt.Errorf("model returned a draft with no files")
	}

	d := &Draft{Name: rd.Name, Description: rd.Description}
	for _, v := range rd.Variables {
		d.Variables = append(d.Variables, DraftVariable{
			Name: v.Name, Prompt: v.Prompt, Default: v.Default, Required: v.Required,
		})
	}
	for _, c := range rd.Computed {
		d.Computed = append(d.Computed, DraftComputed{Name: c.Name, Value: c.Value})
	}
	for _, f := range rd.Files {
		d.Files = append(d.Files, DraftFile{Path: f.Path, Content: f.Content})
	}
	return d, nil
}
