package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"scaffold-engine-go/internal/learn"
)

// learnFlags are the flags `learn` itself owns - it has no dimension/variable flags to resolve,
// since it isn't rendering an existing template.
var learnFlags = []string{
	"output", "provider", "model", "base-url", "response-format", "draft", "force",
	"scaffolding-code", "skip-match", "prompt-addendum",
}

func newLearnCommand() *cobra.Command {
	return &cobra.Command{
		Use: "learn <path> [<path2> ...] --output=<dir> [--provider=anthropic|openai] " +
			"[--model=...] [--base-url=...] [--response-format=tool|json_schema] [--draft=<path|->] " +
			"[--force]",
		Short: "Draft a jig.yaml + templated files from one or more existing example folders",
		Long: "scaffold learn <path> --output=<dir>\n\n" +
			"Scans the example folder at <path>, calls an LLM once to separate invariant\n" +
			"structure from variable names/paths/fields, and writes the result to --output as a\n" +
			"draft jig.yaml plus templated files - a candidate, not yet a live template.\n" +
			"Regenerating afterward goes through the existing, fully deterministic `create` path:\n" +
			"zero further AI calls.\n\n" +
			"A draft's file paths are relative to <path> (the scanned example folder) itself, not\n" +
			"the destination the template will eventually write to once registered - don't bake a\n" +
			"real project's destination prefix into a draft's own paths, or `learn-review`'s\n" +
			"byte-for-byte comparison against <path> will report every file as mismatched. Add any\n" +
			"such nesting as a `target:` override after promoting instead.\n\n" +
			"Given two or more paths (`scaffold learn <path1> <path2> ... --output=<dir>`), all\n" +
			"instances are sent to the model in ONE call, which generalizes across them instead of\n" +
			"just one - a variable's default is always drawn from <path1> specifically, so review it\n" +
			"with `scaffold learn-review <draft-dir> <path1> <path2> ...` afterward, passing every\n" +
			"example path in the same order (learn-review checks <path1> byte-for-byte and every\n" +
			"later path structurally). A single <path> behaves exactly as it always has.\n\n" +
			"Provider is chosen by --provider=anthropic|openai, or auto-detected from whichever of\n" +
			"ANTHROPIC_API_KEY / OPENAI_API_KEY is set. --base-url points the openai provider at\n" +
			"any compatible endpoint (Groq, OpenRouter, a local server, ...).\n\n" +
			"--response-format picks how the openai provider asks for JSON back: \"tool\" (default)\n" +
			"forces a tool call, as always; \"json_schema\" uses response_format instead, for a model\n" +
			"that rejects forced tool_choice while still needing schema-valid JSON. Anthropic only\n" +
			"supports forced tool use and rejects any other value.\n\n" +
			"--prompt-addendum=<path> appends a file's content to the built-in system prompt (never\n" +
			"replacing any of it) for project-specific guidance - extra reserved words, domain naming\n" +
			"conventions. With no flag given, `learn_prompt_addendum:` in ./.scaffold.yaml or\n" +
			"$HOME/.scaffold.yaml is checked next; with neither, `learn` uses its built-in prompt\n" +
			"unmodified, today's exact behavior. Has no effect together with --draft, which never\n" +
			"calls a provider.\n\n" +
			"An AI agent invoking this command is already an LLM - rather than pay for a second,\n" +
			"separately-billed model call, it can do the invariant/variable separation itself and\n" +
			"pass the result straight through with --draft=<path|->, skipping any provider call and\n" +
			"any API key entirely.\n\n" +
			"--output must be empty; pass --force to write into a directory that already holds\n" +
			"something.\n\n" +
			"Before scanning or inferring, `learn` checks whether an already-registered template's\n" +
			"base shape (file names, directory structure) already matches the example folder - on a\n" +
			"confident match it prints the `scaffold create ...` invocation that already covers it\n" +
			"and exits, with no provider call and no draft written, rather than growing a duplicate\n" +
			"template in the registry. Pass --skip-match to always learn a fresh draft regardless.",
		DisableFlagParsing: true,
		RunE:               runLearn,
	}
}

