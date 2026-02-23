package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ranausmanai/acl/internal/protocol"
)

// react.run — ReAct (Reason + Act) autonomous loop.
//
// The LLM decides which tool to call next based on observations,
// repeating until it produces a Final Answer or hits max_steps.
//
// Args:
//
//	goal      string  (required) — what to accomplish
//	tools     string  (required) — comma-separated tool names to allow
//	max_steps float   (optional) — max iterations, default 8
//	context   string  (optional) — extra background context for the LLM
//
// Returns: {answer, steps_taken, log:[{thought,action,input,observation}]}
func makeReactRun(reg *protocol.Registry) protocol.ToolFn {
	return func(ctx context.Context, args map[string]any) (any, error) {
		return reactRun(ctx, args, reg)
	}
}

// reactLogEntry records one Thought/Action/Observation cycle.
type reactLogEntry struct {
	Thought     string `json:"thought"`
	Action      string `json:"action,omitempty"`
	ActionInput string `json:"action_input,omitempty"`
	Observation string `json:"observation,omitempty"`
	FinalAnswer string `json:"final_answer,omitempty"`
}

func reactRun(ctx context.Context, args map[string]any, reg *protocol.Registry) (any, error) {
	goal, _ := args["goal"].(string)
	if goal == "" {
		return nil, fmt.Errorf("react.run: goal is required")
	}
	toolsRaw, _ := args["tools"].(string)
	if toolsRaw == "" {
		return nil, fmt.Errorf("react.run: tools is required (comma-separated list)")
	}
	maxSteps := 8
	if ms, ok := args["max_steps"].(float64); ok && ms > 0 {
		maxSteps = int(ms)
	}
	extraContext, _ := args["context"].(string)

	// Validate tool names.
	var allowedTools []string
	for _, t := range strings.Split(toolsRaw, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !reg.Has(t) {
			return nil, fmt.Errorf("react.run: tool %q not registered", t)
		}
		allowedTools = append(allowedTools, t)
	}
	if len(allowedTools) == 0 {
		return nil, fmt.Errorf("react.run: no valid tools specified")
	}

	system := buildReactSystem(allowedTools, extraContext)

	var entries []reactLogEntry

	// The running transcript sent to the LLM each turn.
	// We embed the system instructions at the top of the prompt.
	transcript := system + "\nGoal: " + goal + "\n\n"

	for step := 0; step < maxSteps; step++ {
		// Call LLM — prime it with "Thought:" to enforce the format.
		llmResult, err := llmGenerate(ctx, map[string]any{
			"prompt":     transcript + "Thought:",
			"max_tokens": int64(512),
		})
		if err != nil {
			return nil, fmt.Errorf("react.run step %d: llm: %w", step+1, err)
		}
		raw, _ := llmResult.(map[string]any)
		response, _ := raw["text"].(string)
		response = strings.TrimSpace(response)

		// Prepend "Thought:" since we primed the LLM with it.
		fullResponse := "Thought:" + response

		// Check for Final Answer.
		if idx := strings.Index(fullResponse, "Final Answer:"); idx >= 0 {
			answer := strings.TrimSpace(fullResponse[idx+len("Final Answer:"):])
			thought := extractSection(fullResponse, "Thought:", "Final Answer:")
			entries = append(entries, reactLogEntry{Thought: thought, FinalAnswer: answer})
			return map[string]any{
				"answer":      answer,
				"steps_taken": step + 1,
				"log":         entries,
			}, nil
		}

		// Parse Action and Action Input.
		thought := extractSection(fullResponse, "Thought:", "Action:")
		action := strings.TrimSpace(extractSection(fullResponse, "Action:", "Action Input:"))
		actionInputRaw := strings.TrimSpace(extractAfter(fullResponse, "Action Input:"))

		if action == "" {
			// LLM didn't follow the format — nudge it.
			transcript += fullResponse + "\nObservation: Please use the format: Action: <tool>\nAction Input: {\"key\":\"value\"}\n\n"
			entries = append(entries, reactLogEntry{Thought: thought, Observation: "format error"})
			continue
		}

		// Validate the action is an allowed tool.
		if !contains(allowedTools, action) {
			obs := fmt.Sprintf("Error: %q is not an allowed tool. Use one of: %s", action, strings.Join(allowedTools, ", "))
			transcript += fullResponse + "\nObservation: " + obs + "\n\n"
			entries = append(entries, reactLogEntry{Thought: thought, Action: action, Observation: obs})
			continue
		}

		// Parse Action Input as JSON.
		toolArgs := map[string]any{}
		if actionInputRaw != "" {
			jsonStr := extractJSON(actionInputRaw)
			if jsonStr != "" {
				json.Unmarshal([]byte(jsonStr), &toolArgs) //nolint
			}
		}

		// Execute the tool.
		toolResult, toolErr := reg.Call(ctx, action, toolArgs)
		var observation string
		if toolErr != nil {
			observation = "Error: " + toolErr.Error()
		} else {
			b, _ := json.Marshal(toolResult)
			observation = string(b)
			if len(observation) > 1500 {
				observation = observation[:1500] + "... [truncated]"
			}
		}

		entries = append(entries, reactLogEntry{
			Thought:     thought,
			Action:      action,
			ActionInput: actionInputRaw,
			Observation: observation,
		})

		transcript += fullResponse + "\nObservation: " + observation + "\n\n"
	}

	// Reached max steps.
	return map[string]any{
		"answer":      "Max steps reached. Last observation: " + reactLastObservation(entries),
		"steps_taken": maxSteps,
		"log":         entries,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func buildReactSystem(tools []string, extraContext string) string {
	toolDescs := map[string]string{
		"http.request":    "Make HTTP requests (GET, POST, etc.) to any URL",
		"http.get":        "Fetch a URL via GET request",
		"search.web":      "Search the web for current information",
		"browser.fetch":   "Fetch a web page and return readable text",
		"llm.generate":    "Generate text using an LLM",
		"json.parse":      "Parse a JSON string into structured data",
		"json.stringify":  "Convert data to a JSON string",
		"file.read":       "Read a file from disk",
		"file.write":      "Write content to a file",
		"memory.get":      "Retrieve a previously stored value by key",
		"memory.set":      "Store a value by key for later retrieval",
		"memory.messages": "Manage a conversation thread (get/append/clear messages)",
		"vision.describe": "Analyze and describe an image from a URL or file path",
		"code.run":        "Execute a Python or JavaScript code snippet",
		"text.chunk":      "Split text into overlapping chunks",
		"csv.parse":       "Parse a CSV string into rows",
		"sql.query":       "Run a SQL query against a database",
	}

	var sb strings.Builder
	sb.WriteString("You are an autonomous agent that reasons and acts step by step.\n\n")
	sb.WriteString("Available tools:\n")
	for _, t := range tools {
		desc := toolDescs[t]
		if desc == "" {
			desc = "Tool: " + t
		}
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", t, desc))
	}
	if extraContext != "" {
		sb.WriteString("\nContext:\n" + extraContext + "\n")
	}
	sb.WriteString(`
Always use this EXACT format:

Thought: [your reasoning about what to do next]
Action: [tool name exactly as listed above]
Action Input: {"key": "value"}

After receiving an Observation, continue with another Thought/Action or:

Thought: I now have enough information to answer.
Final Answer: [your complete answer]
`)
	return sb.String()
}

func extractSection(s, start, end string) string {
	si := strings.Index(s, start)
	if si < 0 {
		return ""
	}
	s = s[si+len(start):]
	ei := strings.Index(s, end)
	if ei < 0 {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:ei])
}

func extractAfter(s, marker string) string {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(s[idx+len(marker):])
}

func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func reactLastObservation(entries []reactLogEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Observation != "" {
			return entries[i].Observation
		}
	}
	return ""
}
