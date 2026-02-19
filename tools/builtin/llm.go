package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var llmHTTPClient = &http.Client{Timeout: 120 * time.Second}

// llmGenerate calls a real LLM provider.
//
// Required arg:
//
//	prompt  string  — the user message
//
// Optional args:
//
//	provider   string  — "anthropic"|"openai"|"groq"|"mistral"|"ollama"
//	                     defaults: ACL_LLM_PROVIDER env → infer from model →
//	                               auto-detect from set API keys
//	model      string  — provider-specific model id; defaults to ACL_LLM_MODEL
//	                     or a sensible default per provider
//	data       any     — extra context serialised as JSON in the system prompt
//	format     string  — "json"|"markdown"|"plain"  (instructs the model)
//	max_tokens int     — default 1024
//
// Returns {"text":string, "model":string, "tokens":int, "provider":string}
func llmGenerate(ctx context.Context, args map[string]any) (any, error) {
	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		return nil, fmt.Errorf("llm.generate: prompt argument required")
	}

	provider := resolveProvider(args)
	if provider == "" {
		return nil, fmt.Errorf(
			"llm.generate: no provider configured — set one of:\n" +
				"  ANTHROPIC_API_KEY   → uses claude-sonnet-4-6\n" +
				"  OPENAI_API_KEY      → uses gpt-4o\n" +
				"  GROQ_API_KEY        → uses llama-3.3-70b-versatile\n" +
				"  MISTRAL_API_KEY     → uses mistral-large-latest\n" +
				"  OLLAMA_HOST         → uses llama3.2 locally\n" +
				"  ACL_LLM_PROVIDER=<name>  to force a specific provider")
	}

	model := resolveModel(args, provider)

	maxTokens := int64(1024)
	if mt, ok := args["max_tokens"].(int64); ok && mt > 0 {
		maxTokens = mt
	}

	system := buildSystemPrompt(args["data"], args["format"])

	switch provider {
	case "anthropic":
		return callAnthropic(ctx, model, system, prompt, maxTokens)
	case "openai":
		return callOpenAICompat(ctx,
			"https://api.openai.com/v1/chat/completions",
			os.Getenv("OPENAI_API_KEY"), model, system, prompt, maxTokens, "openai")
	case "groq":
		return callOpenAICompat(ctx,
			"https://api.groq.com/openai/v1/chat/completions",
			os.Getenv("GROQ_API_KEY"), model, system, prompt, maxTokens, "groq")
	case "mistral":
		return callOpenAICompat(ctx,
			"https://api.mistral.ai/v1/chat/completions",
			os.Getenv("MISTRAL_API_KEY"), model, system, prompt, maxTokens, "mistral")
	case "ollama":
		return callOllama(ctx, model, system, prompt, maxTokens)
	default:
		return nil, fmt.Errorf("llm.generate: unknown provider %q (want: anthropic|openai|groq|mistral|ollama)", provider)
	}
}

// ── Provider resolution ────────────────────────────────────────────────────────

func resolveProvider(args map[string]any) string {
	// 1. Explicit arg.
	if p, _ := args["provider"].(string); p != "" {
		return strings.ToLower(p)
	}
	// 2. Infer from model name prefix.
	if m, _ := args["model"].(string); m != "" {
		switch {
		case strings.HasPrefix(m, "claude"):
			return "anthropic"
		case strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3"):
			return "openai"
		case strings.HasPrefix(m, "llama") || strings.HasPrefix(m, "mixtral") || strings.HasPrefix(m, "gemma"):
			return "groq"
		case strings.HasPrefix(m, "mistral") || strings.HasPrefix(m, "codestral"):
			return "mistral"
		}
	}
	// 3. Env var override.
	if p := os.Getenv("ACL_LLM_PROVIDER"); p != "" {
		return strings.ToLower(p)
	}
	// 4. Auto-detect from whichever API key is present.
	switch {
	case os.Getenv("ANTHROPIC_API_KEY") != "":
		return "anthropic"
	case os.Getenv("OPENAI_API_KEY") != "":
		return "openai"
	case os.Getenv("GROQ_API_KEY") != "":
		return "groq"
	case os.Getenv("MISTRAL_API_KEY") != "":
		return "mistral"
	case os.Getenv("OLLAMA_HOST") != "":
		return "ollama"
	}
	return ""
}