func runLearn(cmd *cobra.Command, rawArgs []string) error {
	args, err := parseArgs(rawArgs)
	if err != nil {
		return err
	}
	if args.help {
		return cmd.Help()
	}

	learnArgs, err := parseLearnArgs(args)
	if err != nil {
		return err
	}
	// Checked before anything is scanned or inferred, not only inside WriteDraft: with a provider
	// call in between, a non-empty --output would otherwise surface after a slow, billed inference
	// whose result is then discarded.
	if err := learn.CheckOutputDir(learnArgs.outputDir, learnArgs.force); err != nil {
		return err
	}

	// Checked before scanning/inferring on EITHER path below (--draft included): the goal isn't
	// only avoiding a billed call, it's avoiding a duplicate template in the registry, and an
	// agent-supplied --draft can grow one just as easily as a provider call can.
	if !learnArgs.skipMatch {
		scaffoldingCodeRoot := resolveScaffoldingCodeRoot(learnArgs.scaffoldingCode)
		match := tryMatchExistingTemplate(scaffoldingCodeRoot, learnArgs.paths[0])
		if match.confident {
			fmt.Fprintln(cmd.OutOrStdout(),
				"An existing template already appears to cover this pattern - skipping learn to "+
					"avoid a duplicate (and, where applicable, a billed model call):")
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", match.invocation)
			fmt.Fprintln(cmd.OutOrStdout(),
				"\nIf this is genuinely a new pattern, rerun with --skip-match.")
			return nil
		}
		if match.uncertain {
			fmt.Fprintf(cmd.OutOrStdout(),
				"Note: an existing template has a similar shape (%.0f%% overlap) but wasn't "+
					"confident enough to skip automatically - check it before promoting a new one:\n",
				match.score*100)
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n\n", match.invocation)
		}
	}

	if learnArgs.draftPath != "" {
		raw, err := readDraftInput(cmd, learnArgs.draftPath)
		if err != nil {
			return err
		}
		return runLearnWithDraftJSON(cmd, learnArgs.outputDir, raw, learnArgs.force)
	}

	client, err := learn.ResolveClient(learnArgs.provider, learnArgs.model, learnArgs.baseURL,
		learnArgs.responseFormat, learnArgs.promptAddendum)
	if err != nil {
		return err
	}
	if len(learnArgs.paths) == 1 {
		return runLearnWithClient(cmd, learnArgs.paths[0], learnArgs.outputDir, client, learnArgs.force)
	}
	return runLearnWithClientMultiExample(cmd, learnArgs.paths, learnArgs.outputDir, client, learnArgs.force)
}

type learnArgs struct {
	paths                                                                           []string
	outputDir, provider, model, baseURL, responseFormat, draftPath, scaffoldingCode string
	// promptAddendum is already-read file content (see resolvePromptAddendum), not a path - it goes
	// straight to learn.ResolveClient to append to the built-in system prompt.
	promptAddendum   string
	force, skipMatch bool
}

