package render

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExistingPolicy decides what happens when the target directory already exists.
type ExistingPolicy int

const (
	// FailIfExists is the default: refuse and say so clearly. Deliberately not an interactive
	// prompt - this CLI must stay safe to call from a script.
	FailIfExists ExistingPolicy = iota
	// Overwrite replaces colliding files (--force).
	Overwrite
	// SkipExisting keeps files already on disk and writes only the new ones (--skip-existing).
	SkipExisting
)

// Write commits the rendered tree to <target> transactionally: everything is staged in a sibling
// temp directory (so the final move stays on one filesystem, where rename is atomic) and moved
// into place only once every file has been written, so a failure partway through never leaves a
// half-written tree on disk.
func Write(target string, files []File, policy ExistingPolicy) (written []string, err error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("nothing to write: the resolved template produced no files")
	}

	targetExists := false
	if info, statErr := os.Stat(target); statErr == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("%s already exists and is not a directory", target)
		}
		targetExists = true
	}

	// Scoped to this invocation's own resolved file paths, not "does target exist at all": two
	// templates sharing one <name> but writing to non-overlapping paths (e.g. a pair of IaC
	// templates meant to be created under the same setup name) must not collide with each other's
	// leftover directory just because it happens to already exist (issue #53).
	if targetExists && policy == FailIfExists {
		if colliding := collidingPaths(target, files); len(colliding) > 0 {
			return nil, fmt.Errorf("%d file(s) already exist under %s:\n  %s\nuse --force to "+
				"overwrite them, or --skip-existing to keep the files that are already there",
				len(colliding), target, strings.Join(colliding, "\n  "))
		}
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("creating output parent %s: %w", parent, err)
	}
	staging, err := os.MkdirTemp(parent, ".scaffold-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, f := range files {
		if targetExists && policy == SkipExisting {
			if _, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(f.Path))); statErr == nil {
				continue
			}
		}
		dest := filepath.Join(staging, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", f.Path, err)
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(dest, f.Content, mode); err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.Path, err)
		}
		written = append(written, f.Path)
	}

	// Commit. A fresh target is a single atomic rename; an existing tree is merged file by file.
	if !targetExists {
		if err := os.Rename(staging, target); err != nil {
			return nil, fmt.Errorf("moving generated project into %s: %w", target, err)
		}
		return written, nil
	}
	if err := moveTree(staging, target); err != nil {
		return nil, fmt.Errorf("merging generated files into %s: %w", target, err)
	}
	return written, nil
}

// collidingPaths returns, sorted, every file in files whose resolved destination under target
// already exists on disk - the actual conflict a FailIfExists policy should refuse on, rather than
// treating any pre-existing target directory as a collision regardless of which files it holds.
func collidingPaths(target string, files []File) []string {
	var colliding []string
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(f.Path))); err == nil {
			colliding = append(colliding, f.Path)
		}
	}
	sort.Strings(colliding)
	return colliding
}

// moveTree copies the staged tree over an existing target directory.
func moveTree(staging, target string) error {
	return filepath.Walk(staging, func(abs string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(staging, abs)
		if err != nil || rel == "." {
			return err
		}
		dest := filepath.Join(target, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, info.Mode().Perm())
	})
}
