package context

import (
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"

	"github.com/emaland/ions/internal/expression"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// GitHubContext builds the "github" context object.
// It reads git repo info from the local .git/ directory using go-git.
func GitHubContext(opts BuilderOptions) expression.Value {
	actor := "local-actor"
	repository := "local/repo"
	repositoryOwner := "local"
	ref := ""
	sha := ""
	refName := ""
	hasRemote := false

	if opts.RepoPath != "" {
		repo, err := git.PlainOpenWithOptions(opts.RepoPath, &git.PlainOpenOptions{
			DetectDotGit: true,
		})
		if err == nil {
			actor = readGitActor(repo, actor)
			prevRepo := repository
			repository, repositoryOwner = readGitRepository(repo, repository, repositoryOwner)
			hasRemote = repository != prevRepo
			ref, sha, refName = readGitRef(repo)
		}
	}

	// Default ref to refs/heads/main when the repo has no commits yet.
	// Many actions (e.g. actions/cache) validate that github.ref is set
	// and skip execution if it's empty.
	if ref == "" {
		ref = "refs/heads/main"
		refName = "main"
	}

	eventName := opts.EventName
	if eventName == "" {
		eventName = "push"
	}

	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("%d", rand.Int63n(9000000000)+1000000000)
	}

	runNumber := opts.RunNumber
	if runNumber == 0 {
		runNumber = 1
	}

	workspace, _ := os.Getwd()

	// When there's no git remote, point server_url at the broker so
	// that actions/checkout hits our local stub instead of trying to
	// clone from https://github.com/local/repo (which doesn't exist).
	// The workspace is pre-populated with repo files, so checkout
	// failures are non-fatal for most workflows.
	serverURL := "https://github.com"
	if !hasRemote && opts.APIBaseURL != "" {
		// Strip /api/v3 suffix to get the base broker URL.
		serverURL = strings.TrimSuffix(opts.APIBaseURL, "/api/v3")
	}

	var event expression.Value
	if opts.EventPayload != nil {
		event = toExpressionValue(opts.EventPayload)
	} else {
		event = buildEventPayload(eventName, opts.Inputs, repository, repositoryOwner, ref, sha, actor, serverURL)
	}

	fields := map[string]expression.Value{
		"event_name":        expression.String(eventName),
		"workflow":          expression.String(opts.WorkflowName),
		"run_id":            expression.String(runID),
		"run_number":        expression.String(fmt.Sprintf("%d", runNumber)),
		"actor":             expression.String(actor),
		"repository":        expression.String(repository),
		"repository_owner":  expression.String(repositoryOwner),
		"ref":               expression.String(ref),
		"sha":               expression.String(sha),
		"head_ref":          expression.String(ref),
		"base_ref":          expression.String(""),
		"ref_name":          expression.String(refName),
		"workspace":         expression.String(workspace),
		"action":            expression.String(""),
		"server_url":        expression.String(serverURL),
		"api_url":           expression.String(apiURL(opts)),
		"graphql_url":       expression.String(graphqlURL(opts)),
		"event":             event,
		"token":             expression.String(tokenValue(opts)),
		"job":               expression.String(""),
		"action_path":       expression.String(""),
		"action_ref":        expression.String(""),
		"action_repository": expression.String(""),
		"environment":       expression.String(opts.EnvironmentName),
	}

	return expression.Object(fields)
}

// buildEventPayload generates a realistic event payload based on the event type.
// This populates github.event.* so workflows can reference event-specific fields.
func buildEventPayload(eventName string, inputs map[string]string,
	repository, repositoryOwner, ref, sha, actor, serverURL string) expression.Value {

	repoObj := expression.Object(map[string]expression.Value{
		"full_name":      expression.String(repository),
		"name":           expression.String(repoName(repository)),
		"owner":          expression.Object(map[string]expression.Value{"login": expression.String(repositoryOwner)}),
		"default_branch": expression.String("main"),
		"html_url":       expression.String(serverURL + "/" + repository),
		"clone_url":      expression.String(serverURL + "/" + repository + ".git"),
	})

	senderObj := expression.Object(map[string]expression.Value{
		"login": expression.String(actor),
		"type":  expression.String("User"),
	})

	switch eventName {
	case "push":
		return expression.Object(map[string]expression.Value{
			"ref":        expression.String(ref),
			"before":     expression.String(strings.Repeat("0", 40)),
			"after":      expression.String(sha),
			"repository": repoObj,
			"sender":     senderObj,
			"head_commit": expression.Object(map[string]expression.Value{
				"id":      expression.String(sha),
				"message": expression.String("local commit"),
				"author":  expression.Object(map[string]expression.Value{"name": expression.String(actor)}),
			}),
		})

	case "pull_request", "pull_request_target":
		return expression.Object(map[string]expression.Value{
			"action": expression.String("opened"),
			"number": expression.Number(1),
			"pull_request": expression.Object(map[string]expression.Value{
				"number": expression.Number(1),
				"title":  expression.String("Local PR"),
				"state":  expression.String("open"),
				"merged": expression.Bool(false),
				"draft":  expression.Bool(false),
				"head": expression.Object(map[string]expression.Value{
					"ref": expression.String(ref),
					"sha": expression.String(sha),
				}),
				"base": expression.Object(map[string]expression.Value{
					"ref": expression.String("main"),
					"sha": expression.String(strings.Repeat("0", 40)),
				}),
				"user": senderObj,
			}),
			"repository": repoObj,
			"sender":     senderObj,
		})

	case "workflow_dispatch":
		inputFields := make(map[string]expression.Value, len(inputs))
		for k, v := range inputs {
			inputFields[k] = expression.String(v)
		}
		return expression.Object(map[string]expression.Value{
			"inputs":     expression.Object(inputFields),
			"ref":        expression.String(ref),
			"repository": repoObj,
			"sender":     senderObj,
		})

	case "schedule":
		return expression.Object(map[string]expression.Value{
			"schedule":   expression.String(""),
			"repository": repoObj,
			"sender":     senderObj,
		})

	case "workflow_call":
		inputFields := make(map[string]expression.Value, len(inputs))
		for k, v := range inputs {
			inputFields[k] = expression.String(v)
		}
		return expression.Object(map[string]expression.Value{
			"inputs": expression.Object(inputFields),
		})

	default:
		// Generic event with common fields.
		return expression.Object(map[string]expression.Value{
			"repository": repoObj,
			"sender":     senderObj,
		})
	}
}

