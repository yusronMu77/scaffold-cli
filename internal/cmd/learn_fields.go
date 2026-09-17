package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"scaffold-engine-go/internal/javafields"
)

func newLearnFieldsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "learn-fields <JavaFile.java>",
		Short: "Read data.entity.fields straight from an existing Java class, no model call",
		Long: "scaffold learn-fields <JavaFile.java>\n\n" +
			"Extracts field declarations from a Java POJO/@Entity source file with a regex\n" +
			"heuristic and prints them as the data.entity.fields YAML a -f values file already\n" +
			"supplies to the mvc leaf - pipe or redirect the output straight into one. This is a\n" +
			"complement to `scaffold learn`, not a replacement: it needs no API key and never calls\n" +
			"a model, but an irregular class is still better served by `learn` itself.",
		DisableFlagParsing: true,
		RunE:               runLearnFields,
	}
}

func runLearnFields(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}
	if len(args.positional) != 1 {
		return fmt.Errorf("learn-fields takes exactly one positional argument: the Java source file")
	}
	if err := args.requireAllFlagsConsumed(nil); err != nil {
		return err
	}

	path := args.positional[0]
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	fields, err := javafields.ExtractFields(source)
	if err != nil {
		return fmt.Errorf("extracting fields from %s: %w", path, err)
	}

	encoded, err := yaml.Marshal(map[string]any{
		"data": map[string]any{
			"entity": map[string]any{
				"fields": fields,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("encoding fields as yaml: %w", err)
	}
	fmt.Fprint(cmd.OutOrStdout(), string(encoded))
	return nil
}
