// Package packs provides built-in ACL packs (templates + manifests) that can be
// installed without writing Go wrappers.
package packs

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ranausmanai/acl/internal/checker"
	"github.com/ranausmanai/acl/internal/lexer"
	"github.com/ranausmanai/acl/internal/parser"
	"github.com/ranausmanai/acl/tools/builtin"
)

//go:embed data/**
var builtins embed.FS

type Index struct {
	Version string     `json:"version"`
	Packs   []PackMeta `json:"packs"`
}

type PackMeta struct {
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Version      string         `json:"version"`
	Description  string         `json:"description"`
	Docs         string         `json:"docs,omitempty"`
	ToolManifest string         `json:"tool_manifest,omitempty"`
	Templates    []TemplateMeta `json:"templates,omitempty"`
	Env          []EnvVarMeta   `json:"env,omitempty"`
}

type TemplateMeta struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	Description string `json:"description"`
}

type EnvVarMeta struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type InstallResult struct {
	Name   string
	Path   string
	Files  int
	Source string
}

type ValidateResult struct {
	PackPath        string
	TemplateCount   int
	TemplatesPassed int
}

func List() ([]PackMeta, error) {
	idx, err := loadIndex()
	if err != nil {
		return nil, err
	}
	packs := append([]PackMeta(nil), idx.Packs...)
	sort.Slice(packs, func(i, j int) bool { return packs[i].Name < packs[j].Name })
	return packs, nil
}

func Info(name string) (*PackMeta, error) {
	idx, err := loadIndex()
	if err != nil {
		return nil, err
	}
	for _, p := range idx.Packs {
		if p.Name == name {
			cp := p
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("pack %q not found", name)
}

// Install installs a built-in pack by name into outputDir. If outputDir is empty,
// installs to ./acl-packs/<name>.
func Install(name, outputDir string) (*InstallResult, error) {
	meta, err := Info(name)
	if err != nil {
		return nil, err
	}
	root := "data/" + name
	sub, err := fs.Sub(builtins, root)
	if err != nil {
		return nil, fmt.Errorf("pack %q assets missing: %w", name, err)
	}
	if outputDir == "" {
		outputDir = filepath.Join(".", "acl-packs", name)
	} else {
		outputDir = filepath.Join(outputDir, name)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("install pack: mkdir %s: %w", outputDir, err)
	}
	files, err := copyFS(sub, ".", outputDir)
	if err != nil {
		return nil, err
	}
	return &InstallResult{
		Name:   meta.Name,
		Path:   outputDir,
		Files:  files,
		Source: "builtin",
	}, nil
}

// ReadFile reads a file from a built-in pack by relative path (e.g. templates/x.acl).
func ReadFile(packName, relPath string) ([]byte, error) {
	return builtins.ReadFile(filepath.ToSlash(filepath.Join("data", packName, relPath)))
}

// Init scaffolds a local pack directory for authors.
func Init(name, outputDir string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("pack name required")
	}
	if outputDir == "" {
		outputDir = filepath.Join(".", name)
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "templates"), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "samples"), 0o755); err != nil {
		return "", err
	}
	packJSON := fmt.Sprintf(`{
  "name": %q,
  "title": %q,
  "version": "0.1.0",
  "description": "ACL pack scaffold",
  "docs": "README.md",
  "tool_manifest": "tool-manifest.json",
  "templates": [
    { "name": "example", "file": "templates/example.acl", "description": "Starter template" }
  ]
}
`, name, strings.Title(strings.ReplaceAll(name, "-", " "))+" Pack")
	manifestJSON := `{
  "version": "1",
  "provider": "example",
  "tools": [
    {
      "name": "example.read",
      "transport": "http",
      "method": "GET",
      "path": "/example",
      "side_effect": "read",
      "approval_required": false
    }
  ]
}
`
	templateACL := `INTENT "Pack scaffold template"
ALLOW llm.generate

AGENT ExamplePackTemplate
  OUT answer
  TOOLS llm.generate

  STEP answer = TOOL llm.generate(prompt="This is a starter ACL pack template. Replace with your workflow.")
  MUST has(answer, "text")
  RESULT answer
END
`
	sampleVars := `{
  "api_key": "YOUR_API_KEY",
  "base_url": "https://api.example.com",
  "request_json": "{\"example\":true}"
}
`
	readme := "# ACL Pack Scaffold\n\nFill in `tool-manifest.json`, `templates/*.acl`, and sample vars files.\n\nValidate with `acl pack validate .`\n"
	files := map[string]string{
		"pack.json":                packJSON,
		"tool-manifest.json":       manifestJSON,
		"templates/example.acl":    templateACL,
		"samples/vars.sample.json": sampleVars,
		"README.md":                readme,
	}
	for rel, content := range files {
		p := filepath.Join(outputDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return outputDir, nil
}

// Validate checks pack metadata, manifest existence, and template syntax/semantics.
func Validate(packPath string) (*ValidateResult, error) {
	if packPath == "" {
		packPath = "."
	}
	data, err := os.ReadFile(filepath.Join(packPath, "pack.json"))
	if err != nil {
		return nil, fmt.Errorf("read pack.json: %w", err)
	}
	var meta PackMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse pack.json: %w", err)
	}
	if meta.Name == "" {
		return nil, fmt.Errorf("pack.json: name is required")
	}
	if meta.ToolManifest != "" {
		if _, err := os.Stat(filepath.Join(packPath, meta.ToolManifest)); err != nil {
			return nil, fmt.Errorf("manifest file missing: %s", meta.ToolManifest)
		}
	}
	reg := builtin.NewRegistry()
	out := &ValidateResult{PackPath: packPath}
	for _, tpl := range meta.Templates {
		out.TemplateCount++
		src, err := os.ReadFile(filepath.Join(packPath, tpl.File))
		if err != nil {
			return nil, fmt.Errorf("read template %s: %w", tpl.File, err)
		}
		toks, err := lexer.New(string(src)).Tokenize()
		if err != nil {
			return nil, fmt.Errorf("template %s lex: %w", tpl.File, err)
		}
		nodes, err := parser.Parse(toks)
		if err != nil {
			return nil, fmt.Errorf("template %s parse: %w", tpl.File, err)
		}
		if errs := checker.Check(nodes, reg.Names()); len(errs) > 0 {
			return nil, fmt.Errorf("template %s check: %v", tpl.File, errs[0])
		}
		out.TemplatesPassed++
	}
	return out, nil
}

