package learn

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/jig"
	"scaffold-engine-go/internal/render"
)

// windowsInvalidPathChars are the characters Windows forbids in a filename, beyond the path
// separators. A piped template filter (`{{ .X | kebabcase }}`) is the realistic way one of these
// would end up in a path - the engine's `computed:` mechanism is the correct escape hatch instead
// (see DraftComputed), so this is treated as a draft-authoring bug, not tolerated per-OS behavior.
const windowsInvalidPathChars = `<>:"|?*`

// validDraftPath rejects a file path that no filesystem accepts, that escapes outputDir, or that
// collides with a name the engine reserves. A draft's Files come straight from a provider's tool
// call or an agent-supplied --draft JSON, neither of which is trusted input, so this mirrors the
// same escape check render.checkContained applies to a jig.yaml's own file targets at render time.
func validDraftPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("a draft file has an empty path")
	}
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
	if clean == "." {
		return fmt.Errorf("file path %q names the output directory itself, not a file", p)
	}

	return reservedBaseName("file path", p, path.Base(clean))
}

// reservedBaseName rejects a file name the engine owns. The comparison is case-insensitive: on
// Windows "Jig.yaml" opens the very same file as "jig.yaml", so a case-sensitive check lets a draft
// file overwrite the manifest WriteDraft wrote moments earlier - and when its content happens to
// parse as a jig, the self-validation load succeeds and `learn` reports success over a destroyed
// draft.
func reservedBaseName(what, p, base string) error {
	lower := strings.ToLower(base)
	if lower == strings.ToLower(jig.FileName) {
		return fmt.Errorf("%s %q is reserved: %s is the manifest this draft generates, so a "+
			"template file cannot be called that", what, p, jig.FileName)
	}
	if jig.IsPartial(lower) {
		return fmt.Errorf("%s %q is reserved: %s*%s holds `define` blocks for other templates "+
			"to include and is never emitted as output, so a file that should be generated cannot "+
			"be called that", what, p, jig.PartialPrefix, jig.PartialSuffix)
	}
	return nil
}

// validDraftTarget rejects a `target` that would land somewhere the engine forbids. A target is the
// path a file takes in a generated project and comes from the same untrusted draft as Path, so it
// needs the same structural rules. Unlike Path it is a template rendered at `create` time rather
// than a literal on-disk name, so piped filters and Windows-invalid characters are legitimate in it
// and only the structural rules apply.
func validDraftTarget(srcPath, target string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("file %q has a `target` that is blank; omit it entirely when the file "+
			"should land exactly where its path says", srcPath)
	}
	if filepath.IsAbs(target) || strings.HasPrefix(filepath.ToSlash(target), "/") {
		return fmt.Errorf("file %q has an absolute target %q; a target is relative to the generated "+
			"project", srcPath, target)
	}
	clean := path.Clean(filepath.ToSlash(target))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("file %q has target %q, which escapes the generated project", srcPath, target)
	}
	if clean == "." {
		return fmt.Errorf("file %q has target %q, which names the project directory itself, not a "+
			"file", srcPath, target)
	}
	return reservedBaseName("target", target, path.Base(clean))
}

// validDraftLayout rejects a file set that cannot all exist on one filesystem: two entries writing
// the same path, where the second silently wins while the report still lists both, or one entry's
// path being a directory another entry needs (`a` as a file plus `a/b.txt` under it), which fails
// partway through writing and leaves --output half-populated.
func validDraftLayout(files []DraftFile) error {
	seen := map[string]string{}
	dirs := map[string]bool{}
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Path))
		if prev, dup := seen[clean]; dup {
			return fmt.Errorf("two draft files both write %q (%q and %q) - one would silently "+
				"overwrite the other", clean, prev, f.Path)
		}
		seen[clean] = f.Path
		for d := path.Dir(clean); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for clean, orig := range seen {
		if dirs[clean] {
			return fmt.Errorf("draft file %q is written as a file, but another draft file needs it "+
				"to be a directory", orig)
		}
	}
	return nil
}

// validDraftNames rejects a draft whose variables would be unusable once the template is live: a
// variable whose CLI flag is one of the reserved values-file keys is accepted by jig.Load but
// rejected later by every `scaffold create`, and a computed entry sharing a variable's name
// silently overwrites it at render time.
func validDraftNames(d *Draft) error {
	seen := map[string]bool{}
	for _, v := range d.Variables {
		flag := render.VariableFlagName(jig.Variable{Name: v.Name, Flag: ""})
		if reason, reserved := jig.ReservedValueKeys[flag]; reserved {
			return fmt.Errorf("variable %q maps to the flag --%s, which is reserved (%s) and would "+
				"make every later `scaffold create` fail - name it something more specific, "+
				"e.g. EntityName or ClassName", v.Name, flag, reason)
		}
		seen[v.Name] = true
	}
	for _, c := range d.Computed {
		if seen[c.Name] {
			return fmt.Errorf("computed %q has the same name as a variable, which would silently "+
				"overwrite it at render time - give the computed entry its own name", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

// WriteDraft writes a Draft as a jig.yaml plus its templated files under outputDir, then
// self-validates by loading the jig.yaml back through jig.Load - the same strict decoder `create`
// uses - so a broken draft is never reported as written successfully. A non-empty outputDir is
// refused unless force is set, since a draft landing on top of an existing template would
// overwrite work that cannot be recovered.
func WriteDraft(outputDir string, d *Draft, force bool) error {
	if err := validDraftNames(d); err != nil {
		return err
	}
	for _, f := range d.Files {
		if err := validDraftPath(f.Path); err != nil {
			return err
		}
		if f.Target != "" {
			if err := validDraftTarget(f.Path, f.Target); err != nil {
				return err
			}
		}
	}
	if err := validDraftLayout(d.Files); err != nil {
		return err
	}
	if err := CheckOutputDir(outputDir, force); err != nil {
		return err
	}

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
	// A `files:` entry is only needed for a file whose stored name differs from where it should
	// land - e.g. .gitignore, stored as gitignore.tpl so git doesn't apply it to the templates
	// repo itself.
	for _, f := range d.Files {
		if f.Target != "" {
			m.Files = append(m.Files, jig.FileEntry{Path: f.Path, Target: f.Target})
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

// CheckOutputDir refuses to write a draft into a directory that already holds something, unless
// force is set - the same fail-then-opt-in shape `create` and `init` use. Exported so the command
// can run it before the provider call: a `learn` run that only discovers a non-empty --output after
// inferring has already paid for, and then thrown away, a full model call.
func CheckOutputDir(outputDir string, force bool) error {
	if force {
		return nil
	}
	entries, err := os.ReadDir(outputDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", outputDir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("--output %s is not empty; a draft would overwrite what is already "+
			"there. Point --output at a fresh directory, or pass --force to write anyway", outputDir)
	}
	return nil
}
