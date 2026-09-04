package learn

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/jig"
)

// windowsInvalidPathChars are the characters Windows forbids in a filename, beyond the path
// separators. A piped template filter (`{{ .X | kebabcase }}`) is the realistic way one of these
// would end up in a path - the engine's `computed:` mechanism is the correct escape hatch instead
// (see DraftComputed), so this is treated as a draft-authoring bug, not tolerated per-OS behavior.
const windowsInvalidPathChars = `<>:"|?*`

// validDraftPath rejects a file path containing a character no real filesystem accepts, or one
// that escapes outputDir, so a bad draft fails clearly at write time instead of surfacing as a
// cryptic OS error - or, worse, silently writing outside outputDir. A draft's Files come straight
// from a provider's tool call or an agent-supplied --draft JSON, neither of which is trusted input,
// so this mirrors the same escape check render.checkContained applies to a jig.yaml's own file
// targets at render time.
func validDraftPath(p string) error {
	if i := strings.IndexAny(p, windowsInvalidPathChars); i >= 0 {
		return fmt.Errorf("file path %q contains %q, which cannot appear in a filename - express "+
			"any non-canonical casing via a `computed` variable and reference it with plain "+
			"\"{{ .Name }}\" syntax instead of a piped filter in the path", p, string(p[i]))
	}
	if filepath.IsAbs(p) || strings.HasPrefix(filepath.ToSlash(p), "/") {
		return fmt.Errorf("file path %q is absolute; draft file paths must be relative to --output", p)
	}
	clean := path.Clean(filepath.ToSlash(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("file path %q escapes the output directory", p)
	}
	return nil
}

// WriteDraft writes a Draft as a jig.yaml plus its templated files under outputDir, then
// self-validates by loading the jig.yaml back through jig.Load - the same strict decoder `create`
// uses - so a broken draft is never reported as written successfully.
func WriteDraft(outputDir string, d *Draft) error {
	m := jig.Jig{
		Name:        d.Name,
		Description: d.Description,
	}
	for _, v := range d.Variables {
		m.Variables = append(m.Variables, jig.Variable{
			Name: v.Name, Prompt: v.Prompt, Default: v.Default, Required: v.Required,
		})
	}
	for _, c := range d.Computed {
		m.Computed = append(m.Computed, jig.Computed{Name: c.Name, Value: c.Value})
	}

	for _, f := range d.Files {
		if err := validDraftPath(f.Path); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", outputDir, err)
	}

	encoded, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("encoding jig.yaml: %w", err)
	}
	jigPath := filepath.Join(outputDir, jig.FileName)
	if err := os.WriteFile(jigPath, encoded, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", jigPath, err)
	}

	for _, f := range d.Files {
		dest := filepath.Join(outputDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}

	if _, err := jig.Load(jigPath); err != nil {
		return fmt.Errorf("draft written but failed self-validation, fix before using it: %w", err)
	}
	return nil
}
