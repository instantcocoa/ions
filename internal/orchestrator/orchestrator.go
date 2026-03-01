package orchestrator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/emaland/ions/internal/artifacts"
	"github.com/emaland/ions/internal/broker"
	"github.com/emaland/ions/internal/cache"
	ionsctx "github.com/emaland/ions/internal/context"
	"github.com/emaland/ions/internal/docker"
	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/runner"
	"github.com/emaland/ions/internal/workflow"
)

// Options configures an orchestrator run.
type Options struct {
	WorkflowPath    string
	JobFilter       string
	EventName       string
	Secrets         map[string]string
	Vars            map[string]string
	Env             map[string]string
	Inputs          map[string]string
	DryRun          bool
	Verbose         bool
	RepoPath        string
	ArtifactDir     string
	ReuseContainers bool
	Platform        string
}

// RunResult captures the outcome of the full orchestrated run.
type RunResult struct {
	Success    bool
	JobResults map[string]*JobRunResult
	Duration   time.Duration
}

// Orchestrator coordinates the execution of a workflow.
type Orchestrator struct {
	opts      Options
	broker    *broker.Server
	runnerMgr *runner.Manager
	logger    *LogStreamer
	masker    *SecretMasker
}

// New creates a new Orchestrator.
func New(opts Options) (*Orchestrator, error) {
	if opts.RepoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		opts.RepoPath = cwd
	}

	masker := NewSecretMasker(opts.Secrets)
	logger := NewLogStreamer(masker, opts.Verbose)

	var mgr *runner.Manager
	if !opts.DryRun {
		var err error
		mgr, err = runner.NewManager()
		if err != nil {
			return nil, fmt.Errorf("runner manager: %w", err)
		}
	}

	return &Orchestrator{
		opts:      opts,
		runnerMgr: mgr,
		logger:    logger,
		masker:    masker,
	}, nil
}

// Run executes the workflow.
func (o *Orchestrator) Run(ctx context.Context) (*RunResult, error) {
	start := time.Now()

	// Parse workflow.
	w, err := workflow.ParseFile(o.opts.WorkflowPath)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	// Validate.
	if errs := workflow.Validate(w); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "validation error: %s\n", e)
		}
		return nil, fmt.Errorf("workflow has %d validation error(s)", len(errs))
	}

	// Build graph.
	g, err := graph.Build(w)
	if err != nil {
		return nil, fmt.Errorf("graph error: %w", err)
	}
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("graph error: %w", err)
	}

	// Build initial context for planning.
	runID := uuid.New().String()[:8]
	initialCtx := o.buildContext(w, nil, nil, nil, runID)

	// Create execution plan.
	plan, err := g.Plan(initialCtx)
	if err != nil {
		return nil, fmt.Errorf("plan error: %w", err)
	}

	// Filter to specific job if requested.
	if o.opts.JobFilter != "" {
		plan = filterPlan(plan, o.opts.JobFilter)
		if len(plan.Groups) == 0 {
			return nil, fmt.Errorf("no jobs match filter %q", o.opts.JobFilter)
		}
	}

	// Dry run — print plan and return.
	if o.opts.DryRun {
		return o.dryRun(plan, start)
	}

	// Ensure runner binary is installed.
	runnerDir, err := o.runnerMgr.EnsureInstalled(ctx)
	if err != nil {
		return nil, fmt.Errorf("runner install: %w", err)
	}

	// Build expression defaults for action.yml patching from the initial context.
	exprDefaults := buildExprDefaults(initialCtx)

	// Start broker server.
	brokerSrv, err := broker.NewServer(broker.ServerConfig{
		Verbose:      o.opts.Verbose,
		ExprDefaults: exprDefaults,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: %w", err)
	}
	o.broker = brokerSrv

	// Register cache and artifact routes on the broker.
	homeDir, _ := os.UserHomeDir()

	cacheDir := filepath.Join(homeDir, ".ions", "cache")
	cacheSrv, err := cache.NewServer(cacheDir, brokerSrv.URL())
	if err != nil {
		return nil, fmt.Errorf("cache server: %w", err)
	}
	brokerSrv.RegisterRoutes(cacheSrv)

	artifactDir := o.opts.ArtifactDir
	if artifactDir == "" {
		artifactDir = filepath.Join(homeDir, ".ions", "artifacts")
	}
	artifactSrv, err := artifacts.NewServer(artifactDir, brokerSrv.URL())
	if err != nil {
		return nil, fmt.Errorf("artifact server: %w", err)
	}
	brokerSrv.RegisterRoutes(artifactSrv)

	if err := brokerSrv.Start(ctx); err != nil {
		return nil, fmt.Errorf("broker start: %w", err)
	}
	defer brokerSrv.Stop(context.Background())

	// Execute plan group by group.
	allResults := make(map[string]*JobRunResult)
	jobOutputs := make(map[string]*ionsctx.JobResult) // for needs context

	success := true
	for _, group := range plan.Groups {
		groupResults := o.executeGroup(ctx, group, w, jobOutputs, brokerSrv, runnerDir, runID)

		for nodeID, result := range groupResults {
			allResults[nodeID] = result
			if result.Status == "failure" {
				success = false
			}
			// Store outputs for needs context of subsequent groups.
			jobOutputs[nodeID] = &ionsctx.JobResult{
				Result:  result.Status,
				Outputs: result.Outputs,
			}
		}
	}

	// Mark skipped jobs.
	for _, node := range plan.Skipped {
		allResults[node.NodeID] = &JobRunResult{
			NodeID: node.NodeID,
			Status: "skipped",
		}
	}

	o.logger.Summary(allResults)

	return &RunResult{
		Success:    success,
		JobResults: allResults,
		Duration:   time.Since(start),
	}, nil
}

