package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type File struct {
	Version  string      `json:"version"`
	Provider string      `json:"provider"`
	BaseURL  string      `json:"base_url,omitempty"`
	Auth     Auth        `json:"auth,omitempty"`
	Tools    []ToolEntry `json:"tools"`
}

type Auth struct {
	Type string `json:"type,omitempty"`
	Env  string `json:"env,omitempty"`
}

type ToolEntry struct {
	Name             string         `json:"name"`
	Transport        string         `json:"transport,omitempty"`
	Method           string         `json:"method,omitempty"`
	Path             string         `json:"path,omitempty"`
	SideEffect       string         `json:"side_effect,omitempty"`
	ApprovalRequired bool           `json:"approval_required,omitempty"`
	Input            map[string]any `json:"input,omitempty"`
	Output           map[string]any `json:"output,omitempty"`
}

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m File
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest JSON: %w", err)
	}
	return &m, nil
}

func ParseBytes(b []byte) (*File, error) {
	var m File
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse manifest JSON: %w", err)
	}
	return &m, nil
}

func Validate(m *File) []error {
	if m == nil {
		return []error{fmt.Errorf("manifest is nil")}
	}
	var errs []error
	if len(m.Tools) == 0 {
		errs = append(errs, fmt.Errorf("tools: at least one tool is required"))
	}
	seen := map[string]bool{}
	for i, t := range m.Tools {
		p := fmt.Sprintf("tools[%d]", i)
		if strings.TrimSpace(t.Name) == "" {
			errs = append(errs, fmt.Errorf("%s.name: required", p))
		} else if seen[t.Name] {
			errs = append(errs, fmt.Errorf("%s.name: duplicate %q", p, t.Name))
		} else {
			seen[t.Name] = true
		}
		if t.Transport == "" {
			errs = append(errs, fmt.Errorf("%s.transport: required", p))
		}
		if t.Transport == "http" {
			if t.Method == "" {
				errs = append(errs, fmt.Errorf("%s.method: required for http tools", p))
			}
			if t.Path == "" {
				errs = append(errs, fmt.Errorf("%s.path: required for http tools", p))
			}
		}
		if t.SideEffect != "" && t.SideEffect != "read" && t.SideEffect != "write" && t.SideEffect != "external" {
			errs = append(errs, fmt.Errorf("%s.side_effect: must be read|write|external", p))
		}
	}
	return errs
}

func Summary(m *File) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "provider  %s\n", emptyDash(m.Provider))
	fmt.Fprintf(&b, "version   %s\n", emptyDash(m.Version))
	if m.BaseURL != "" {
		fmt.Fprintf(&b, "base_url  %s\n", m.BaseURL)
	}
	if m.Auth.Type != "" || m.Auth.Env != "" {
		fmt.Fprintf(&b, "auth      %s", emptyDash(m.Auth.Type))
		if m.Auth.Env != "" {
			fmt.Fprintf(&b, " (env: %s)", m.Auth.Env)
		}
		b.WriteByte('\n')
	}
	if len(m.Tools) > 0 {
		b.WriteString("tools\n")
		tools := append([]ToolEntry(nil), m.Tools...)
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		for _, t := range tools {
			side := t.SideEffect
			if side == "" {
				side = "-"
			}
			meth := t.Method
			if meth == "" {
				meth = "-"
			}
			path := t.Path
			if path == "" {
				path = "-"
			}
			appr := "no"
			if t.ApprovalRequired {
				appr = "yes"
			}
			fmt.Fprintf(&b, "  - %s [%s %s] side=%s approval=%s\n", t.Name, meth, path, side, appr)
		}
	}
	return b.String()
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
