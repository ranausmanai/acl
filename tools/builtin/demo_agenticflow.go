package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

var demoState = struct {
	mu            sync.Mutex
	expenseSeq    int
	refundSeq     int
	noteSeq       int
	splitBalances map[string]float64
	orders        map[string]map[string]any
	events        map[string]map[string]any
}{
	splitBalances: map[string]float64{
		"Sarah": -42.0,
		"Ali":   18.0,
	},
	orders: map[string]map[string]any{
		"10482": {
			"order_id":           "10482",
			"status":             "delivered",
			"total_amount":       79.99,
			"currency":           "USD",
			"purchased_at":       "2026-02-18T13:21:00Z",
			"customer_email":     "sarah@example.com",
			"customer_name":      "Sarah Lee",
			"refund_eligible":    true,
			"refund_window_days": 30,
			"reason_hint":        "Damaged on delivery",
		},
		"10483": {
			"order_id":           "10483",
			"status":             "delivered",
			"total_amount":       24.50,
			"currency":           "USD",
			"purchased_at":       "2025-12-01T10:10:00Z",
			"customer_email":     "alex@example.com",
			"customer_name":      "Alex Martin",
			"refund_eligible":    false,
			"refund_window_days": 30,
			"reason_hint":        "Outside refund window",
		},
	},
	events: map[string]map[string]any{
		"evt_1001": {
			"event_id":  "evt_1001",
			"title":     "Coffee with Sarah",
			"starts_at": "2026-02-23T15:00:00-05:00",
			"location":  "Downtown Cafe",
			"attendees": []any{"sarah@example.com"},
			"owner":     "rana@example.com",
			"calendar":  "personal",
		},
		"evt_1002": {
			"event_id":  "evt_1002",
			"title":     "Product sync",
			"starts_at": "2026-02-24T11:00:00-05:00",
			"location":  "Zoom",
			"attendees": []any{"team@example.com"},
			"owner":     "rana@example.com",
			"calendar":  "work",
		},
	},
}

func demoSplitwiseFindContact(_ context.Context, args map[string]any) (any, error) {
	name := strings.TrimSpace(argString(args, "name"))
	if name == "" {
		return nil, fmt.Errorf("demo.splitwise.find_contact: name argument required")
	}
	all := []map[string]any{
		{"id": "c_sarah_1", "name": "Sarah Lee", "email": "sarah@example.com"},
		{"id": "c_sarah_2", "name": "Sarah Khan", "email": "sarah.khan@example.com"},
		{"id": "c_ali_1", "name": "Ali Raza", "email": "ali@example.com"},
	}
	lower := strings.ToLower(name)
	var matches []any
	for _, c := range all {
		n, _ := c["name"].(string)
		if strings.Contains(strings.ToLower(n), lower) {
			matches = append(matches, c)
		}
	}
	return map[string]any{
		"query":   name,
		"matches": matches,
		"count":   int64(len(matches)),
	}, nil
}

