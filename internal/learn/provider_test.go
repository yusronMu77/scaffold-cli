package learn

import "testing"

func TestResolveClient_AutoDetectsSingleProvider(t *testing.T) {
	t.Setenv(EnvAnthropicAPIKey, "sk-ant-test")
	t.Setenv(EnvOpenAIAPIKey, "")

	client, err := ResolveClient("", "", "")
	if err != nil {
		t.Fatalf("ResolveClient returned error: %v", err)
	}
	if _, ok := client.(*anthropicClient); !ok {
		t.Fatalf("expected an anthropicClient, got %T", client)
	}
}

func TestResolveClient_AmbiguousWithoutExplicitProvider(t *testing.T) {
	t.Setenv(EnvAnthropicAPIKey, "sk-ant-test")
	t.Setenv(EnvOpenAIAPIKey, "sk-oai-test")

	if _, err := ResolveClient("", "", ""); err == nil {
		t.Fatal("expected an error when both provider keys are set and --provider is omitted")
	}
}

func TestResolveClient_NoProviderConfigured(t *testing.T) {
	t.Setenv(EnvAnthropicAPIKey, "")
	t.Setenv(EnvOpenAIAPIKey, "")

	if _, err := ResolveClient("", "", ""); err == nil {
		t.Fatal("expected an error when neither provider key is set")
	}
}

func TestResolveClient_ExplicitProviderMissingKey(t *testing.T) {
	t.Setenv(EnvAnthropicAPIKey, "")
	t.Setenv(EnvOpenAIAPIKey, "")

	if _, err := ResolveClient("anthropic", "", ""); err == nil {
		t.Fatal("expected an error requesting --provider=anthropic without ANTHROPIC_API_KEY set")
	}
}

func TestResolveClient_UnknownProvider(t *testing.T) {
	t.Setenv(EnvAnthropicAPIKey, "sk-ant-test")
	if _, err := ResolveClient("bogus", "", ""); err == nil {
		t.Fatal("expected an error for an unrecognized --provider value")
	}
}

func TestResolveClient_ExplicitOpenAI(t *testing.T) {
	t.Setenv(EnvOpenAIAPIKey, "sk-oai-test")
	client, err := ResolveClient("openai", "", "https://example.test/v1")
	if err != nil {
		t.Fatalf("ResolveClient returned error: %v", err)
	}
	oc, ok := client.(*openAIClient)
	if !ok {
		t.Fatalf("expected an openAIClient, got %T", client)
	}
	if oc.baseURL != "https://example.test/v1" {
		t.Errorf("expected base URL override to take effect, got %q", oc.baseURL)
	}
}
