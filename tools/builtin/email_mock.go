package builtin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// mockEmailDraft is the test-time replacement for email.draft.
// It validates arguments and returns a deterministic message_id without
// connecting to any SMTP server.
func mockEmailDraft(_ context.Context, args map[string]any) (any, error) {
	to, _ := args["to"].(string)
	if to == "" {
		return nil, fmt.Errorf("email.draft: to argument required")
	}
	if !strings.Contains(to, "@") {
		return nil, fmt.Errorf("email.draft: invalid to address %q", to)
	}
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)

	h := sha256.Sum256([]byte(to + subject + body))
	msgID := fmt.Sprintf("<mock_%x@acl.test>", h[:6])

	preview := body
	if len(preview) > 120 {
		preview = preview[:120] + "…"
	}

	result := map[string]any{
		"message_id": msgID,
		"to":         to,
		"subject":    subject,
		"preview":    preview,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
	}
	if attachments, ok := args["attachments"].([]any); ok {
		result["attachments"] = int64(len(attachments))
	}
	return result, nil
}