func loadIndex() (*Index, error) {
	data, err := builtins.ReadFile("data/index.json")
	if err != nil {
		return nil, fmt.Errorf("packs: read index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("packs: parse index: %w", err)
	}
	return &idx, nil
}

func copyFS(src fs.FS, root, dst string) (int, error) {
	count := 0
	err := fs.WalkDir(src, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		outPath := filepath.Join(dst, path)
		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("install pack: %w", err)
	}
	return count, nil
}

func FormatInfo(meta *PackMeta) string {
	if meta == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pack     %s\n", meta.Name)
	if meta.Title != "" {
		fmt.Fprintf(&b, "title    %s\n", meta.Title)
	}
	if meta.Version != "" {
		fmt.Fprintf(&b, "version  %s\n", meta.Version)
	}
	if meta.Description != "" {
		fmt.Fprintf(&b, "about    %s\n", meta.Description)
	}
	if meta.ToolManifest != "" {
		fmt.Fprintf(&b, "manifest %s\n", meta.ToolManifest)
	}
	if len(meta.Env) > 0 {
		b.WriteString("env\n")
		for _, e := range meta.Env {
			req := "optional"
			if e.Required {
				req = "required"
			}
			fmt.Fprintf(&b, "  - %s (%s): %s\n", e.Name, req, e.Description)
		}
	}
	if len(meta.Templates) > 0 {
		b.WriteString("templates\n")
		for _, t := range meta.Templates {
			fmt.Fprintf(&b, "  - %s (%s): %s\n", t.Name, t.File, t.Description)
		}
	}
	return b.String()
}
