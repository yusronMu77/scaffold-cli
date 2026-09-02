package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/jig"
)

// starterRootJig is what `init` writes: a valid root registry shape, deliberately empty of real
// scaffolds so `list`/`create` correctly refuse to do anything until the user registers their
// first one (jig.LoadRoot's "registers no scaffolds" error) - "start empty and grow" per the
// issue this closes, not a fake non-empty registry.
const starterRootJig = `name: "Scaffolding Code Root"
description: "Registry of supported scaffolds"

# Register each scaffold this project owns below - an unregistered folder is invisible to
# ` + "`scaffold list`" + ` and can't be generated. Full format guide:
# https://github.com/yusronMu77/scaffold-templates#3-every-folder-needs-to-be-registered
#
# values:
#   - name: "spring-boot"
#     description: "Spring Boot (Java/Maven)"
values: []
`

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init [path] [--force]",
		Short: "Bootstrap a minimal, valid scaffolding-code root at path",
		Long: "scaffold init [path]\n\n" +
			"Writes a starter " + jig.FileName + " - a valid root registry with no scaffolds yet - " +
			"into path (default \".\"), creating the directory if needed. This is a pure local file " +
			"write: no network calls, and no git init.\n\n" +
			"    --force   overwrite an existing " + jig.FileName + " at path",
		DisableFlagParsing: true,
		RunE:               runInit,
	}
}

func runInit(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}
	if len(args.positional) > 1 {
		return fmt.Errorf("usage: scaffold init [path]\n"+
			"got %d positional arguments, at most 1 is accepted", len(args.positional))
	}
	force := args.value("force") == "true"
	args.markConsumed("force")
	if err := args.requireAllFlagsConsumed([]string{"force"}); err != nil {
		return err
	}

	path := "."
	if len(args.positional) == 1 {
		path = args.positional[0]
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}

	jigPath := filepath.Join(path, jig.FileName)
	if !force {
		if _, err := os.Stat(jigPath); err == nil {
			return fmt.Errorf("%s already exists - pass --force to overwrite it", jigPath)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking %s: %w", jigPath, err)
		}
	}

	if err := os.WriteFile(jigPath, []byte(starterRootJig), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", jigPath, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Wrote %s\n\n"+
		"Next: edit it to register your first scaffold, then `scaffold list --scaffolding-code=%s`.\n"+
		"Format guide: https://github.com/yusronMu77/scaffold-templates#3-every-folder-needs-to-be-registered\n",
		jigPath, path)
	return nil
}