var defaultModels = map[string]string{
	"anthropic": "claude-sonnet-4-6",
	"openai":    "gpt-4o",
	"groq":      "llama-3.3-70b-versatile",
	"mistral":   "mistral-large-latest",
	"ollama":    "llama3.2",
}

func resolveModel(args map[string]any, provider string) string {
	if m, _ := args["model"].(string); m != "" {
		return m
	}
	if m := os.Getenv("ACL_LLM_MODEL"); m != "" {
		return m
	}
	return defaultModels[provider]
}

func buildSystemPrompt(data any, formatArg any) string {
	parts := []string{"You are a precise, concise AI assistant."}
	if data != nil {
		if b, err := json.Marshal(data); err == nil && string(b) != "null" {
			parts = append(parts, "Context data (JSON):\n"+string(b))
		}
	}
	switch strings.ToLower(fmt.Sprintf("%v", formatArg)) {
	case "json":
		parts = append(parts, "Respond ONLY with valid JSON. No explanation, no markdown fences.")
	case "markdown":
		parts = append(parts, "Format your response as clean Markdown.")
	default:
		parts = append(parts, "Respond with plain text.")
	}
	return strings.Join(parts, "\n\n")
}

// ── Anthropic ─────────────────────────────────────────────────────────────────

func callAnthropic(ctx context.Context, model, system, prompt string, maxTokens int64) (any, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("llm.generate (anthropic): ANTHROPIC_API_KEY not set")
	}

	payload, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm.generate (anthropic): %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("llm.generate (anthropic): HTTP %d: %s", resp.StatusCode, raw)
	}

	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Model string `json:"model"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("llm.generate (anthropic): parse: %w", err)
	}
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	return map[string]any{
		"text":     text,
		"model":    res.Model,
		"tokens":   int64(res.Usage.OutputTokens),
		"provider": "anthropic",
	}, nil
}

// ── OpenAI-compatible (OpenAI, Groq, Mistral) ────────────────────────────────

func callOpenAICompat(ctx context.Context, endpoint, key, model, system, prompt string, maxTokens int64, providerName string) (any, error) {
	if key == "" {
		envName := strings.ToUpper(providerName) + "_API_KEY"
		return nil, fmt.Errorf("llm.generate (%s): %s not set", providerName, envName)
	}

	payload, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": prompt},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm.generate (%s): %w", providerName, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("llm.generate (%s): HTTP %d: %s", providerName, resp.StatusCode, raw)
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("llm.generate (%s): parse: %w", providerName, err)
	}
	text := ""
	if len(res.Choices) > 0 {
		text = res.Choices[0].Message.Content
	}
	return map[string]any{
		"text":     text,
		"model":    res.Model,
		"tokens":   int64(res.Usage.CompletionTokens),
		"provider": providerName,
	}, nil
}

// ── Ollama (local) ────────────────────────────────────────────────────────────

func callOllama(ctx context.Context, model, system, prompt string, maxTokens int64) (any, error) {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}

	payload, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": system + "\n\n" + prompt,
		"stream": false,
		"options": map[string]any{
			"num_predict": maxTokens,
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", host+"/api/generate", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm.generate (ollama): %w — is Ollama running at %s?", err, host)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("llm.generate (ollama): HTTP %d: %s", resp.StatusCode, raw)
	}

	var res struct {
		Response  string `json:"response"`
		Model     string `json:"model"`
		EvalCount int    `json:"eval_count"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("llm.generate (ollama): parse: %w", err)
	}
	return map[string]any{
		"text":     res.Response,
		"model":    res.Model,
		"tokens":   int64(res.EvalCount),
		"provider": "ollama",
	}, nil
}
