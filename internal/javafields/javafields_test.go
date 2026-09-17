package javafields

import (
	"reflect"
	"testing"
)

// A realistic multi-field @Entity: multiple annotations on one field, a field with none, a
// generic type, a final field, and an edge case where an annotation trails a prior block's closing
// brace rather than sitting on its own line above the next field.
const sampleEntity = `package com.company.order;

@Entity
public class Product {

    @NotBlank
    @Size(max = 200, message = "name must be at most 200 characters")
    private String name;

    @Min(0)
    private int price;

    private String description;

    private List<String> tags;

    static { price = 0; } @Deprecated
    private String legacyField;

    private final String sku = "unset";
}
`

func TestExtractFields_MultiFieldEntity(t *testing.T) {
	fields, err := ExtractFields([]byte(sampleEntity))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}

	want := []Field{
		{Name: "name", Type: "String", Constraints: []string{
			"@NotBlank", `@Size(max = 200, message = "name must be at most 200 characters")`,
		}},
		{Name: "price", Type: "int", Constraints: []string{"@Min(0)"}},
		{Name: "description", Type: "String"},
		{Name: "tags", Type: "List<String>"},
		{Name: "legacyField", Type: "String"},
		{Name: "sku", Type: "String"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("unexpected fields:\ngot:  %+v\nwant: %+v", fields, want)
	}
}

func TestExtractFields_FieldWithNoAnnotationsHasEmptyConstraints(t *testing.T) {
	fields, err := ExtractFields([]byte("private String description;\n"))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}
	if len(fields) != 1 || len(fields[0].Constraints) != 0 {
		t.Fatalf("expected one field with no constraints, got: %+v", fields)
	}
}

func TestExtractFields_GenericType(t *testing.T) {
	fields, err := ExtractFields([]byte("private Map<String, List<Integer>> matrix;\n"))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}
	if len(fields) != 1 || fields[0].Type != "Map<String, List<Integer>>" || fields[0].Name != "matrix" {
		t.Fatalf("unexpected generic field extraction: %+v", fields)
	}
}

func TestExtractFields_FinalFieldWithInitializer(t *testing.T) {
	fields, err := ExtractFields([]byte(`private final List<String> tags = new ArrayList<>();` + "\n"))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}
	if len(fields) != 1 || fields[0].Type != "List<String>" || fields[0].Name != "tags" {
		t.Fatalf("unexpected final-field extraction: %+v", fields)
	}
}

// A line combining a block's closing brace with a trailing annotation must not be mistaken for a
// pure annotation line - the annotation must not attach to the following field.
func TestExtractFields_AnnotationOnClosingBraceLineDoesNotAttach(t *testing.T) {
	src := "static { x = 0; } @Deprecated\nprivate String legacyField;\n"
	fields, err := ExtractFields([]byte(src))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("expected exactly one field, got: %+v", fields)
	}
	if len(fields[0].Constraints) != 0 {
		t.Fatalf("expected no constraints to falsely attach, got: %+v", fields[0].Constraints)
	}
}

// A blank line between an annotation and its field must not stop the scan; a blank line between
// two DIFFERENT fields' declarations must not let annotations from a field two above bleed into
// the field directly below.
func TestExtractFields_BlankLinesAroundAnnotations(t *testing.T) {
	src := "@Min(0)\n\nprivate int price;\n\nprivate String description;\n"
	fields, err := ExtractFields([]byte(src))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected two fields, got: %+v", fields)
	}
	if !reflect.DeepEqual(fields[0].Constraints, []string{"@Min(0)"}) {
		t.Errorf("expected price to carry @Min(0) across the blank line, got: %+v", fields[0].Constraints)
	}
	if len(fields[1].Constraints) != 0 {
		t.Errorf("expected description to carry no constraints, got: %+v", fields[1].Constraints)
	}
}

func TestExtractFields_NoFieldsInSourceReturnsEmpty(t *testing.T) {
	fields, err := ExtractFields([]byte("public class Empty {\n}\n"))
	if err != nil {
		t.Fatalf("ExtractFields returned error: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("expected no fields, got: %+v", fields)
	}
}
