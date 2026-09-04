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
func ResolveClient(provider, model, baseURL string) (Inferer, error) {
	anthropicKey := os.Getenv(EnvAnthropicAPIKey)
	openAIKey := os.Getenv(EnvOpenAIAPIKey)

	// --base-url only reaches the OpenAI-shaped client; the anthropic one always calls Anthropic's
	// own endpoint. Accepting it silently would send the request - and the API key - somewhere other
	// than the gateway the operator named.
	anthropic := func() (Inferer, error) {
		if baseURL != "" {
			return nil, fmt.Errorf("--base-url has no effect on the anthropic provider, which always "+
				"calls Anthropic's own endpoint - drop it, or pass --provider=openai to point at an "+
				"OpenAI-compatible gateway (it reads %s)", EnvOpenAIAPIKey)
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
		return NewOpenAIClient(openAIKey, baseURL, model), nil
	case "":
		switch {
		case anthropicKey != "" && openAIKey != "":
			return nil, fmt.Errorf("both %s and %s are set - pass --provider=anthropic or "+
				"--provider=openai to choose which one `learn` should call",
				EnvAnthropicAPIKey, EnvOpenAIAPIKey)
		case anthropicKey != "":
			return anthropic()
		case openAIKey != "":
			return NewOpenAIClient(openAIKey, baseURL, model), nil
		default:
			return nil, fmt.Errorf("no LLM provider configured - set %s or %s "+
				"(or pass --provider explicitly)", EnvAnthropicAPIKey, EnvOpenAIAPIKey)
		}
	default:
		return nil, fmt.Errorf("unknown --provider=%q, expected \"anthropic\" or \"openai\"", provider)
	}
}
