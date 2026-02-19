package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// audio.transcribe — transcribe an audio file to text using Whisper.
//
// Provider resolution: GROQ_API_KEY (whisper-large-v3) → OPENAI_API_KEY (whisper-1)
//
// Args:
//
//	file      string  local file path (required) — mp3, mp4, wav, m4a, ogg, flac, webm
//	language  string  ISO-639-1 language code (optional, e.g. "en", "ar")
//	provider  string  override provider
//
// Returns: {text, language, duration_s, segments: [{text, start, end}], model, provider}
func audioTranscribe(ctx context.Context, args map[string]any) (any, error) {
	filePath, _ := args["file"].(string)
	if filePath == "" {
		return nil, fmt.Errorf("audio.transcribe: file is required")
	}

	language, _ := args["language"].(string)
	provider, _ := args["provider"].(string)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("audio.transcribe: read file: %w", err)
	}
	filename := filepath.Base(filePath)

	if provider == "" {
		switch {
		case os.Getenv("GROQ_API_KEY") != "":
			provider = "groq"
		case os.Getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		default:
			return nil, fmt.Errorf("audio.transcribe: set GROQ_API_KEY or OPENAI_API_KEY")
		}
	}

	switch provider {
	case "groq":
		return whisperCall(ctx, data, filename, language,
			"https://api.groq.com/openai/v1/audio/transcriptions",
			"whisper-large-v3", os.Getenv("GROQ_API_KEY"), "groq")
	case "openai":
		return whisperCall(ctx, data, filename, language,
			"https://api.openai.com/v1/audio/transcriptions",
			"whisper-1", os.Getenv("OPENAI_API_KEY"), "openai")
	default:
		return nil, fmt.Errorf("audio.transcribe: unknown provider %q", provider)
	}
}

func whisperCall(ctx context.Context, data []byte, filename, language, endpoint, model, apiKey, provider string) (any, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("audio.transcribe: create form file: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return nil, fmt.Errorf("audio.transcribe: write file: %w", err)
	}
	w.WriteField("model", model)          //nolint
	w.WriteField("response_format", "verbose_json") //nolint
	if language != "" {
		w.WriteField("language", language) //nolint
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audio.transcribe (%s): %w", provider, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			Text  string  `json:"text"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("audio.transcribe: parse response: %w — body: %s", err, preview)
	}

	var segments []any
	for _, s := range result.Segments {
		segments = append(segments, map[string]any{
			"text":  strings.TrimSpace(s.Text),
			"start": s.Start,
			"end":   s.End,
		})
	}
	if segments == nil {
		segments = []any{}
	}

	return map[string]any{
		"text":       strings.TrimSpace(result.Text),
		"language":   result.Language,
		"duration_s": result.Duration,
		"segments":   segments,
		"model":      model,
		"provider":   provider,
	}, nil
}
