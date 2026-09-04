package learn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicClient_Infer_ParsesToolUseResponse(t *testing.T) {
	var gotAuth, gotVersion string
	var gotBody anthropicRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{
			"content": [
				{"type": "tool_use", "name": "` + toolName + `", "input": {
					"name": "widget", "variables": [{"name": "ClassName", "default": "Widget"}],
					"files": [{"path": "{{ .ClassName }}.java", "content": "class {{ .ClassName }} {}"}]
				}}
			]
		}`))
	}))
	defer srv.Close()

	c := &anthropicClient{apiKey: "test-key", model: "test-model", http: srv.Client()}
	// Point the client at the test server instead of the real endpoint.
	orig := anthropicEndpoint
	anthropicEndpoint = srv.URL
	defer func() { anthropicEndpoint = orig }()

	draft, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "class Widget {}"}})
	if err != nil {
		t.Fatalf("Infer returned error: %v", err)
	}

	if gotAuth != "test-key" {
		t.Errorf("expected x-api-key header to be set, got %q", gotAuth)
	}
	if gotVersion != anthropicVersion {
		t.Errorf("expected anthropic-version %q, got %q", anthropicVersion, gotVersion)
	}
	if gotBody.Model != "test-model" || gotBody.ToolChoice.Name != toolName {
		t.Errorf("unexpected request body: %+v", gotBody)
	}
	if draft.Name != "widget" || len(draft.Variables) != 1 || draft.Variables[0].Name != "ClassName" {
		t.Fatalf("unexpected draft: %+v", draft)
	}
}

func TestAnthropicClient_Infer_SurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"type": "authentication_error", "message": "invalid x-api-key"}}`))
	}))
	defer srv.Close()

	orig := anthropicEndpoint
	anthropicEndpoint = srv.URL
	defer func() { anthropicEndpoint = orig }()

	c := &anthropicClient{apiKey: "bad-key", model: "test-model", http: srv.Client()}
	if _, err := c.Infer(context.Background(), []SourceFile{{Path: "a.java", Content: "x"}}); err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}