// executeGroup runs all jobs in a parallel group concurrently.
// Jobs whose dependencies failed are skipped unless they have `if: always()`.
func (o *Orchestrator) executeGroup(
	ctx context.Context,
	group graph.ParallelGroup,
	w *workflow.Workflow,
	jobOutputs map[string]*ionsctx.JobResult,
	brokerSrv *broker.Server,
	runnerDir string,
	runID string,
) map[string]*JobRunResult {
	results := make(map[string]*JobRunResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, node := range group.Nodes {
		// Check if any dependency failed.
		depFailed := false
		for _, dep := range node.DependsOn {
			if jr, ok := jobOutputs[dep]; ok && jr.Result != "success" {
				depFailed = true
				break
			}
		}

		// Skip jobs whose dependencies failed, unless they have always() in their if: condition.
		if depFailed && !strings.Contains(node.Job.If, "always()") {
			o.logger.JobStarted(node.NodeID)
			o.logger.JobCompleted(node.NodeID, "skipped", 0)
			results[node.NodeID] = &JobRunResult{
				NodeID: node.NodeID,
				Status: "skipped",
			}
			continue
		}

		wg.Add(1)
		go func(node *graph.JobNode) {
			defer wg.Done()
			result := o.executeJob(ctx, node, w, jobOutputs, brokerSrv, runnerDir, runID)
			mu.Lock()
			results[node.NodeID] = result
			mu.Unlock()
		}(node)
	}

	wg.Wait()
	return results
}

// executeJob runs a single job via the broker/runner.
func (o *Orchestrator) executeJob(
	ctx context.Context,
	node *graph.JobNode,
	w *workflow.Workflow,
	jobOutputs map[string]*ionsctx.JobResult,
	brokerSrv *broker.Server,
	runnerDir string,
	runID string,
) *JobRunResult {
	start := time.Now()
	o.logger.JobStarted(node.NodeID)

	// Set up Docker service containers if the job defines any.
	var dockerEnv *docker.JobEnvironment
	if len(node.Job.Services) > 0 {
		dockerMgr, err := docker.NewManager(o.opts.ReuseContainers)
		if err != nil {
			o.logger.StepOutput(node.NodeID, fmt.Sprintf("docker: %v", err))
			o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
			return &JobRunResult{
				NodeID:   node.NodeID,
				Status:   "failure",
				Duration: time.Since(start),
			}
		}

		services := make(map[string]docker.ServiceConfig, len(node.Job.Services))
		for name, svc := range node.Job.Services {
			services[name] = docker.ServiceConfig{
				Image:   svc.Image,
				Env:     svc.Env,
				Ports:   svc.Ports,
				Volumes: svc.Volumes,
				Options: svc.Options,
			}
		}

		var err2 error
		dockerEnv, err2 = dockerMgr.SetupServices(ctx, sanitizePath(node.NodeID), services)
		if err2 != nil {
			o.logger.StepOutput(node.NodeID, fmt.Sprintf("docker services: %v", err2))
			o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
			return &JobRunResult{
				NodeID:   node.NodeID,
				Status:   "failure",
				Duration: time.Since(start),
			}
		}
		defer dockerMgr.Teardown(context.Background(), dockerEnv)

		// Log service container info.
		for name, svc := range dockerEnv.Services {
			for containerPort, hostPort := range svc.Ports {
				o.logger.StepOutput(node.NodeID, fmt.Sprintf("service %s (%s): %s -> localhost:%s", name, svc.Image, containerPort, hostPort))
			}
		}
	}

	// Build context for this job.
	jobCtx := o.buildContext(w, node, jobOutputs, nil, runID)

	// Build job message.
	msg, err := broker.BuildJobMessage(node, node.Job, jobCtx, brokerSrv.URL(), runID, o.opts.Secrets)
	if err != nil {
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{
			NodeID:   node.NodeID,
			Status:   "failure",
			Duration: time.Since(start),
		}
	}

	// Enqueue job.
	resultCh := brokerSrv.EnqueueJob(msg)

	// Configure and start runner process.
	// Sanitize the node ID for filesystem use — matrix node IDs contain
	// spaces and parentheses (e.g. "test (node: 20, os: ubuntu-latest)")
	// which break shell script execution (exit code 126).
	workDir := filepath.Join(o.opts.RepoPath, ".ions-work", sanitizePath(node.NodeID))
	os.MkdirAll(workDir, 0o755)

	// Pre-populate the runner workspace with local repo files so that
	// local action references (uses: ./path) resolve without needing
	// actions/checkout to clone from a remote. The runner expects code
	// at <workDir>/repo/repo/.
	wsDir := filepath.Join(workDir, "repo", "repo")
	if err := copyDir(o.opts.RepoPath, wsDir); err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("warning: failed to pre-populate workspace: %v", err))
	}

	proc, err := runner.NewProcess(runner.ProcessConfig{
		RunnerDir: runnerDir,
		BrokerURL: brokerSrv.URL(),
		Name:      "ions-" + node.NodeID,
		WorkDir:   workDir,
	})
	if err != nil {
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{
			NodeID:   node.NodeID,
			Status:   "failure",
			Duration: time.Since(start),
		}
	}

	// Configure runner.
	if err := proc.Configure(ctx); err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("runner configure error: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{
			NodeID:   node.NodeID,
			Status:   "failure",
			Duration: time.Since(start),
		}
	}

	// Start runner.
	if err := proc.Start(ctx); err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("runner start error: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{
			NodeID:   node.NodeID,
			Status:   "failure",
			Duration: time.Since(start),
		}
	}

	// Stream runner output.
	if proc.Stdout() != nil {
		go streamOutput(proc.Stdout(), node.NodeID, o.logger)
	}
	if proc.Stderr() != nil {
		go streamOutput(proc.Stderr(), node.NodeID, o.logger)
	}

	// Wait for result from broker or runner exit.
	var jobResult *broker.JobCompletionResult
	select {
	case jobResult = <-resultCh:
		// Job completed via broker protocol.
	case <-ctx.Done():
		proc.Stop()
		o.logger.JobCompleted(node.NodeID, "cancelled", time.Since(start))
		return &JobRunResult{
			NodeID:   node.NodeID,
			Status:   "cancelled",
			Duration: time.Since(start),
		}
	}

	// Stop the runner process — it won't exit on its own since we don't
	// use --ephemeral mode (we write config files directly).
	proc.Stop()

	status := "success"
	if jobResult.Result != "succeeded" {
		status = "failure"
	}

	// Extract outputs.
	outputs := make(map[string]string)
	if jobResult.Outputs != nil {
		for k, v := range jobResult.Outputs {
			outputs[k] = v.Value
		}
	}

	duration := time.Since(start)
	o.logger.JobCompleted(node.NodeID, status, duration)

	return &JobRunResult{
		NodeID:   node.NodeID,
		Status:   status,
		Duration: duration,
		Outputs:  outputs,
	}
}

