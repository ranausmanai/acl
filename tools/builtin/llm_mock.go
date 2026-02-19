package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// mockLLMGenerate is the test-time replacement for llm.generate.
// It produces deterministic output from the input (same prompt → same text)
// so tests are reproducible without any API key.
func mockLLMGenerate(_ context.Context, args map[string]any) (any, error) {
	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		return nil, fmt.Errorf("llm.generate: prompt argument required")
	}
	dataArg := args["data"]
	format, _ := args["format"].(string)

	seed := mockLLMSeed(prompt, dataArg, format)
	rng := rand.New(rand.NewSource(seed))
	text := mockLLMText(prompt, format, rng)

	return map[string]any{
		"text":     text,
		"model":    "acl-mock-2.0",
		"tokens":   int64(len(strings.Fields(text))*4/3 + 10),
		"provider": "mock",
	}, nil
}

func mockLLMSeed(prompt string, data any, format string) int64 {
	b, _ := json.Marshal(data)
	h := sha256.Sum256([]byte(prompt + string(b) + format))
	seed, _ := strconv.ParseInt(fmt.Sprintf("%x", h[:4]), 16, 64)
	return seed
}

func mockLLMText(prompt, format string, rng *rand.Rand) string {
	summaries := []string{
		"Based on the provided data, the analysis reveals strong performance indicators with positive trends.",
		"The findings suggest significant optimization opportunities across multiple dimensions.",
		"After thorough evaluation, the results demonstrate consistent patterns with actionable insights.",
		"The comprehensive review indicates several key areas for strategic investment.",
		"Data analysis confirms the primary hypothesis with strong supporting evidence.",
	}
	conclusions := []string{
		"Recommend proceeding with the outlined strategy.",
		"Further investigation is warranted in the identified areas.",
		"Implementation should prioritize high-impact items first.",
		"Results exceed baseline expectations by a significant margin.",
		"Action items have been identified and prioritized by impact.",
	}
	summary := summaries[rng.Intn(len(summaries))]
	conclusion := conclusions[rng.Intn(len(conclusions))]
	confidence := 0.70 + rng.Float64()*0.29
	switch strings.ToLower(format) {
	case "json":
		return fmt.Sprintf(`{"summary":"%s","conclusion":"%s","confidence":%.2f,"source":"llm.generate"}`,
			summary, conclusion, confidence)
	case "markdown":
		return fmt.Sprintf("## Summary\n\n%s\n\n## Conclusion\n\n%s\n\n*Confidence: %.0f%%*\n",
			summary, conclusion, confidence*100)
	default:
		return fmt.Sprintf("%s %s (confidence: %.0f%%)", summary, conclusion, confidence*100)
	}
}
