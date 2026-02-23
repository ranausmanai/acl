// Package ui serves the embedded ACL web dashboard at GET /.
package ui

import (
	_ "embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardHTML []byte

//go:embed agent.html
var agentHTML []byte

//go:embed agenticflow.html
var agenticFlowHTML []byte

//go:embed quickstart.html
var quickstartHTML []byte

// Handler returns an http.Handler that serves the dashboard HTML.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(dashboardHTML) //nolint
	})
}

// AgentHandler returns an http.Handler that serves the agent chat UI.
func AgentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(agentHTML) //nolint
	})
}

// AgenticFlowHandler returns an http.Handler that serves the multi-mode demo chat UI.
func AgenticFlowHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(agenticFlowHTML) //nolint
	})
}

// QuickstartHandler returns an http.Handler that serves the quickstart UI.
func QuickstartHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(quickstartHTML) //nolint
	})
}