func demoSplitwiseCreateExpense(_ context.Context, args map[string]any) (any, error) {
	description := argString(args, "description")
	if description == "" {
		description = "Shared expense"
	}
	person := firstNonEmpty(argString(args, "person"), argString(args, "participant"))
	if person == "" {
		person = "Sarah"
	}
	payer := firstNonEmpty(argString(args, "paid_by"), argString(args, "payer"))
	if payer == "" {
		return nil, fmt.Errorf("demo.splitwise.create_expense: paid_by argument required")
	}
	currency := firstNonEmpty(argString(args, "currency"), "USD")
	amountEach := argNumber(args, "amount_each")
	if amountEach == 0 {
		amountEach = argNumber(args, "per_person_amount")
	}
	total := argNumber(args, "total_amount")
	if total == 0 && amountEach > 0 {
		total = amountEach * 2
	}
	if total == 0 {
		return nil, fmt.Errorf("demo.splitwise.create_expense: total_amount or amount_each required")
	}
	if amountEach == 0 {
		amountEach = total / 2
	}
	if strings.EqualFold(payer, "me") || strings.EqualFold(payer, "rana") || strings.EqualFold(payer, "you") {
		payer = "You"
	}

	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	demoState.expenseSeq++
	expenseID := fmt.Sprintf("exp_%04d", demoState.expenseSeq)

	// Positive means they owe you; negative means you owe them.
	demoState.splitBalances[person] += amountEach
	balanceAfter := demoState.splitBalances[person]
	createdAt := time.Now().UTC().Format(time.RFC3339)

	expense := map[string]any{
		"id":           expenseID,
		"description":  description,
		"total_amount": total,
		"currency":     currency,
		"paid_by":      payer,
		"split_type":   "equal",
		"participants": []any{"You", person},
		"created_at":   createdAt,
	}
	balances := []any{
		map[string]any{
			"person":       person,
			"owes_you":     round2(maxf(balanceAfter, 0)),
			"you_owe_them": round2(maxf(-balanceAfter, 0)),
			"net_balance":  round2(balanceAfter),
			"currency":     currency,
		},
	}
	return map[string]any{
		"status":  "ok",
		"expense": expense,
		"split": map[string]any{
			"per_person_amount": round2(amountEach),
			"owed_to_payer":     round2(amountEach),
		},
		"balances": balances,
		"message":  fmt.Sprintf("%s owes you %s %.2f for %s", person, currency, round2(amountEach), description),
	}, nil
}

func demoSplitwiseGetBalances(_ context.Context, _ map[string]any) (any, error) {
	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	var rows []any
	for name, bal := range demoState.splitBalances {
		rows = append(rows, map[string]any{
			"person":       name,
			"net_balance":  round2(bal),
			"owes_you":     round2(maxf(bal, 0)),
			"you_owe_them": round2(maxf(-bal, 0)),
			"currency":     "USD",
		})
	}
	return map[string]any{
		"balances": rows,
		"count":    int64(len(rows)),
	}, nil
}

func demoSupportGetOrder(_ context.Context, args map[string]any) (any, error) {
	orderID := strings.TrimSpace(firstNonEmpty(argString(args, "order_id"), argString(args, "id")))
	if orderID == "" {
		return nil, fmt.Errorf("demo.support.get_order: order_id argument required")
	}
	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	order, ok := demoState.orders[orderID]
	if !ok {
		return map[string]any{"found": false, "order_id": orderID}, nil
	}
	out := copyMap(order)
	out["found"] = true
	return out, nil
}

