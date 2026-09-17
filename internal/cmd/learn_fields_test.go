package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestLearnFields_RegisteredOnRootCommand(t *testing.T) {
	root := &cobra.Command{Use: "scaffold"}
	root.AddCommand(newLearnFieldsCommand())
	found, _, err := root.Find([]string{"learn-fields"})
	if err != nil || found.Name() != "learn-fields" {
		t.Fatalf("expected `learn-fields` to be a registered subcommand, got %v, err=%v", found, err)
	}
}

func TestLearnFields_RequiresExactlyOnePositional(t *testing.T) {
	if _, err := run(t, newLearnFieldsCommand); err == nil {
		t.Fatal("expected an error with no positional argument")
	}
	if _, err := run(t, newLearnFieldsCommand, "one.java", "two.java"); err == nil {
		t.Fatal("expected an error with two positional arguments")
	}
}

func TestLearnFields_MissingFileIsReported(t *testing.T) {
	_, err := run(t, newLearnFieldsCommand, filepath.Join(t.TempDir(), "nope.java"))
	if err == nil || !strings.Contains(err.Error(), "nope.java") {
		t.Fatalf("expected the missing file to be named, got: %v", err)
	}
}

func TestLearnFields_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Product.java", `
@Entity
public class Product {
    @NotBlank
    private String name;

    @Min(0)
    private int price;

    private String description;
}
`)

	out, err := run(t, newLearnFieldsCommand, filepath.Join(dir, "Product.java"))
	if err != nil {
		t.Fatalf("learn-fields returned error: %v\n%s", err, out)
	}

	for _, want := range []string{
		"data:", "entity:", "fields:",
		"name: name", "type: String", "@NotBlank",
		"name: price", "type: int", "@Min(0)",
		"name: description",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}