// parseLearnArgs validates learn's positional/flag shape, kept separate from provider resolution
// so tests can exercise argument errors without any provider env var set.
func parseLearnArgs(args *parsedArgs) (learnArgs, error) {
	if len(args.positional) < 1 {
		return learnArgs{}, fmt.Errorf(
			"learn takes at least one positional argument: the example folder to learn from " +
				"(two or more learns from all of them at once, generalizing across instances)")
	}
	outputDir, err := args.requireValue("output")
	if err != nil {
		return learnArgs{}, err
	}
	scaffoldingCode, err := args.requireValue("scaffolding-code")
	if err != nil {
		return learnArgs{}, err
	}
	promptAddendumFlag, err := args.requireValue("prompt-addendum")
	if err != nil {
		return learnArgs{}, err
	}
	promptAddendum, err := resolvePromptAddendum(promptAddendumFlag)
	if err != nil {
		return learnArgs{}, err
	}
	la := learnArgs{
		paths:           args.positional,
		outputDir:       outputDir,
		provider:        args.value("provider"),
		model:           args.value("model"),
		baseURL:         args.value("base-url"),
		responseFormat:  args.value("response-format"),
		draftPath:       args.value("draft"),
		scaffoldingCode: scaffoldingCode,
		promptAddendum:  promptAddendum,
		force:           args.value("force") == "true",
		skipMatch:       args.value("skip-match") == "true",
	}

	if err := args.requireAllFlagsConsumed(learnFlags); err != nil {
		return learnArgs{}, err
	}
	if la.outputDir == "" {
		return learnArgs{}, fmt.Errorf(
			"--output is required for learn: a draft must not land somewhere create/list/lint " +
				"would discover it before it has been reviewed")
	}
	if la.draftPath != "" && (la.provider != "" || la.model != "" || la.baseURL != "" ||
		la.responseFormat != "" || la.promptAddendum != "") {
		return learnArgs{}, fmt.Errorf(
			"--draft supplies an already-reasoned draft directly, so --provider/--model/--base-url/" +
				"--response-format/--prompt-addendum (which all pick or shape a provider call) don't " +
				"apply together with it")
	}
	return la, nil
}

// resolvePromptAddendum reads the file resolveLearnPromptAddendumPath names, if any. No path
// resolved anywhere (flag or config) returns "" with no error - learn's built-in prompt then goes
// out unmodified, today's exact behavior. A path that IS named but can't be read is a real
// misconfiguration and fails loudly, rather than silently falling back as if nothing were set.
func resolvePromptAddendum(flagValue string) (string, error) {
	path := resolveLearnPromptAddendumPath(flagValue)
	if path == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading --prompt-addendum file %s: %w", path, err)
	}
	return string(content), nil
}

// readDraftInput reads a draft's JSON from a file, or from stdin when path is "-".
func readDraftInput(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("reading draft JSON from stdin: %w", err)
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading draft JSON from %s: %w", path, err)
	}
	return raw, nil
}

// runLearnWithDraftJSON writes an already-reasoned draft (see --draft) with no provider call at
// all - the caller (typically an AI agent) already did the invariant/variable separation itself.
func runLearnWithDraftJSON(cmd *cobra.Command, outputDir string, raw []byte, force bool) error {
	draft, err := learn.ParseDraft(raw)
	if err != nil {
		return err
	}
	if err := learn.WriteDraft(outputDir, draft, force); err != nil {
		return err
	}
	reportDraft(cmd, outputDir, draft)
	return nil
}

// runLearnWithClient does the actual scan/infer/write for a single example, taking an
// already-resolved Inferer so tests can inject a fake one and never touch the network. Its
// signature is frozen (existing tests call it directly with a bare path), so the multi-example
// case (below) is a separate sibling function rather than a change to this one.
func runLearnWithClient(cmd *cobra.Command, path, outputDir string, client learn.Inferer, force bool) error {
	files, skipped, err := learn.Scan(path)
	if err != nil {
		return err
	}
	reportSkipped(cmd, skipped)
	return inferAndWrite(cmd, files, outputDir, client, force)
}

// runLearnWithClientMultiExample scans several examples of the same pattern in one call - the v3
// capability from issue #19 - stamping each file's ExampleIndex so the model can tell instances
// apart (see prompt.go's promptForFiles/buildUserContent) while emitting normal, unprefixed output
// paths. Additive on top of runLearnWithClient above, which stays untouched for the single-example
// case.
func runLearnWithClientMultiExample(cmd *cobra.Command, paths []string, outputDir string, client learn.Inferer, force bool) error {
	var files []learn.SourceFile
	total := 0
	for i, p := range paths {
		found, skipped, err := learn.Scan(p)
		if err != nil {
			return fmt.Errorf("scanning example %d (%s): %w", i+1, p, err)
		}
		prefix := fmt.Sprintf("example-%d/", i+1)
		for _, f := range found {
			total += len(f.Content)
			files = append(files, learn.SourceFile{Path: f.Path, Content: f.Content, ExampleIndex: i + 1})
		}
		labeled := make([]string, len(skipped))
		for j, s := range skipped {
			labeled[j] = prefix + s
		}
		reportSkipped(cmd, labeled)
	}
	// Each Scan call already bounds its own example against learn.TotalMaxBytes individually, but
	// that check is local to one call - N examples each just under the limit would otherwise
	// concatenate to N times it with nothing left to catch it, silently reopening the exact
	// output-budget concern issue #23 closed.
	if total > learn.TotalMaxBytes {
		return fmt.Errorf("combined examples are %d bytes, over the %d byte total limit `learn` "+
			"sends in one call across ALL examples - use fewer or smaller examples, trimmed to just "+
			"the pattern itself", total, learn.TotalMaxBytes)
	}
	return inferAndWrite(cmd, files, outputDir, client, force)
}

