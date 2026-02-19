package runtime

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

	"acl/internal/receipt"
)

var remoteHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// callRemoteAgent POSTs to baseURL/run/agentName and returns the agent's result value.
// The request body is {"vars": inputs}. The response is a full receipt JSON.
//
// Auth: if ACL_SERVE_API_KEY is set it is forwarded as "Authorization: Bearer <key>".
// This means two ACL servers that talk to each other must share the same key.
func callRemoteAgent(ctx context.Context, name, baseURL string, inputs map[string]any) (any, error) {
	url := strings.TrimRight(baseURL, "/") + "/run/" + name

	body, err := json.Marshal(map[string]any{"vars": inputs})
	if err != nil {
		return nil, fmt.Errorf("remote agent %q: marshal request: %w", name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("remote agent %q: build request: %w", name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ACL/0.1")

	if key := os.Getenv("ACL_SERVE_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := remoteHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote agent %q: POST %s: %w", name, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote agent %q: server returned HTTP %d: %s", name, resp.StatusCode, raw)
	}

	var rec receipt.Receipt
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("remote agent %q: decode response: %w", name, err)
	}
	if rec.Status != "success" {
		msg := rec.Error
		if msg == "" {
			msg = "remote execution failed"
		}
		return nil, fmt.Errorf("remote agent %q: %s", name, msg)
	}
	if len(rec.Agents) == 0 {
		return nil, fmt.Errorf("remote agent %q: empty agent trace in response", name)
	}
	return rec.Agents[len(rec.Agents)-1].Result, nil
}
