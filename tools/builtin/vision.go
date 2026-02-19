package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// vision.describe — describe or analyze an image using a vision-capable LLM.
//
// Provider resolution: ANTHROPIC_API_KEY → OPENAI_API_KEY → GROQ_API_KEY
//
// Args:
//
//	url      string  public image URL (use this OR path)
//	path     string  local image file path (use this OR url)
//	prompt   string  (default: "Describe this image in detail.")
//	provider string  override provider
//
// Returns: {description, model, provider}
func visionDescribe(ctx context.Context, args map[string]any) (any, error) {
	imageURL, _ := args["url"].(string)
	imagePath, _ := args["path"].(string)
	if imageURL == "" && imagePath == "" {
		return nil, fmt.Errorf("vision.describe: url or path is required")
	}

	prompt := "Describe this image in detail."
	if p, ok := args["prompt"].(string); ok && p != "" {
		prompt = p
	}

	provider, _ := args["provider"].(string)
	if provider == "" {
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") != "":
			provider = "anthropic"
		case os.Getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		case os.Getenv("GROQ_API_KEY") != "":
			provider = "groq"
		default:
			return nil, fmt.Errorf("vision.describe: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GROQ_API_KEY")
		}
	}

	switch provider {
	case "anthropic":
		return visionAnthropic(ctx, imageURL, imagePath, prompt, os.Getenv("ANTHROPIC_API_KEY"))
	case "openai":
		return visionOpenAI(ctx, imageURL, imagePath, prompt, os.Getenv("OPENAI_API_KEY"))
	case "groq":
		return visionGroq(ctx, imageURL, prompt, os.Getenv("GROQ_API_KEY"))
	default:
		return nil, fmt.Errorf("vision.describe: unknown provider %q", provider)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func imageToBase64(path string) (b64 string, mediaType string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".png"):
		mediaType = "image/png"
	case strings.HasSuffix(lower, ".gif"):
		mediaType = "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		mediaType = "image/webp"
	default:
		mediaType = "image/jpeg"
	}
	return base64.StdEncoding.EncodeToString(data), mediaType, nil
}

func visionAnthropic(ctx context.Context, imageURL, imagePath, prompt, apiKey string) (any, error) {
	var imgContent map[string]any
	if imagePath != "" {
		b64, mt, err := imageToBase64(imagePath)
		if err != nil {
			return nil, fmt.Errorf("vision.describe: %w", err)
		}
		imgContent = map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "base64", "media_type": mt, "data": b64,
			},
		}
	} else {
		imgContent = map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "url", "url": imageURL},
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"model":      "claude-opus-4-6",
		"max_tokens": 1024,
		"messages": []any{map[string]any{
			"role":    "user",
			"content": []any{imgContent, map[string]any{"type": "text", "text": prompt}},
		}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vision.describe (anthropic): %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct{ Text string `json:"text"` } `json:"content"`
		Model   string                                `json:"model"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint
	text := ""
	if len(result.Content) > 0 {
		text = result.Content[0].Text
	}
	return map[string]any{"description": text, "model": result.Model, "provider": "anthropic"}, nil
}

func visionOpenAI(ctx context.Context, imageURL, imagePath, prompt, apiKey string) (any, error) {
	var imgMsg map[string]any
	if imagePath != "" {
		b64, mt, err := imageToBase64(imagePath)
		if err != nil {
			return nil, fmt.Errorf("vision.describe: %w", err)
		}
		imgMsg = map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": fmt.Sprintf("data:%s;base64,%s", mt, b64)},
		}
	} else {
		imgMsg = map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": imageURL},
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"model":      "gpt-4o",
		"max_tokens": 1024,
		"messages": []any{map[string]any{
			"role":    "user",
			"content": []any{imgMsg, map[string]any{"type": "text", "text": prompt}},
		}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://api.openai.com/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vision.describe (openai): %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct{ Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
	}
	json.NewDecoder(resp.Body).Decode(&result) //nolint
	text := ""
	if len(result.Choices) > 0 {
		text = result.Choices[0].Message.Content
	}
	return map[string]any{"description": text, "model": result.Model, "provider": "openai"}, nil
}

func visionGroq(ctx context.Context, imageURL, prompt, apiKey string) (any, error) {
	// Groq vision only supports URLs (no base64 upload).
	if imageURL == "" {
		return nil, fmt.Errorf("vision.describe (groq): only url is supported, not local path")
	}
	payload, _ := json.Marshal(map[string]any{
		"model":      "llama-3.2-11b-vision-preview",
		"max_tokens": 1024,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}},
				map[string]any{"type": "text", "text": prompt},
			},
		}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vision.describe (groq): %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Choices []struct {
			Message struct{ Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
	}
	json.Unmarshal(body, &result) //nolint
	text := ""
	if len(result.Choices) > 0 {
		text = result.Choices[0].Message.Content
	}
	return map[string]any{"description": text, "model": result.Model, "provider": "groq"}, nil
}
