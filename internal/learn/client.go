package learn

import "context"

// Draft is what one `learn` inference call produces: enough to write a jig.yaml plus its
// templated files. Variables/Computed use jig's own field shapes directly so the result is
// guaranteed to match what internal/jig's strict decoder accepts - see WriteDraft.
type Draft struct {
	Name        string
	Description string
	Variables   []DraftVariable
	Computed    []DraftComputed
	Files       []DraftFile
}

// DraftVariable mirrors jig.Variable's fields the model is asked to fill in.
type DraftVariable struct {
	Name     string
	Prompt   string
	Default  string
	Required bool
}

// DraftComputed mirrors jig.Computed: a variable derived from another via a template, e.g. a
// kebab-case form of a PascalCase variable. Needed because a piped filter (`{{ .X | kebabcase }}`)
// cannot appear directly in a physical file *path* - Windows forbids `|` in filenames - so any
// non-canonical casing needed in a path is expressed as a computed variable instead, referenced
// with plain `{{ .Name }}` syntax. Pipe filters remain fine inside file content.
type DraftComputed struct {
	Name  string
	Value string
}

// DraftFile is one file the draft writes under the output directory. Path is the literal on-disk
// path and may itself contain `{{ }}` template syntax, since render.RenderSource templates both
// file contents and path names for every file in a source folder by default.
type DraftFile struct {
	Path    string
	Content string
}

// Inferer separates invariant structure from variable names/paths/fields in one model call.
// Implemented per API shape (Anthropic Messages, OpenAI Chat Completions) so `learn` is not tied
// to one vendor.
type Inferer interface {
	Infer(ctx context.Context, files []SourceFile) (*Draft, error)
}
