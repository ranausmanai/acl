// Package registry implements the ACL service registry client.
//
// Commands:
//
//	acl publish <file.acl>           Prepare a publishable package in dist/
//	acl search  <query>              Search the public registry
//	acl install <owner/name>         Download and install a service
//
// The registry index is a JSON file hosted at IndexURL.  Publishing is a two-
// step process: generate the package locally, then submit a PR to the ACL
// registry repo (or self-host your own index).
package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ranausmanai/acl/internal/schema"
)

// IndexURL is the public registry index.  Override with ACL_REGISTRY env var.
const IndexURL = "https://raw.githubusercontent.com/ranausmanai/acl/main/registry/index.json"

var httpClient = &http.Client{Timeout: 15 * time.Second}

// ── Index types ───────────────────────────────────────────────────────────────

// Index is the root of the registry index JSON.
type Index struct {
	Version   string     `json:"version"`
	UpdatedAt string     `json:"updated_at"`
	Services  []*Service `json:"services"`
}

// Service is one entry in the registry.
type Service struct {
	Name        string   `json:"name"`
	Owner       string   `json:"owner"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Repo        string   `json:"repo"`
	ACLPath     string   `json:"acl_path"`
	SchemaURL   string   `json:"schema_url"`
	Stars       int      `json:"stars"`
	PublishedAt string   `json:"published_at"`
}

// FullName returns "owner/name".
func (s *Service) FullName() string { return s.Owner + "/" + s.Name }

// ACLURL returns the raw GitHub URL for the .acl file.
func (s *Service) ACLURL() string {
	// Convert github.com/owner/repo → raw.githubusercontent.com/owner/repo/main/<path>
	repo := strings.TrimPrefix(s.Repo, "https://github.com/")
	return "https://raw.githubusercontent.com/" + repo + "/main/" + s.ACLPath
}

// ── Publish ───────────────────────────────────────────────────────────────────

// PublishResult describes the files written by Publish.
type PublishResult struct {
	Dir        string
	SchemaFile string
	MetaFile   string
	ACLFile    string
}

// Publish reads aclFile, generates a dist/ package, and prints submission instructions.
// destDir is the output directory (default: "dist").
func Publish(aclFile, destDir string) (*PublishResult, error) {
	if destDir == "" {
		destDir = "dist"
	}

	src, err := os.ReadFile(aclFile)
	if err != nil {
		return nil, fmt.Errorf("publish: read %s: %w", aclFile, err)
	}

	svc, err := schema.FromSource(string(src))
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("publish: mkdir %s: %w", destDir, err)
	}

	// Derive service name from intent or file name.
	name := serviceNameFrom(svc.Intent, aclFile)

	// 1. Copy the ACL file.
	aclDest := filepath.Join(destDir, name+".acl")
	if err := os.WriteFile(aclDest, src, 0o644); err != nil {
		return nil, fmt.Errorf("publish: write acl: %w", err)
	}

	// 2. Write schema.json.
	schemaBytes, err := json.MarshalIndent(svc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("publish: marshal schema: %w", err)
	}
	schemaDest := filepath.Join(destDir, "schema.json")
	if err := os.WriteFile(schemaDest, schemaBytes, 0o644); err != nil {
		return nil, fmt.Errorf("publish: write schema: %w", err)
	}

	// 3. Write acl-service.json (registry metadata).
	meta := map[string]any{
		"name":         name,
		"description":  svc.Intent,
		"tags":         []string{},
		"acl_version":  svc.ACLVersion,
		"agents":       len(svc.Agents),
		"published_at": time.Now().UTC().Format(time.RFC3339),
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	metaDest := filepath.Join(destDir, "acl-service.json")
	if err := os.WriteFile(metaDest, metaBytes, 0o644); err != nil {
		return nil, fmt.Errorf("publish: write meta: %w", err)
	}

	return &PublishResult{
		Dir:        destDir,
		SchemaFile: schemaDest,
		MetaFile:   metaDest,
		ACLFile:    aclDest,
	}, nil
}

// ── Search ────────────────────────────────────────────────────────────────────

// Search fetches the registry index and returns services matching query.
// query is matched against name, description, and tags (case-insensitive).
func Search(query string) ([]*Service, error) {
	idx, err := fetchIndex()
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return idx.Services, nil
	}

	var results []*Service
	for _, svc := range idx.Services {
		if matchesQuery(svc, q) {
			results = append(results, svc)
		}
	}
	return results, nil
}

func matchesQuery(svc *Service, q string) bool {
	if strings.Contains(strings.ToLower(svc.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(svc.Description), q) {
		return true
	}
	if strings.Contains(strings.ToLower(svc.Owner), q) {
		return true
	}
	for _, tag := range svc.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// ── Install ───────────────────────────────────────────────────────────────────

// InstallResult describes a successful install.
type InstallResult struct {
	Name    string
	ACLFile string // path where the .acl file was written
}

// Install downloads a service from the registry and writes it to destDir.
// ref may be "owner/name" or just "name" (resolved from index).
func Install(ref, destDir string) (*InstallResult, error) {
	if destDir == "" {
		destDir = "."
	}

	idx, err := fetchIndex()
	if err != nil {
		return nil, err
	}

	svc := resolveRef(idx, ref)
	if svc == nil {
		return nil, fmt.Errorf("install: service %q not found in registry\n"+
			"  Run 'acl search %s' to check available services", ref, ref)
	}

	aclURL := svc.ACLURL()
	body, err := fetchURL(aclURL)
	if err != nil {
		return nil, fmt.Errorf("install: fetch %s: %w", aclURL, err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("install: mkdir: %w", err)
	}

	outPath := filepath.Join(destDir, svc.Name+".acl")
	if err := os.WriteFile(outPath, body, 0o644); err != nil {
		return nil, fmt.Errorf("install: write %s: %w", outPath, err)
	}

	return &InstallResult{Name: svc.FullName(), ACLFile: outPath}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fetchIndex() (*Index, error) {
	src := os.Getenv("ACL_REGISTRY")
	if src == "" {
		src = IndexURL
	}

	var body []byte
	var err error
	// Allow ACL_REGISTRY to be a local file path for development / self-hosting.
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		body, err = os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("registry: read local index %s: %w", src, err)
		}
	} else {
		body, err = fetchURL(src)
		if err != nil {
			return nil, fmt.Errorf("registry: fetch index: %w", err)
		}
	}

	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("registry: parse index: %w", err)
	}
	return &idx, nil
}

func fetchURL(rawURL string) ([]byte, error) {
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(resp.Body)
}

func resolveRef(idx *Index, ref string) *Service {
	ref = strings.ToLower(strings.TrimSpace(ref))
	for _, svc := range idx.Services {
		if strings.ToLower(svc.FullName()) == ref || strings.ToLower(svc.Name) == ref {
			return svc
		}
	}
	return nil
}

func serviceNameFrom(intent, aclFile string) string {
	if intent != "" {
		// Take first 3 words, lowercase, join with hyphens.
		words := strings.Fields(intent)
		if len(words) > 3 {
			words = words[:3]
		}
		var parts []string
		for _, w := range words {
			w = strings.ToLower(w)
			w = strings.Trim(w, ".,;:!?\"'")
			if w != "" && w != "—" && w != "-" {
				parts = append(parts, w)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "-")
		}
	}
	base := filepath.Base(aclFile)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
