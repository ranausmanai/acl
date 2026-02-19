package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpRequest is the http.request tool — full HTTP (GET, POST, PUT, DELETE, PATCH).
//
// Args:
//
//	url          string  (required)
//	method       string  GET|POST|PUT|DELETE|PATCH  (default: GET)
//	body         any     string or object (JSON-serialised for objects)
//	headers      map     additional request headers
//	bearer       string  shorthand: sets Authorization: Bearer <token>
//	api_key      string  shorthand: sets X-API-Key: <key>
//	content_type string  override Content-Type (default: application/json for body)
//	timeout      float   seconds (default 30)
//
// Returns: {status, text, json (if parseable), url, method}
func httpRequest(ctx context.Context, args map[string]any) (any, error) {
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return nil, fmt.Errorf("http.request: url is required")
	}

	method := "GET"
	if m, ok := args["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	timeoutSec := 30.0
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeoutSec = t
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec*float64(time.Second)))
	defer cancel()

	// Build body.
	var bodyReader io.Reader
	contentType := "application/json"
	if bodyVal, ok := args["body"]; ok && bodyVal != nil {
		switch v := bodyVal.(type) {
		case string:
			bodyReader = strings.NewReader(v)
			contentType = "text/plain"
		default:
			data, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("http.request: marshal body: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}
	}
	if ct, ok := args["content_type"].(string); ok && ct != "" {
		contentType = ct
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("http.request: %w", err)
	}

	req.Header.Set("User-Agent", "ACL/0.1 (+https://github.com/ranausmanai/acl)")
	if bodyReader != nil {
		req.Header.Set("Content-Type", contentType)
	}

	// Auth shorthands.
	if token, ok := args["bearer"].(string); ok && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key, ok := args["api_key"].(string); ok && key != "" {
		req.Header.Set("X-API-Key", key)
	}

	// Custom headers (override anything above).
	if headers, ok := args["headers"].(map[string]any); ok {
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http.request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http.request: read body: %w", err)
	}

	out := map[string]any{
		"status": resp.StatusCode,
		"text":   string(respBody),
		"url":    rawURL,
		"method": method,
	}

	// Attach parsed JSON if possible.
	var parsed any
	if json.Unmarshal(respBody, &parsed) == nil {
		out["json"] = parsed
	}

	return out, nil
}
