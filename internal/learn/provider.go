package learn

import (
	"fmt"
	"os"
)

// Env var names read for each provider's API key. Neither is treated as "the" default - see
// ResolveClient.
const (
	EnvAnthropicAPIKey = "ANTHROPIC_API_KEY"
	EnvOpenAIAPIKey    = "OPENAI_API_KEY"
)

// ResolveClient picks which Inferer to build. An explicit provider is honored as given; with none
// given, it auto-detects from whichever single API-key env var is set, so neither provider is a
// hardcoded default - only one of the two keys being present is what decides it.
//
// responseFormat selects how the OpenAI-shaped client asks for JSON back (ResponseFormatTool or
// ResponseFormatJSONSchema); empty defaults to ResponseFormatTool, today's only behavior.
func ResolveClient(provider, model, baseURL, responseFormat string) (Inferer, error) {
	anthropicKey := os.Getenv(EnvAnthropicAPIKey)
	openAIKey := os.Getenv(EnvOpenAIAPIKey)

	if responseFormat == "" {
		responseFormat = ResponseFormatTool
	}
	if responseFormat != ResponseFormatTool && responseFormat != ResponseFormatJSONSchema {
		return nil, fmt.Errorf("unknown --response-format=%q, expected %q or %q",
			responseFormat, ResponseFormatTool, ResponseFormatJSONSchema)
	}

	// --base-url and --response-format=json_schema only reach the OpenAI-shaped client; the
	// anthropic one always calls Anthropic's own endpoint and only ever forces a tool call.
	// Accepting either silently would send the request - and the API key, for --base-url - or
	// produce a shape Anthropic doesn't support.
	anthropic := func() (Inferer, error) {
		if baseURL != "" {
			return nil, fmt.Errorf("--base-url has no effect on the anthropic provider, which always "+
				"calls Anthropic's own endpoint - drop it, or pass --provider=openai to point at an "+
				"OpenAI-compatible gateway (it reads %s)", EnvOpenAIAPIKey)
		}
		if responseFormat != ResponseFormatTool {
			return nil, fmt.Errorf("--response-format has no effect on the anthropic provider, which "+
				"only supports forced tool use - drop it, or pass --provider=openai (it reads %s)",
				EnvOpenAIAPIKey)
		}
		return NewAnthropicClient(anthropicKey, model), nil
	}

	switch provider {
	case "anthropic":
		if anthropicKey == "" {
			return nil, fmt.Errorf("--provider=anthropic requires %s to be set", EnvAnthropicAPIKey)
		}
		return anthropic()
	case "openai":
		if openAIKey == "" {
			return nil, fmt.Errorf("--provider=openai requires %s to be set", EnvOpenAIAPIKey)
		}
		return NewOpenAIClient(openAIKey, baseURL, model, responseFormat), nil
	case "":
		switch {
		case anthropicKey != "" && openAIKey != "":
			return nil, fmt.Errorf("both %s and %s are set - pass --provider=anthropic or "+
				"--provider=openai to choose which one `learn` should call",
				EnvAnthropicAPIKey, EnvOpenAIAPIKey)
		case anthropicKey != "":
			return anthropic()
		case openAIKey != "":
			return NewOpenAIClient(openAIKey, baseURL, model, responseFormat), nil
		default:
			return nil, fmt.Errorf("no LLM provider configured - set %s or %s "+
				"(or pass --provider explicitly)", EnvAnthropicAPIKey, EnvOpenAIAPIKey)
		}
	default:
		return nil, fmt.Errorf("unknown --provider=%q, expected \"anthropic\" or \"openai\"", provider)
	}
}
