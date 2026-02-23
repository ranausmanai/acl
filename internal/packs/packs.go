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
