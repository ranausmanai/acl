package adaptergen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ranausmanai/acl/internal/manifest"
)

type OpenAPISpec struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Servers []struct {
		URL string `json:"url"`
	} `json:"servers"`
	Paths map[string]map[string]map[string]any `json:"paths"`
}

type GenerateResult struct {
	OutputDir string
	PackName  string
	Files     []string
}

func GeneratePackFromOpenAPI(specPath, outputDir string) (*GenerateResult, error) {
	if strings.HasSuffix(strings.ToLower(specPath), ".yaml") || strings.HasSuffix(strings.ToLower(specPath), ".yml") {
		return nil, fmt.Errorf("YAML OpenAPI input is not supported in this MVP yet; use JSON")
	}
	b, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	var spec OpenAPISpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("parse OpenAPI JSON: %w", err)
	}
	packName := slug(firstNonEmpty(spec.Info.Title, filepath.Base(specPath)))
	if packName == "" {
		packName = "api-pack"
	}
	if outputDir == "" {
		outputDir = filepath.Join(".", packName+"-acl-pack")
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "templates"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "samples"), 0o755); err != nil {
		return nil, err
	}

	baseURL := ""
	if len(spec.Servers) > 0 {
		baseURL = spec.Servers[0].URL
	}
	mf := manifest.File{Version: "1", Provider: packName, BaseURL: baseURL, Tools: []manifest.ToolEntry{}}
	if baseURL != "" {
		mf.Auth = manifest.Auth{Type: "bearer", Env: strings.ToUpper(strings.ReplaceAll(packName, "-", "_")) + "_API_KEY"}
	}

	type tpl struct{ Name, File, Desc string }
	var templates []tpl
	var paths []string
	for p := range spec.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ops := spec.Paths[p]
		methods := make([]string, 0, len(ops))
		for m := range ops {
			methods = append(methods, strings.ToUpper(m))
		}
		sort.Strings(methods)
		for _, method := range methods {
			op := ops[strings.ToLower(method)]
			if op == nil {
				op = ops[method]
			}
			opID, _ := op["operationId"].(string)
			if opID == "" {
				opID = inferOperationID(method, p)
			}
			side := "read"
			if method != "GET" && method != "HEAD" {
				side = "write"
			}
			toolName := fmt.Sprintf("%s.%s", packName, strings.ReplaceAll(opID, "_", "."))
			mf.Tools = append(mf.Tools, manifest.ToolEntry{
				Name: toolName, Transport: "http", Method: method, Path: p, SideEffect: side,
				ApprovalRequired: side == "write",
				Input:            map[string]any{"type": "object", "description": "Generated from OpenAPI; refine fields"},
				Output:           map[string]any{"type": "object", "description": "Generated from OpenAPI; refine response schema"},
			})
			tname := slug(opID)
			if tname == "" {
				tname = slug(method + "-" + p)
			}
			tf := filepath.Join("templates", tname+".acl")
			code := generateACLTemplate(packName, opID, method, p)
			if err := os.WriteFile(filepath.Join(outputDir, tf), []byte(code), 0o644); err != nil {
				return nil, err
			}
			templates = append(templates, tpl{Name: tname, File: tf, Desc: fmt.Sprintf("%s %s", method, p)})
		}
	}

	packJSON := map[string]any{
		"name":          packName,
		"title":         firstNonEmpty(spec.Info.Title, strings.ToUpper(packName)),
		"version":       firstNonEmpty(spec.Info.Version, "0.1.0"),
		"description":   "Generated ACL pack from OpenAPI JSON (MVP scaffold)",
		"docs":          "README.md",
		"tool_manifest": "tool-manifest.json",
	}
	var tpls []map[string]any
	for _, t := range templates {
		tpls = append(tpls, map[string]any{"name": t.Name, "file": t.File, "description": t.Desc})
	}
	packJSON["templates"] = tpls
	if mf.Auth.Env != "" {
		packJSON["env"] = []map[string]any{{"name": mf.Auth.Env, "required": true, "description": "API token for generated API"}}
	}

	if err := writeJSON(filepath.Join(outputDir, "tool-manifest.json"), mf); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(outputDir, "pack.json"), packJSON); err != nil {
		return nil, err
	}
	sampleVars := map[string]any{"base_url": baseURL, "api_key": "YOUR_API_KEY", "request_json": "{\"example\":true}"}
	if err := writeJSON(filepath.Join(outputDir, "samples", "vars.sample.json"), sampleVars); err != nil {
		return nil, err
	}
	readme := generateREADME(packName, specPath, mf.Auth.Env)
	if err := os.WriteFile(filepath.Join(outputDir, "README.md"), []byte(readme), 0o644); err != nil {
		return nil, err
	}

	return &GenerateResult{OutputDir: outputDir, PackName: packName, Files: []string{"pack.json", "tool-manifest.json", "templates/", "samples/vars.sample.json", "README.md"}}, nil
}

func generateACLTemplate(packName, opID, method, path string) string {
	agent := slug(opID)
	if agent == "" {
		agent = "api_op"
	}
	agent = strings.Title(strings.ReplaceAll(agent, "_", " "))
	agent = strings.ReplaceAll(agent, " ", "")
	lowerMethod := strings.ToUpper(method)
	bodyLine := ""
	if lowerMethod != "GET" && lowerMethod != "HEAD" {
		bodyLine = `,
    content_type="application/json",
    body=request_json`
	}
	commentLine := ""
	if lowerMethod != "GET" && lowerMethod != "HEAD" {
		commentLine = "  # Write-like endpoint: add preview/confirm before production use.\n"
	}
	return fmt.Sprintf(`INTENT "Generated OpenAPI template for %s %s"
ALLOW http.request, llm.generate

AGENT %s
  IN api_key
  IN base_url
  IN request_json
  OUT answer
  TOOLS http.request, llm.generate
%s

  STEP res = TOOL http.request(
    url="{base_url}%s",
    method="%s",
    bearer=api_key%s)
    CHECK res.status >= 200

  STEP answer = TOOL llm.generate(prompt="Summarize API response for %s %s in 1-3 sentences. Response: {res.text}")
  MUST has(answer, "text")
  RESULT answer
END
`, method, path, strings.ReplaceAll(agent, "-", ""), commentLine, path, method, bodyLine, method, path)
}

func generateREADME(packName, specPath, envVar string) string {
	return fmt.Sprintf("# %s ACL Pack (Generated)\n\nGenerated from OpenAPI JSON: %s\n\nThis is a starter pack scaffold. Review and refine:\n- tool-manifest.json schemas\n- templates/*.acl checks and preview/confirm behavior\n\n## Quick start\n\n1. Set %s\n2. Inspect templates in ./templates\n3. Run `acl check templates/<file>.acl`\n4. Run `acl run templates/<file>.acl --vars samples/vars.sample.json`\n", packName, specPath, envVar)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func inferOperationID(method, path string) string {
	p := strings.Trim(path, "/")
	p = strings.ReplaceAll(p, "/", "_")
	p = strings.ReplaceAll(p, "{", "")
	p = strings.ReplaceAll(p, "}", "")
	if p == "" {
		p = "root"
	}
	return strings.ToLower(method) + "_" + p
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
