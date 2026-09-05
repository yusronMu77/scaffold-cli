// Package learn implements `scaffold learn`: scanning one example folder, calling an LLM once to
// separate invariant structure from variable names/paths/fields, and writing the result as a draft
// jig.yaml + templated files that the existing `create` path can render deterministically.
package learn

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourceFile is one file read from the example folder, relative path plus content.
type SourceFile struct {
	Path    string
	Content string
}

// perFileMaxBytes and totalMaxBytes cap what gets sent to the model in one call - large enough for
// a real example (a controller, a CDK stack), small enough that a mistakenly huge folder fails
// fast instead of producing a silently expensive request.
const (
	perFileMaxBytes = 256 * 1024
	totalMaxBytes   = 1_000_000
)

// credentialFileNames and credentialFileExts are files whose whole purpose is holding secrets.
// `learn` ships everything it scans to a third-party provider, so these are skipped even when they
// sit inside the pattern being learned - a template can be re-authored, a leaked key cannot be
// un-sent. Deliberately limited to unambiguous credential stores: ordinary config files
// (application.properties and friends) are part of the template and stay in.
var (
	credentialFileNames = map[string]bool{
		"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
		"credentials": true, "credentials.json": true, "service-account.json": true,
		"secrets.json": true, "secrets.yaml": true, "secrets.yml": true,
		"kubeconfig": true, ".netrc": true, "htpasswd": true,
		".env": true, ".envrc": true, ".pgpass": true, ".npmrc": true,
		".git-credentials": true, ".dockercfg": true, ".pypirc": true,
	}
	credentialFileExts = map[string]bool{
		".pem": true, ".key": true, ".p12": true, ".pfx": true,
		".jks": true, ".keystore": true, ".ppk": true,
	}
)

// isCredentialFile reports whether name is an unambiguous credential store rather than part of the
// pattern being learned. Names match with or without a leading dot, since the dotted and undotted
// spellings of the same store are equally common (`htpasswd` and `.htpasswd`) and dot-files are now
// scanned. `.env` and friends are matched by prefix too, so `.env.local` and `.env.production` are
// covered without listing every suffix anyone invents.
func isCredentialFile(name string) bool {
	lower := strings.ToLower(name)
	if credentialFileNames[lower] || credentialFileNames[strings.TrimPrefix(lower, ".")] {
		return true
	}
	if credentialFileExts[strings.ToLower(filepath.Ext(lower))] {
		return true
	}
	return strings.HasPrefix(lower, ".env.")
}

// Scan walks dir and returns every text file in it, plus the relative paths it deliberately left
// out (credential stores and symlinks - both reported to the user, since the point is knowing what
// did and didn't leave the machine). Dot-directories (.git and similar) and anything that sniffs
// as binary are dropped silently; dot-files are kept, since .gitignore/.dockerignore are part of
// the pattern being learned. Files come back sorted so the prompt built from them is deterministic
// run to run.
func Scan(dir string) ([]SourceFile, []string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading example folder %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", dir)
	}

	var files []SourceFile
	var skipped []string
	total := 0

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			// Every dot-directory is skipped, `.github` included: `learn` targets the code pattern,
			// not the repository around it. Decided deliberately (2026-09-05) rather than
			// allowlisted, since widening what leaves the machine is what let credential files
			// through once already.
			if rel != "." && len(d.Name()) > 0 && d.Name()[0] == '.' {
				return filepath.SkipDir
			}
			return nil
		}
		if isCredentialFile(d.Name()) {
			skipped = append(skipped, filepath.ToSlash(rel))
			return nil
		}
		// WalkDir doesn't follow symlinks but os.ReadFile does, so a link named like ordinary
		// config would ship its target - possibly a credential outside the folder entirely - past
		// the deny-list above, which only ever sees the link's own name.
		if d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, filepath.ToSlash(rel)+" (symlink)")
			return nil
		}

		// Size is checked from the directory entry first so an oversized file fails fast instead of
		// being pulled into memory in full only to be rejected on the next line.
		if info, infoErr := d.Info(); infoErr == nil && info.Size() > perFileMaxBytes {
			return fmt.Errorf("%s is %d bytes, over the %d byte per-file limit for `learn` - "+
				"trim the example folder to just the pattern itself", rel, info.Size(), perFileMaxBytes)
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", rel, readErr)
		}
		if len(data) > perFileMaxBytes {
			return fmt.Errorf("%s is %d bytes, over the %d byte per-file limit for `learn` - "+
				"trim the example folder to just the pattern itself", rel, len(data), perFileMaxBytes)
		}
		if looksBinary(data) {
			return nil
		}
		total += len(data)
		if total > totalMaxBytes {
			return fmt.Errorf("example folder is over the %d byte total limit for `learn` - "+
				"trim it to just the pattern itself", totalMaxBytes)
		}
		files = append(files, SourceFile{Path: filepath.ToSlash(rel), Content: string(data)})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(skipped)
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no text files found under %s to learn from", dir)
	}
	return files, skipped, nil
}

// looksBinary sniffs a file the same way `file`/git do: a NUL byte in the first chunk means
// binary. Good enough to skip compiled artefacts and images without a content-type library.
func looksBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
