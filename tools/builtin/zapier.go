package builtin

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var zapierHTTPClient = &http.Client{Timeout: 20 * time.Second}

// zapierInvoke sends a payload to a Zapier webhook (or previews it).
//
// Required args:
//   - action string (e.g. "calendar.create_event")
//
// Optional args:
//   - mode string ("preview" | "execute"), default "preview"
//   - approved bool|string (recommended for write actions)
//   - webhook_url string (overrides env lookup)
//   - any additional args become the payload body sent to Zapier
//
// Webhook URL resolution:
//  1. webhook_url arg
//  2. ACL_ZAPIER_WEBHOOK_<SANITIZED_ACTION>
//  3. ACL_ZAPIER_WEBHOOK_URL (generic)
//
// Returns:
//
//	{
//	  status, mode, action, approved, configured, request_id,
//	  webhook_url_set, payload, accepted, http_status, response_text, preview
//	}
func zapierInvoke(ctx context.Context, args map[string]any) (any, error) {
	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	if action == "" {
		return nil, fmt.Errorf("zapier.invoke: action argument required")
	}

	mode := strings.ToLower(strings.TrimSpace(asString(args["mode"])))
	if mode == "" {
		mode = "preview"
	}
	if mode != "preview" && mode != "execute" {
		return nil, fmt.Errorf("zapier.invoke: mode must be preview or execute (got %q)", mode)
	}
	approved := argBool(args, "approved")

	payload := make(map[string]any)
	for k, v := range args {
		switch k {
		case "action", "mode", "approved", "webhook_url":
			continue
		default:
			payload[k] = v
		}
	}
	payload["action"] = action
	payload["requested_at"] = time.Now().UTC().Format(time.RFC3339)

	requestID := zapierRequestID(action, payload)
	webhookURL := strings.TrimSpace(asString(args["webhook_url"]))
	if webhookURL == "" {
		webhookURL = resolveZapierWebhookURL(action)
	}
	configured := webhookURL != ""

	preview := map[string]any{
		"action":       action,
		"approved":     approved,
		"payload":      payload,
		"request_id":   requestID,
		"configured":   configured,
		"will_execute": mode == "execute" && approved && configured,
	}

	// Preview path is always safe and does not require a webhook.
	if mode == "preview" {
		out := map[string]any{
			"status":          "preview",
			"mode":            mode,
			"action":          action,
			"approved":        approved,
			"configured":      configured,
			"webhook_url_set": configured,
			"request_id":      requestID,
			"payload":         payload,
			"accepted":        false,
			"preview":         preview,
			"message":         "Preview only. No Zapier action executed.",
			"response_text":   "Preview only. No Zapier action executed.",
		}
		return withZapierPayloadAliases(out, payload), nil
	}

	// Execute path requires approval for safety.
	if !approved {
		out := map[string]any{
			"status":          "approval_required",
			"mode":            mode,
			"action":          action,
			"approved":        approved,
			"configured":      configured,
			"webhook_url_set": configured,
			"request_id":      requestID,
			"payload":         payload,
			"accepted":        false,
			"preview":         preview,
			"message":         "Execution blocked: explicit approval required.",
			"response_text":   "Execution blocked: explicit approval required.",
		}
		return withZapierPayloadAliases(out, payload), nil
	}

	if !configured {
		out := map[string]any{
			"status":          "not_configured",
			"mode":            mode,
			"action":          action,
			"approved":        approved,
			"configured":      false,
			"webhook_url_set": false,
			"request_id":      requestID,
			"payload":         payload,
			"accepted":        false,
			"preview":         preview,
			"message":         "No Zapier webhook configured for this action.",
			"response_text":   "No Zapier webhook configured for this action.",
		}
		return withZapierPayloadAliases(out, payload), nil
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("zapier.invoke: encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("zapier.invoke: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ACL-Action", action)
	req.Header.Set("X-ACL-Request-ID", requestID)

	resp, err := zapierHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zapier.invoke: http post: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := strings.TrimSpace(string(raw))
	if len(text) > 800 {
		text = text[:800] + "..."
	}

	status := "accepted"
	if resp.StatusCode >= 400 {
		status = "http_error"
	}
	out := map[string]any{
		"status":          status,
		"mode":            mode,
		"action":          action,
		"approved":        approved,
		"configured":      true,
		"webhook_url_set": true,
		"request_id":      requestID,
		"payload":         payload,
		"accepted":        resp.StatusCode >= 200 && resp.StatusCode < 300,
		"http_status":     int64(resp.StatusCode),
		"response_text":   text,
		"preview":         preview,
	}
	return withZapierPayloadAliases(out, payload), nil
}

func resolveZapierWebhookURL(action string) string {
	key := "ACL_ZAPIER_WEBHOOK_" + sanitizeZapierAction(action)
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("ACL_ZAPIER_WEBHOOK_URL")); v != "" {
		return v
	}
	return ""
}

func sanitizeZapierAction(action string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(action) {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	s = strings.Trim(s, "_")
	if s == "" {
		return "ACTION"
	}
	return s
}

func zapierRequestID(action string, payload map[string]any) string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(action)
	b.WriteByte('|')
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		j, _ := json.Marshal(payload[k])
		b.Write(j)
		b.WriteByte(';')
	}
	sum := sha1.Sum([]byte(b.String()))
	return "zap_" + hex.EncodeToString(sum[:8])
}

func withZapierPayloadAliases(out map[string]any, payload map[string]any) map[string]any {
	// Copy simple payload fields to top level so ACL interpolation can use z.title, z.starts_at, etc.
	for _, k := range []string{"title", "starts_at", "location", "attendee", "attendee_email", "notes"} {
		if _, exists := out[k]; exists {
			continue
		}
		if v, ok := payload[k]; ok {
			out[k] = v
		}
	}
	return out
}
