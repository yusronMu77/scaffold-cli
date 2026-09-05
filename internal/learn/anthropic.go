package learn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DefaultAnthropicModel is used when --model is not given for the Anthropic provider.
const DefaultAnthropicModel = "claude-sonnet-5"

// anthropicEndpoint is a var, not a const, so tests can point it at an httptest server.
var anthropicEndpoint = "https://api.anthropic.com/v1/messages"

const (
	// anthropicVersion is the API version header Anthropic has kept stable for a long time; worth
	// reconfirming against a live response if requests start failing after a provider-side change.
	anthropicVersion = "2023-06-01"
	// anthropicMaxTokens has to cover a draft that echoes every scanned file back as templated
	// content, so it is sized well above a single file. Kept at a non-streaming-safe value: much
	// larger ceilings need a streaming request to avoid HTTP timeouts.
	anthropicMaxTokens = 16384
)

// anthropicClient implements Inferer against Anthropic's Messages API directly (no SDK): a forced
// tool_choice makes the response's tool_use.input already-parsed JSON matching inputSchema().
type anthropicClient struct {
	apiKey string
	model  string
	http   *http.Client
}

// NewAnthropicClient builds an Inferer for Anthropic's Messages API.
func NewAnthropicClient(apiKey, model string) Inferer {
	if model == "" {
		model = DefaultAnthropicModel
	}
	return &anthropicClient{apiKey: apiKey, model: model, http: &http.Client{Timeout: 180 * time.Second}}
}

type anthropicRequest struct {
	Model      string              `json:"model"`
	MaxTokens  int                 `json:"max_tokens"`
	System     string              `json:"system"`
	Messages   []anthropicMessage  `json:"messages"`
	Tools      []anthropicTool     `json:"tools"`
	ToolChoice anthropicToolChoice `json:"tool_choice"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *anthropicClient) Infer(ctx context.Context, files []SourceFile) (*Draft, error) {
	reqBody := anthropicRequest{
		Model:     c.model,
		MaxTokens: anthropicMaxTokens,
		System:    systemPrompt,
		Messages:  []anthropicMessage{{Role: "user", Content: buildUserContent(files)}},
		Tools: []anthropicTool{{
			Name:        toolName,
			Description: "Emit the learned template as invariant/variable structure.",
			InputSchema: inputSchema(),
		}},
		ToolChoice: anthropicToolChoice{Type: "tool", Name: toolName},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("building anthropic request: %w", err)
	}

	headers := map[string]string{
		"x-api-key":         c.apiKey,
		"anthropic-version": anthropicVersion,
		"content-type":      "application/json",
	}
	respBody, status, err := postJSON(ctx, c.http, anthropicEndpoint, headers, body)
	if err != nil {
		return nil, fmt.Errorf("calling anthropic: %w", err)
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parsing anthropic response (status %d): %w\n%s", status, err, respBody)
	}
	if status != http.StatusOK {
		if parsed.Error != nil {
			return nil, fmt.Errorf("anthropic API error (%s): %s", parsed.Error.Type, parsed.Error.Message)
		}
		return nil, fmt.Errorf("anthropic API returned status %d: %s", status, respBody)
	}
	// A truncated response is still HTTP 200, and its tool_use JSON is cut off mid-object - parsing
	// it would surface as a confusing "malformed draft" instead of the real cause.
	if parsed.StopReason == "max_tokens" {
		return nil, fmt.Errorf("anthropic hit the %d-token output limit before finishing the draft - "+
			"the example folder is too large to emit as one template; trim it to just the pattern itself",
			anthropicMaxTokens)
	}

	for _, block := range parsed.Content {
		if block.Type == "tool_use" && block.Name == toolName {
			return ParseDraft(block.Input)
		}
	}
	return nil, fmt.Errorf("anthropic response did not include a %s tool call", toolName)
}
