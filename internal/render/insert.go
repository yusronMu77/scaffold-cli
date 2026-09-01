package render

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ApplyInserts splices every Insert into the file it names, which must already exist under
// target - `create` only ever calls this after the normal file tree has been committed, so a
// file this same run just wrote or merged is a legal splice target too. Order matters: inserts
// run in source order, and each sees the file as a previous insert into the same path left it, so
// a later insert may anchor off text an earlier one just added. Applying is skipped, and the path
// reported under skipped, when the file already contains the insert's content verbatim - so
// running the same `create` twice never duplicates the spliced block.
func ApplyInserts(target string, inserts []Insert) (applied, skipped []string, err error) {
	cache := map[string][]byte{}

	for _, ins := range inserts {
		content, ok := cache[ins.Path]
		if !ok {
			abs := filepath.Join(target, filepath.FromSlash(ins.Path))
			content, err = os.ReadFile(abs)
			if err != nil {
				return applied, skipped, fmt.Errorf(
					"%s declares insert_%s against %s, but it does not exist - inserting only "+
						"works against a file that is already there (e.g. re-running create "+
						"against an already-generated project), not one this run just created: %w",
					ins.Source, direction(ins.After), ins.Path, err)
			}
		}

		if bytes.Contains(content, ins.Content) {
			skipped = append(skipped, ins.Path)
			continue
		}

		updated, err := spliceAtAnchor(content, ins)
		if err != nil {
			return applied, skipped, fmt.Errorf("%s: %s: %w", ins.Source, ins.Path, err)
		}
		cache[ins.Path] = updated
		applied = append(applied, ins.Path)
	}

	for relPath, content := range cache {
		abs := filepath.Join(target, filepath.FromSlash(relPath))
		if err := writeFileInPlace(abs, content); err != nil {
			return applied, skipped, err
		}
	}
	return applied, skipped, nil
}

func direction(after bool) string {
	if after {
		return "after"
	}
	return "before"
}

// spliceAtAnchor finds the single line matching ins.Anchor and inserts ins.Content's lines
// immediately after or before it, per ins.After. CRLF line endings are normalized to LF for the
// search-and-splice and restored on the way out, so the file's own convention survives untouched.
func spliceAtAnchor(content []byte, ins Insert) ([]byte, error) {
	crlf := bytes.Contains(content, []byte("\r\n"))
	body := string(content)
	if crlf {
		body = strings.ReplaceAll(body, "\r\n", "\n")
	}
	lines := strings.Split(body, "\n")

	var re *regexp.Regexp
	if ins.Regex {
		var err error
		re, err = regexp.Compile(ins.Anchor)
		if err != nil {
			return nil, fmt.Errorf("anchor %q is not a valid regexp: %w", ins.Anchor, err)
		}
	}

	at := -1
	matches := 0
	for i, line := range lines {
		hit := strings.Contains(line, ins.Anchor)
		if ins.Regex {
			hit = re.MatchString(line)
		}
		if hit {
			matches++
			at = i
		}
	}
	switch {
	case matches == 0:
		return nil, fmt.Errorf("anchor %q not found", ins.Anchor)
	case matches > 1:
		return nil, fmt.Errorf("anchor %q matches %d lines, but insertion needs exactly one",
			ins.Anchor, matches)
	}

	insertAt := at + 1
	if !ins.After {
		insertAt = at
	}
	snippet := strings.Split(strings.TrimRight(string(ins.Content), "\n"), "\n")

	out := make([]string, 0, len(lines)+len(snippet))
	out = append(out, lines[:insertAt]...)
	out = append(out, snippet...)
	out = append(out, lines[insertAt:]...)

	result := strings.Join(out, "\n")
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return []byte(result), nil
}

// writeFileInPlace commits an updated file atomically - staged as a temp file in the same
// directory (so the final rename stays on one filesystem), preserving the original's permissions.
func writeFileInPlace(abs string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(abs), ".scaffold-insert-*")
	if err != nil {
		return fmt.Errorf("staging update to %s: %w", abs, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing update to %s: %w", abs, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing update to %s: %w", abs, err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", abs, err)
	}
	if err := os.Rename(tmp.Name(), abs); err != nil {
		return fmt.Errorf("committing update to %s: %w", abs, err)
	}
	return nil
}
