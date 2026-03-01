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

	if opts.RepoPath != "" {
		repo, err := git.PlainOpenWithOptions(opts.RepoPath, &git.PlainOpenOptions{
			DetectDotGit: true,
		})
		if err == nil {
			actor = readGitActor(repo, actor)
			repository, repositoryOwner = readGitRepository(repo, repository, repositoryOwner)
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
		"server_url":        expression.String("https://github.com"),
		"api_url":           expression.String("https://api.github.com"),
		"graphql_url":       expression.String("https://api.github.com/graphql"),
		"event":             expression.Object(map[string]expression.Value{}),
		"token":             expression.String("ions-dummy-token"),
		"job":               expression.String(""),
		"action_path":       expression.String(""),
		"action_ref":        expression.String(""),
		"action_repository": expression.String(""),
	}

	return expression.Object(fields)
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
