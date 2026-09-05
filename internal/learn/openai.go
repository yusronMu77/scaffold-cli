package learn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultOpenAIModel is used when --model is not given for the OpenAI-shaped provider. Confirmed
// against platform.openai.com/docs/models (2026-09-05): the current lineup is GPT-5.6
// Sol/Terra/Luna (flagship/balanced/cost-optimized). Terra is picked for tier parity with
// DefaultAnthropicModel ("claude-sonnet-5", Anthropic's own balanced tier, not its top-of-line
// Opus) - a default nobody explicitly chose shouldn't land on the most expensive tier. Worth
// reconfirming whenever the roster is next revisited; --model always overrides it regardless.
const DefaultOpenAIModel = "gpt-5.6-terra"

// DefaultOpenAIBaseURL is OpenAI's own endpoint. --base-url points this at any other
// OpenAI-compatible endpoint (Groq, OpenRouter, a local Ollama server, ...) with no new code.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// openAIClient implements Inferer against the OpenAI Chat Completions shape (no SDK). Two response
// mechanisms are supported (see responseFormat): a forced tool_choice, whose tool_calls arguments
// are a JSON string (unlike Anthropic's already-parsed object) needing its own json.Unmarshal; or
// response_format: json_schema, whose JSON comes back as the message's plain content string.
type openAIClient struct {
	apiKey         string
	baseURL        string
	model          string
	responseFormat string
	http           *http.Client
}

// NewOpenAIClient builds an Inferer for the OpenAI-compatible Chat Completions API. responseFormat
// is one of ResponseFormatTool/ResponseFormatJSONSchema; empty behaves as ResponseFormatTool.
func NewOpenAIClient(apiKey, baseURL, model, responseFormat string) Inferer {
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	if model == "" {
		model = DefaultOpenAIModel
	}
	return &openAIClient{
		apiKey:         apiKey,
		baseURL:        strings.TrimRight(baseURL, "/"),
		model:          model,
		responseFormat: responseFormat,
		http:           &http.Client{Timeout: 180 * time.Second},
	}
}

type openAIRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	Tools          []openAITool          `json:"tools,omitempty"`
	ToolChoice     *openAIToolChoice     `json:"tool_choice,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

// openAIResponseFormat asks for response_format: json_schema instead of a forced tool call - the
// alternative mechanism for a model that rejects forced tool_choice while still needing
// schema-valid JSON back. strict is deliberately false: inputSchema() (shared with the Anthropic
// tool schema) has genuinely optional fields, and strict mode requires every property listed as
// required with additionalProperties:false everywhere - reshaping it would touch the Anthropic
// path too for a benefit this mode alone doesn't need.
type openAIResponseFormat struct {
	Type       string                  `json:"type"`
	JSONSchema *openAIJSONSchemaFormat `json:"json_schema"`
}

type openAIJSONSchemaFormat struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict"`
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
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
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
	}
	if c.responseFormat == ResponseFormatJSONSchema {
		reqBody.ResponseFormat = &openAIResponseFormat{
			Type: "json_schema",
			JSONSchema: &openAIJSONSchemaFormat{
				Name:   toolName,
				Schema: inputSchema(),
				Strict: false,
			},
		}
	} else {
		reqBody.Tools = []openAITool{{
			Type: "function",
			Function: openAIToolFunction{
				Name:        toolName,
				Description: "Emit the learned template as invariant/variable structure.",
				Parameters:  inputSchema(),
			},
		}}
		reqBody.ToolChoice = &openAIToolChoice{Type: "function", Function: openAIToolChoiceFunction{Name: toolName}}
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("building openai request: %w", err)
	}

	headers := map[string]string{
		"Authorization": "Bearer " + c.apiKey,
		"content-type":  "application/json",
	}
	respBody, status, err := postJSON(ctx, c.http, c.baseURL+"/chat/completions", headers, body)
	if err != nil {
		return nil, fmt.Errorf("calling openai-compatible endpoint: %w", err)
	}

	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parsing openai-compatible response (status %d): %w\n%s", status, err, respBody)
	}
	if status != http.StatusOK {
		if parsed.Error != nil {
			return nil, fmt.Errorf("openai-compatible API error (%s): %s", parsed.Error.Type, parsed.Error.Message)
		}
		return nil, fmt.Errorf("openai-compatible API returned status %d: %s", status, respBody)
	}

	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("openai-compatible response carried no choices")
	}
	if parsed.Choices[0].FinishReason == "length" {
		// A response truncated at the endpoint's own output limit is still HTTP 200, in either
		// response mode, so say so instead of reporting a missing/empty result with no cause.
		return nil, fmt.Errorf("the endpoint hit its output limit before finishing the draft " +
			"(finish_reason=length) - the example folder is too large to emit as one template; " +
			"trim it to just the pattern itself")
	}

	if c.responseFormat == ResponseFormatJSONSchema {
		content := parsed.Choices[0].Message.Content
		if content == "" {
			return nil, fmt.Errorf("openai-compatible response (response_format=json_schema) carried no content")
		}
		return ParseDraft([]byte(content))
	}

	if len(parsed.Choices[0].Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("openai-compatible response did not include a %s tool call", toolName)
	}
	call := parsed.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != toolName {
		return nil, fmt.Errorf("openai-compatible response called %q, expected %q", call.Function.Name, toolName)
	}
	return ParseDraft([]byte(call.Function.Arguments))
}
