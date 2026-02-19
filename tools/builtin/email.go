package builtin

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// emailDraft composes and sends a real email via SMTP.
//
// Args:
//
//	to       string  — recipient address (required)
//	subject  string  — email subject
//	body     string  — plain-text body
//	cc       string  — comma-separated CC addresses (optional)
//	reply_to string  — reply-to address (optional)
//
// SMTP config (all from environment):
//
//	ACL_SMTP_HOST  — SMTP hostname (required, e.g. smtp.gmail.com)
//	ACL_SMTP_PORT  — port (default: 587 for STARTTLS, 465 for TLS, 25 plain)
//	ACL_SMTP_USER  — SMTP username / email address
//	ACL_SMTP_PASS  — SMTP password or app-password
//	ACL_SMTP_FROM  — From address (defaults to ACL_SMTP_USER)
//
// Returns {"message_id":string, "to":string, "subject":string, "preview":string, "sent_at":string}
//
// If ACL_SMTP_HOST is not set the function returns an error explaining the setup.
func emailDraft(ctx context.Context, args map[string]any) (any, error) {
	to, _ := args["to"].(string)
	if to == "" {
		return nil, fmt.Errorf("email.draft: to argument required")
	}
	if !strings.Contains(to, "@") {
		return nil, fmt.Errorf("email.draft: invalid to address %q", to)
	}

	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)
	cc, _ := args["cc"].(string)
	replyTo, _ := args["reply_to"].(string)

	host := os.Getenv("ACL_SMTP_HOST")
	if host == "" {
		return nil, fmt.Errorf(
			"email.draft: ACL_SMTP_HOST not set.\n" +
				"  Set these environment variables:\n" +
				"    ACL_SMTP_HOST=smtp.gmail.com\n" +
				"    ACL_SMTP_PORT=587\n" +
				"    ACL_SMTP_USER=you@gmail.com\n" +
				"    ACL_SMTP_PASS=your-app-password\n" +
				"    ACL_SMTP_FROM=you@gmail.com\n" +
				"  For Gmail, create an App Password at myaccount.google.com/apppasswords")
	}

	port := os.Getenv("ACL_SMTP_PORT")
	if port == "" {
		port = "587"
	}
	user := os.Getenv("ACL_SMTP_USER")
	pass := os.Getenv("ACL_SMTP_PASS")
	from := os.Getenv("ACL_SMTP_FROM")
	if from == "" {
		from = user
	}
	if from == "" {
		return nil, fmt.Errorf("email.draft: ACL_SMTP_FROM or ACL_SMTP_USER must be set")
	}

	// Build the RFC 2822 message.
	msgID := fmt.Sprintf("<%d.acl@%s>", time.Now().UnixNano(), host)
	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	if cc != "" {
		msg.WriteString("Cc: " + cc + "\r\n")
	}
	if replyTo != "" {
		msg.WriteString("Reply-To: " + replyTo + "\r\n")
	}
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("Message-ID: " + msgID + "\r\n")
	msg.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	addr := net.JoinHostPort(host, port)

	if err := sendSMTP(ctx, addr, host, port, user, pass, from, to, cc, msg.String()); err != nil {
		return nil, fmt.Errorf("email.draft: send: %w", err)
	}

	preview := body
	if len(preview) > 120 {
		preview = preview[:120] + "…"
	}

	return map[string]any{
		"message_id": msgID,
		"to":         to,
		"subject":    subject,
		"preview":    preview,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// sendSMTP delivers the message using STARTTLS (port 587), TLS (port 465),
// or plain SMTP (port 25). It uses AUTH PLAIN / LOGIN when credentials are set.
func sendSMTP(_ context.Context, addr, host, port, user, pass, from, to, cc, rawMsg string) error {
	recipients := []string{to}
	if cc != "" {
		for _, r := range strings.Split(cc, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				recipients = append(recipients, r)
			}
		}
	}

	msgBytes := []byte(rawMsg)
	var auth smtp.Auth
	if user != "" && pass != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}

	switch port {
	case "465": // SMTPS — TLS from the start.
		tlsCfg := &tls.Config{ServerName: host}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("TLS dial %s: %w", addr, err)
		}
		defer conn.Close()
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("smtp client: %w", err)
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		return smtpSend(c, from, recipients, msgBytes)

	default: // 587 (STARTTLS) or 25 (plain).
		c, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("dial %s: %w", addr, err)
		}
		defer c.Close()
		if ok, _ := c.Extension("STARTTLS"); ok {
			tlsCfg := &tls.Config{ServerName: host}
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		}
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
		return smtpSend(c, from, recipients, msgBytes)
	}
}

func smtpSend(c *smtp.Client, from string, to []string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("RCPT TO <%s>: %w", r, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}
	return c.Quit()
}
