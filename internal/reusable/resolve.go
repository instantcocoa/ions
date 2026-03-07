package reusable

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emaland/ions/internal/workflow"
)

// Reference represents a parsed reusable workflow reference.
type Reference struct {
	// Local is true when the reference starts with "./"
	Local bool
	// Path is the file path (relative for local, or the path within the repo for remote).
	Path string
	// Owner/Repo for remote references.
	Owner string
	Repo  string
	// Ref is the git ref (branch, tag, SHA) for remote references.
	Ref string
}

// ParseReference parses a reusable workflow `uses:` string.
//
// Formats:
//   - "./path/to/workflow.yml" → local workflow
//   - "owner/repo/.github/workflows/file.yml@ref" → remote workflow
func ParseReference(uses string) (Reference, error) {
	if strings.HasPrefix(uses, "./") {
		return Reference{
			Local: true,
			Path:  uses[2:], // strip "./"
		}, nil
	}

	// Remote: owner/repo/path/to/file.yml@ref
	atIdx := strings.LastIndex(uses, "@")
	if atIdx < 0 {
		return Reference{}, fmt.Errorf("invalid reusable workflow reference %q: missing @ref", uses)
	}

	nameWithPath := uses[:atIdx]
	ref := uses[atIdx+1:]

	parts := strings.SplitN(nameWithPath, "/", 3)
	if len(parts) < 3 {
		return Reference{}, fmt.Errorf("invalid reusable workflow reference %q: expected owner/repo/path@ref", uses)
	}

	return Reference{
		Owner: parts[0],
		Repo:  parts[1],
		Path:  parts[2],
		Ref:   ref,
	}, nil
}

// ResolverOptions configures the workflow resolver.
type ResolverOptions struct {
	// RepoPath is the local repo root for resolving local references.
	RepoPath string
	// GitHubToken for downloading remote workflows.
	GitHubToken string
	// APIBaseURL overrides the GitHub API URL (e.g., for the local stub).
	APIBaseURL string
}

// Resolver loads reusable workflow files.
type Resolver struct {
	opts       ResolverOptions
	httpClient *http.Client
}

// NewResolver creates a new Resolver.
func NewResolver(opts ResolverOptions) *Resolver {
	return &Resolver{
		opts: opts,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Resolve loads and parses a reusable workflow from a reference.
func (r *Resolver) Resolve(ctx context.Context, ref Reference) (*workflow.Workflow, error) {
	if ref.Local {
		return r.resolveLocal(ref)
	}
	return r.resolveRemote(ctx, ref)
}

// resolveLocal loads a workflow from the local filesystem.
func (r *Resolver) resolveLocal(ref Reference) (*workflow.Workflow, error) {
	path := filepath.Join(r.opts.RepoPath, ref.Path)

	// Prevent path traversal.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	absRepo, err := filepath.Abs(r.opts.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("invalid repo path: %w", err)
	}
	if !strings.HasPrefix(absPath, absRepo+string(filepath.Separator)) && absPath != absRepo {
		return nil, fmt.Errorf("reusable workflow path %q escapes repository root", ref.Path)
	}

	return workflow.ParseFile(path)
}

// resolveRemote downloads a workflow file from GitHub.
func (r *Resolver) resolveRemote(ctx context.Context, ref Reference) (*workflow.Workflow, error) {
	// Try the GitHub raw content URL first (no API needed).
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		ref.Owner, ref.Repo, ref.Ref, ref.Path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if r.opts.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.opts.GitHubToken)
	}
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	req.Header.Set("User-Agent", "ions/1.0")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download reusable workflow %s/%s@%s: %w",
			ref.Owner, ref.Repo, ref.Ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download reusable workflow %s/%s/%s@%s: HTTP %d",
			ref.Owner, ref.Repo, ref.Path, ref.Ref, resp.StatusCode)
	}

	// Read into a temp file and parse.
	tmpFile, err := os.CreateTemp("", "ions-reusable-*.yml")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	return workflow.ParseFile(tmpFile.Name())
}

// MapInputs maps the caller's `with:` values to the called workflow's `inputs:`.
// Returns the inputs as a string map suitable for the expression context.
func MapInputs(callerWith map[string]interface{}, calledTriggers workflow.Triggers) map[string]string {
	inputs := make(map[string]string)

	// Extract input definitions from the called workflow's workflow_call trigger.
	inputDefs := calledTriggers.WorkflowCallInputs()

	// Map caller's with: values, applying defaults from the called workflow.
	for name, def := range inputDefs {
		if val, ok := callerWith[name]; ok {
			inputs[name] = fmt.Sprintf("%v", val)
		} else if def.Default != "" {
			inputs[name] = def.Default
		}
	}

	// Also include any with: values that don't have a matching input def
	// (the called workflow may still reference them).
	for name, val := range callerWith {
		if _, ok := inputs[name]; !ok {
			inputs[name] = fmt.Sprintf("%v", val)
		}
	}

	return inputs
}

// MapSecrets resolves the secrets for a reusable workflow call.
// When secrets is "inherit", all caller secrets are passed through.
// When secrets is a map, only the specified secrets are mapped.
func MapSecrets(callerSecrets map[string]string, jobSecrets interface{}) map[string]string {
	if jobSecrets == nil {
		return map[string]string{}
	}

	// "inherit" — pass all caller secrets.
	if s, ok := jobSecrets.(string); ok && s == "inherit" {
		result := make(map[string]string, len(callerSecrets))
		for k, v := range callerSecrets {
			result[k] = v
		}
		return result
	}

	// Explicit mapping: secrets: { DEPLOY_KEY: ${{ secrets.DEPLOY_KEY }} }
	// In parsed YAML this is map[string]interface{}.
	if m, ok := jobSecrets.(map[string]interface{}); ok {
		result := make(map[string]string, len(m))
		for name, val := range m {
			valStr := fmt.Sprintf("%v", val)
			// If the value is a ${{ secrets.X }} expression, resolve it.
			if strings.HasPrefix(valStr, "${{") && strings.HasSuffix(valStr, "}}") {
				inner := strings.TrimSpace(valStr[3 : len(valStr)-2])
				if strings.HasPrefix(inner, "secrets.") {
					key := strings.TrimPrefix(inner, "secrets.")
					if v, ok := callerSecrets[key]; ok {
						result[name] = v
						continue
					}
				}
			}
			result[name] = valStr
		}
		return result
	}

	return map[string]string{}
}