// repoName extracts the repo name from "owner/repo".
func repoName(fullName string) string {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return fullName
}

// readGitActor tries to read the user.name from git config.
func readGitActor(repo *git.Repository, fallback string) string {
	cfg, err := repo.Config()
	if err != nil {
		return fallback
	}
	if cfg.User.Name != "" {
		return cfg.User.Name
	}
	return fallback
}

// readGitRepository tries to derive owner/repo from the "origin" remote URL.
func readGitRepository(repo *git.Repository, fallbackRepo, fallbackOwner string) (string, string) {
	remote, err := repo.Remote("origin")
	if err != nil || remote == nil {
		return fallbackRepo, fallbackOwner
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return fallbackRepo, fallbackOwner
	}

	ownerRepo := parseRemoteURL(urls[0])
	if ownerRepo == "" {
		return fallbackRepo, fallbackOwner
	}

	parts := strings.SplitN(ownerRepo, "/", 2)
	if len(parts) != 2 {
		return fallbackRepo, fallbackOwner
	}
	return ownerRepo, parts[0]
}

// httpsPattern matches HTTPS remote URLs like https://github.com/owner/repo.git
var httpsPattern = regexp.MustCompile(`https?://[^/]+/([^/]+/[^/]+?)(?:\.git)?$`)

// sshPattern matches SSH remote URLs like git@github.com:owner/repo.git
var sshPattern = regexp.MustCompile(`[^@]+@[^:]+:([^/]+/[^/]+?)(?:\.git)?$`)

// parseRemoteURL extracts "owner/repo" from a git remote URL.
// Handles both HTTPS and SSH formats.
func parseRemoteURL(url string) string {
	if m := httpsPattern.FindStringSubmatch(url); len(m) > 1 {
		return m[1]
	}
	if m := sshPattern.FindStringSubmatch(url); len(m) > 1 {
		return m[1]
	}
	return ""
}

// readGitRef reads the current HEAD ref and SHA.
func readGitRef(repo *git.Repository) (ref, sha, refName string) {
	headRef, err := repo.Head()
	if err != nil {
		return "", "", ""
	}

	sha = headRef.Hash().String()

	if headRef.Name().IsBranch() {
		ref = string(headRef.Name())
		refName = headRef.Name().Short()
	} else if headRef.Name().IsTag() {
		ref = string(headRef.Name())
		refName = headRef.Name().Short()
	} else if headRef.Name() == plumbing.HEAD {
		// Detached HEAD
		ref = sha
		refName = sha
	} else {
		ref = string(headRef.Name())
		refName = headRef.Name().Short()
	}

	return ref, sha, refName
}

// apiURL returns the GitHub API URL to use in the context.
func apiURL(opts BuilderOptions) string {
	if opts.APIBaseURL != "" {
		return opts.APIBaseURL
	}
	return "https://api.github.com"
}

// graphqlURL returns the GraphQL URL to use in the context.
func graphqlURL(opts BuilderOptions) string {
	if opts.APIBaseURL != "" {
		return strings.TrimRight(opts.APIBaseURL, "/") + "/graphql"
	}
	return "https://api.github.com/graphql"
}

// tokenValue returns the GitHub token to use in the context.
func tokenValue(opts BuilderOptions) string {
	if opts.GitHubToken != "" {
		return opts.GitHubToken
	}
	return "ions-dummy-token"
}
