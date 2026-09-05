package learn

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/jig"
)

// candidateKey is the literal YAML key WriteDraft sets to mark a draft as a candidate - see
// jig.Jig's Candidate field.
const candidateKey = "candidate"

// Promote approves a draft `scaffold learn` produced, clearing the `candidate:` flag that keeps
// create/list/lint from using it. It edits jig.yaml as a yaml.Node tree rather than decoding into
// jig.Jig and re-marshalling: issue #18's whole point is that a human may have opened this exact
// file in an editor and edited it first, and a fresh struct-marshal would silently discard any
// comments or formatting they just added. Node surgery keeps everything except the one key
// removed.
func Promote(draftDir string) error {
	jigPath := filepath.Join(draftDir, jig.FileName)

	m, err := jig.Load(jigPath)
	if err != nil {
		return err
	}
	if !m.Candidate {
		return fmt.Errorf("%s is not a candidate - it is either already promoted, or was not "+
			"produced by `scaffold learn` - nothing to promote", jigPath)
	}

	raw, err := os.ReadFile(jigPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", jigPath, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", jigPath, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a YAML mapping at its root - cannot promote", jigPath)
	}

	mapping := doc.Content[0]
	removed := false
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == candidateKey {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			removed = true
			break
		}
	}
	if !removed {
		return fmt.Errorf("%s decodes with candidate: true but has no literal %q key - this "+
			"should not happen", jigPath, candidateKey)
	}

	encoded, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", jigPath, err)
	}
	if err := os.WriteFile(jigPath, encoded, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", jigPath, err)
	}

	if _, err := jig.Load(jigPath); err != nil {
		return fmt.Errorf("promoted but failed self-validation, fix before using it: %w", err)
	}
	return nil
}
