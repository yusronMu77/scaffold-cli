package learn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClient_Infer_ParsesToolCallResponse(t *testing.T) {
	var gotAuth string
	var gotBody openAIRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{
			"choices": [{
				"message": {
					"tool_calls": [{
						"function": {
							"name": "` + toolName + `",
							"arguments": "{\"name\":\"widget\",\"variables\":[{\"name\":\"ClassName\",\"default\":\"Widget\"}],\"files\":[{\"path\":\"{{ .ClassName }}.java\",\"content\":\"class {{ .ClassName }} {}\"}]}"
						}
					}]
				}
			}]
		}`))
	}))
	defer srv.Close()

	c := &openAIClient{apiKey: "test-key", baseURL: srv.URL, model: "test-model", http: srv.Client()}
	draft, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "class Widget {}"}})
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Errorf("expected Authorization header, got %q", gotAuth)
	}
	if gotBody.Model != "test-model" || gotBody.ToolChoice.Function.Name != toolName {
		t.Errorf("unexpected request body: %+v", gotBody)
	}
	if draft.Name != "widget" || len(draft.Variables) != 1 || draft.Variables[0].Name != "ClassName" {
		t.Fatalf("unexpected draft: %+v", draft)
	}
}

func TestOpenAIClient_Infer_SurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"type": "invalid_request_error", "message": "invalid api key"}}`))
	}))
	defer srv.Close()

	c := &openAIClient{apiKey: "bad-key", baseURL: srv.URL, model: "test-model", http: srv.Client()}
	if _, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "x"}}); err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}

func TestOpenAIClient_Infer_ReportsTruncatedDraft(t *testing.T) {
	// finish_reason=length with no usable tool call is the OpenAI-shaped equivalent of Anthropic's
	// max_tokens stop: a 200 whose content is cut off, not a missing-tool-call bug.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"tool_calls": []}, "finish_reason": "length"}]}`))
	}))
	defer srv.Close()

	c := &openAIClient{apiKey: "test-key", baseURL: srv.URL, model: "test-model", http: srv.Client()}
	_, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "x"}})
	if err == nil {
		t.Fatal("expected an error for a response truncated at the output limit")
	}
	if !strings.Contains(err.Error(), "output limit") {
		t.Errorf("expected the error to name the output limit, got %v", err)
	}
}

func TestNewOpenAIClient_TrimsTrailingSlashFromBaseURL(t *testing.T) {
	client := NewOpenAIClient("key", "https://example.test/v1/", "", "")
	oc := client.(*openAIClient)
	if oc.baseURL != "https://example.test/v1" {
		t.Errorf("expected trailing slash trimmed, got %q", oc.baseURL)
	}
}

// response_format:json_schema is the alternative to forced tool_choice for a model that rejects
// forced tool use: no tools/tool_choice at all, and the draft JSON comes back as plain message
// content instead of a tool_calls argument string.
func TestOpenAIClient_Infer_JSONSchemaResponseFormat(t *testing.T) {
	var gotBody openAIRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"name\":\"widget\",\"variables\":[{\"name\":\"ClassName\",\"default\":\"Widget\"}],\"files\":[{\"path\":\"{{ .ClassName }}.java\",\"content\":\"class {{ .ClassName }} {}\"}]}"
				},
				"finish_reason": "stop"
			}]
		}`))
	}))
	defer srv.Close()

	c := &openAIClient{apiKey: "test-key", baseURL: srv.URL, model: "test-model", responseFormat: ResponseFormatJSONSchema, http: srv.Client()}
	draft, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "class Widget {}"}})
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}

	if len(gotBody.Tools) != 0 || gotBody.ToolChoice != nil {
		t.Errorf("expected no tools/tool_choice in json_schema mode, got %+v", gotBody)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_schema" {
		t.Fatalf("expected a json_schema response_format, got %+v", gotBody.ResponseFormat)
	}
	if gotBody.ResponseFormat.JSONSchema == nil || gotBody.ResponseFormat.JSONSchema.Strict {
		t.Errorf("expected a non-strict json_schema (schema has optional fields), got %+v", gotBody.ResponseFormat.JSONSchema)
	}
	if draft.Name != "widget" || len(draft.Variables) != 1 || draft.Variables[0].Name != "ClassName" {
		t.Fatalf("unexpected draft: %+v", draft)
	}
}

func TestOpenAIClient_Infer_JSONSchemaReportsTruncatedDraft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": ""}, "finish_reason": "length"}]}`))
	}))
	defer srv.Close()

	c := &openAIClient{apiKey: "test-key", baseURL: srv.URL, model: "test-model", responseFormat: ResponseFormatJSONSchema, http: srv.Client()}
	_, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "x"}})
	if err == nil {
		t.Fatal("expected an error for a response truncated at the output limit")
	}
	if !strings.Contains(err.Error(), "output limit") {
		t.Errorf("expected the error to name the output limit, got %v", err)
	}
}