// buildContext creates the expression context for a job.
func (o *Orchestrator) buildContext(
	w *workflow.Workflow,
	node *graph.JobNode,
	jobOutputs map[string]*ionsctx.JobResult,
	stepResults map[string]*ionsctx.StepResult,
	runID string,
) expression.MapContext {
	opts := ionsctx.BuilderOptions{
		RepoPath:     o.opts.RepoPath,
		WorkflowEnv:  mergeEnv(w.Env, o.opts.Env),
		EventName:    o.opts.EventName,
		WorkflowName: w.Name,
		RunID:        runID,
		RunNumber:    1,
		Secrets:      o.opts.Secrets,
		Vars:         o.opts.Vars,
		Inputs:       o.opts.Inputs,
		StepResults:  stepResults,
		JobResults:   jobOutputs,
	}

	if node != nil {
		opts.JobEnv = node.Job.Env
		opts.MatrixValues = node.MatrixValues
		opts.JobNeeds = node.Job.Needs
	}

	builder := ionsctx.NewBuilder(opts)
	return builder.FullContext()
}

// dryRun prints the execution plan without running.
func (o *Orchestrator) dryRun(plan *graph.ExecutionPlan, start time.Time) (*RunResult, error) {
	fmt.Println("Dry run — execution plan:")
	fmt.Println()

	for i, group := range plan.Groups {
		fmt.Printf("Stage %d:\n", i+1)
		for _, node := range group.Nodes {
			steps := len(node.Job.Steps)
			fmt.Printf("  %s (%d step(s))\n", node.NodeID, steps)
		}
	}

	if len(plan.Skipped) > 0 {
		fmt.Println()
		fmt.Println("Skipped:")
		for _, node := range plan.Skipped {
			fmt.Printf("  %s (if: %s)\n", node.NodeID, node.Job.If)
		}
	}

	return &RunResult{
		Success:    true,
		JobResults: make(map[string]*JobRunResult),
		Duration:   time.Since(start),
	}, nil
}

