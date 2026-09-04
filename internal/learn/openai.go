package learn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOpenAIModel is used when --model is not given for the OpenAI-shaped provider. Worth
// reconfirming against the target endpoint's current model roster - this only needs to be a
// reasonable default, since --model always overrides it.
const DefaultOpenAIModel = "gpt-4o"

// DefaultOpenAIBaseURL is OpenAI's own endpoint. --base-url points this at any other
// OpenAI-compatible endpoint (Groq, OpenRouter, a local Ollama server, ...) with no new code.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// openAIClient implements Inferer against the OpenAI Chat Completions shape (no SDK): a forced
// tool_choice makes the response carry exactly one tool_calls entry, whose arguments are a JSON
// string (unlike Anthropic's already-parsed object) that still needs its own json.Unmarshal.
type openAIClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// NewOpenAIClient builds an Inferer for the OpenAI-compatible Chat Completions API.
func NewOpenAIClient(apiKey, baseURL, model string) Inferer {
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	if model == "" {
		model = DefaultOpenAIModel
	}
	return &openAIClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{Timeout: 180 * time.Second},
	}
}

type openAIRequest struct {
	Model      string           `json:"model"`
	Messages   []openAIMessage  `json:"messages"`
	Tools      []openAITool     `json:"tools"`
	ToolChoice openAIToolChoice `json:"tool_choice"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolChoice struct {
	Type     string                   `json:"type"`
	Function openAIToolChoiceFunction `json:"function"`
}

type openAIToolChoiceFunction struct {
	Name string `json:"name"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (c *openAIClient) Infer(ctx context.Context, files []SourceFile) (*Draft, error) {
	reqBody := openAIRequest{
		Model: c.model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildUserContent(files)},
		},
		Tools: []openAITool{{
			Type: "function",
			Function: openAIToolFunction{
				Name:        toolName,
				Description: "Emit the learned template as invariant/variable structure.",
				Parameters:  inputSchema(),
			},
		}},
		ToolChoice: openAIToolChoice{Type: "function", Function: openAIToolChoiceFunction{Name: toolName}},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("building openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling openai-compatible endpoint: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading openai-compatible response: %w", err)
	}

	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parsing openai-compatible response (status %d): %w\n%s", resp.StatusCode, err, respBody)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return nil, fmt.Errorf("openai-compatible API error (%s): %s", parsed.Error.Type, parsed.Error.Message)
		}
		return nil, fmt.Errorf("openai-compatible API returned status %d: %s", resp.StatusCode, respBody)
	}

	if len(parsed.Choices) == 0 || len(parsed.Choices[0].Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("openai-compatible response did not include a %s tool call", toolName)
	}
	call := parsed.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != toolName {
		return nil, fmt.Errorf("openai-compatible response called %q, expected %q", call.Function.Name, toolName)
	}
	return ParseDraft([]byte(call.Function.Arguments))
}