// reportSkipped prints which credential files/symlinks Scan left out, if any - said before the
// call, not after: the point is the user knows what did and didn't leave the machine.
func reportSkipped(cmd *cobra.Command, skipped []string) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"Skipped %d credential file(s)/symlink(s) - not sent to the provider:\n", len(skipped))
	for _, s := range skipped {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", s)
	}
}

// inferAndWrite redacts, calls the model, and writes the resulting draft - shared by the
// single-example and multi-example entry points above.
func inferAndWrite(cmd *cobra.Command, files []learn.SourceFile, outputDir string, client learn.Inferer, force bool) error {
	// Value-level redaction runs on whatever Scan actually read, right before it ships anywhere -
	// a whole-file skip (above) is safe and lossless for the template, but a secret embedded in an
	// otherwise-legitimate file (a password in application.properties, a hardcoded token in a Java
	// constant) needs this instead. Reported the same way skipped files are: what happened, never
	// the actual values.
	files, redactions := learn.RedactSecrets(files)
	if len(redactions) > 0 {
		printRedactionReport(cmd.OutOrStdout(), redactions)
	}

	draft, err := client.Infer(context.Background(), files)
	if err != nil {
		return err
	}

	if err := learn.WriteDraft(outputDir, draft, force); err != nil {
		return err
	}
	reportDraft(cmd, outputDir, draft)
	return nil
}

// printRedactionReport lists every value-level redaction by file and rule name, never the actual
// secret text - the same "know what did and didn't leave the machine" principle the skipped-file
// report above already follows. A redaction's path is labeled "example-N/..." when it came from a
// multi-example call, so two files that legitimately share the same relative path across
// instances don't merge into one misleading report line.
func printRedactionReport(out io.Writer, redactions []learn.Redaction) {
	rulesByPath := map[string][]string{}
	var paths []string
	for _, r := range redactions {
		p := r.Path
		if r.ExampleIndex > 0 {
			p = fmt.Sprintf("example-%d/%s", r.ExampleIndex, r.Path)
		}
		if _, seen := rulesByPath[p]; !seen {
			paths = append(paths, p)
		}
		rulesByPath[p] = append(rulesByPath[p], r.Rule)
	}
	sort.Strings(paths)

	fmt.Fprintf(out, "Redacted %d value(s) in %d file(s) - not sent to the provider as written:\n",
		len(redactions), len(paths))
	for _, p := range paths {
		fmt.Fprintf(out, "  %s (%s)\n", p, strings.Join(rulesByPath[p], ", "))
	}
}

// reportDraft prints the same summary regardless of how the draft was produced (a provider call
// or an agent-supplied --draft).
func reportDraft(cmd *cobra.Command, outputDir string, draft *learn.Draft) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Learned %q into %s (draft - review before use)\n", draft.Name, outputDir)
	if len(draft.Variables) > 0 {
		fmt.Fprintln(out, "\nVariables:")
		for _, v := range draft.Variables {
			fmt.Fprintf(out, "  %-20s default=%q\n", v.Name, v.Default)
		}
	}
	fmt.Fprintf(out, "\n%d file(s) written:\n", len(draft.Files))
	for _, f := range draft.Files {
		fmt.Fprintf(out, "  %s\n", f.Path)
	}
}