// filterPlan filters the execution plan to only include jobs matching the filter.
func filterPlan(plan *graph.ExecutionPlan, filter string) *graph.ExecutionPlan {
	filtered := &graph.ExecutionPlan{}

	for _, group := range plan.Groups {
		var nodes []*graph.JobNode
		for _, node := range group.Nodes {
			if node.JobID == filter || node.NodeID == filter {
				nodes = append(nodes, node)
			}
		}
		if len(nodes) > 0 {
			filtered.Groups = append(filtered.Groups, graph.ParallelGroup{Nodes: nodes})
		}
	}

	return filtered
}

// mergeEnv merges environment maps, with later maps taking precedence.
func mergeEnv(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// streamOutput reads lines from a reader and sends them to the logger.
func streamOutput(r interface{ Read([]byte) (int, error) }, nodeID string, logger *LogStreamer) {
	buf := make([]byte, 4096)
	var line []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b == '\n' {
					if len(line) > 0 {
						logger.StepOutput(nodeID, string(line))
						line = line[:0]
					}
				} else {
					line = append(line, b)
				}
			}
		}
		if err != nil {
			if len(line) > 0 {
				logger.StepOutput(nodeID, string(line))
			}
			return
		}
	}
}

// buildExprDefaults extracts commonly-used expression values from the initial
// context for use in action.yml patching. The runner's legacy ActionManifestManager
// can't parse BasicExpressionToken in action.yml defaults, so the action tarball
// proxy replaces ${{ expr }} with these literal values.
func buildExprDefaults(ctx expression.MapContext) map[string]string {
	defaults := make(map[string]string)

	if ghCtx, ok := ctx["github"]; ok {
		if fields := ghCtx.ObjectFields(); fields != nil {
			for _, key := range []string{
				"token", "repository", "repository_owner",
				"server_url", "api_url", "graphql_url",
				"actor", "ref", "sha", "event_name",
				"workspace", "action",
			} {
				if v, ok := fields[key]; ok {
					defaults["github."+key] = v.StringVal()
				}
			}
		}
	}

	if runnerCtx, ok := ctx["runner"]; ok {
		if fields := runnerCtx.ObjectFields(); fields != nil {
			for _, key := range []string{"os", "arch", "temp", "tool_cache"} {
				if v, ok := fields[key]; ok {
					defaults["runner."+key] = v.StringVal()
				}
			}
		}
	}

	return defaults
}

// sanitizePath replaces characters that are problematic in filesystem paths.
// Matrix node IDs like "test (node: 20, os: ubuntu-latest)" contain spaces,
// parentheses, and colons which cause issues with shell script execution.
func sanitizePath(s string) string {
	r := strings.NewReplacer(
		" ", "-",
		"(", "",
		")", "",
		":", "",
		",", "_",
	)
	return r.Replace(s)
}

// copyDir recursively copies src to dst, skipping the .ions-work directory
// to avoid infinite recursion (the workspace lives inside the repo).
func copyDir(src, dst string) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == ".ions-work" || strings.HasPrefix(rel, ".ions-work"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