func demoSupportRefundOrder(_ context.Context, args map[string]any) (any, error) {
	orderID := strings.TrimSpace(argString(args, "order_id"))
	if orderID == "" {
		return nil, fmt.Errorf("demo.support.refund_order: order_id argument required")
	}
	approved := argBool(args, "approved")
	reason := firstNonEmpty(argString(args, "reason"), "Customer requested refund")
	amount := argNumber(args, "amount")

	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	order, ok := demoState.orders[orderID]
	if !ok {
		return map[string]any{"status": "not_found", "order_id": orderID}, nil
	}
	eligible, _ := order["refund_eligible"].(bool)
	if !eligible {
		return map[string]any{
			"status":   "rejected",
			"order_id": orderID,
			"reason":   firstNonEmpty(asString(order["reason_hint"]), "Not eligible"),
		}, nil
	}
	if !approved {
		return map[string]any{
			"status":   "approval_required",
			"order_id": orderID,
			"message":  "Refund requires explicit approval in this demo",
		}, nil
	}
	if amount == 0 {
		amount = asFloat(order["total_amount"])
	}
	demoState.refundSeq++
	refundID := fmt.Sprintf("rf_%04d", demoState.refundSeq)
	order["refund_eligible"] = false
	order["status"] = "refunded"
	return map[string]any{
		"status":       "refunded",
		"refund_id":    refundID,
		"order_id":     orderID,
		"amount":       round2(amount),
		"currency":     firstNonEmpty(asString(order["currency"]), "USD"),
		"reason":       reason,
		"processed_at": time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func demoSupportSendEmail(_ context.Context, args map[string]any) (any, error) {
	to := argString(args, "to")
	if to == "" {
		return nil, fmt.Errorf("demo.support.send_email: to argument required")
	}
	subject := firstNonEmpty(argString(args, "subject"), "Update on your order")
	body := argString(args, "body")
	if body == "" {
		body = "Your order update is ready."
	}
	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	demoState.noteSeq++
	msgID := fmt.Sprintf("demo_msg_%04d", demoState.noteSeq)
	preview := body
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}
	return map[string]any{
		"status":     "sent",
		"message_id": msgID,
		"to":         to,
		"subject":    subject,
		"preview":    preview,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func demoCalendarFindEvent(_ context.Context, args map[string]any) (any, error) {
	query := strings.ToLower(argString(args, "query"))
	if query == "" {
		query = strings.ToLower(firstNonEmpty(argString(args, "title"), ""))
	}
	q := strings.TrimSpace(query)
	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	var matches []any
	for _, evt := range demoState.events {
		title, _ := evt["title"].(string)
		startsAt, _ := evt["starts_at"].(string)
		// Treat generic words like "meeting"/"event" as a broad calendar query in the demo.
		generic := q == "" || q == "all" || q == "any" || q == "upcoming" || q == "schedule" ||
			q == "meeting" || q == "meetings" || q == "event" || q == "events" || q == "calendar"
		if generic || strings.Contains(strings.ToLower(title), q) || strings.Contains(strings.ToLower(startsAt), q) || strings.Contains(q, "sarah") && strings.Contains(strings.ToLower(title), "sarah") {
			row := copyMap(evt)
			if _, ok := row["id"]; !ok {
				row["id"] = row["event_id"]
			}
			matches = append(matches, row)
		}
	}
	return map[string]any{
		"query":  argString(args, "query"),
		"events": matches,
		"count":  int64(len(matches)),
	}, nil
}

func demoCalendarMoveEvent(_ context.Context, args map[string]any) (any, error) {
	eventID := argString(args, "event_id")
	if eventID == "" {
		return nil, fmt.Errorf("demo.calendar.move_event: event_id argument required")
	}
	newStartsAt := firstNonEmpty(argString(args, "new_starts_at"), argString(args, "new_start"))
	if newStartsAt == "" {
		return nil, fmt.Errorf("demo.calendar.move_event: new_starts_at argument required")
	}
	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	evt, ok := demoState.events[eventID]
	if !ok {
		return map[string]any{"status": "not_found", "event_id": eventID}, nil
	}
	old := asString(evt["starts_at"])
	evt["starts_at"] = newStartsAt
	return map[string]any{
		"status":        "moved",
		"id":            eventID,
		"event_id":      eventID,
		"title":         evt["title"],
		"old_starts_at": old,
		"new_starts_at": newStartsAt,
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func demoCalendarSendNote(_ context.Context, args map[string]any) (any, error) {
	eventID := argString(args, "event_id")
	message := firstNonEmpty(argString(args, "message"), argString(args, "body"))
	if eventID == "" {
		return nil, fmt.Errorf("demo.calendar.send_note: event_id argument required")
	}
	if message == "" {
		return nil, fmt.Errorf("demo.calendar.send_note: message argument required")
	}
	demoState.mu.Lock()
	defer demoState.mu.Unlock()
	evt, ok := demoState.events[eventID]
	if !ok {
		return map[string]any{"status": "not_found", "event_id": eventID}, nil
	}
	demoState.noteSeq++
	noteID := fmt.Sprintf("note_%04d", demoState.noteSeq)
	attendees, _ := evt["attendees"].([]any)
	return map[string]any{
		"status":         "sent",
		"note_id":        noteID,
		"id":             noteID,
		"event_id":       eventID,
		"attendee_count": int64(len(attendees)),
		"preview":        truncate(message, 100),
		"sent_at":        time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func argString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		return asString(v)
	}
	return ""
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return fmt.Sprintf("%.2f", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func argNumber(args map[string]any, key string) float64 {
	if v, ok := args[key]; ok {
		return asFloat(v)
	}
	return 0
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	default:
		return 0
	}
}

func argBool(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		switch x := v.(type) {
		case bool:
			return x
		case string:
			return strings.EqualFold(x, "true") || strings.EqualFold(x, "yes") || x == "1"
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
