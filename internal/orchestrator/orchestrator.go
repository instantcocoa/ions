package orchestrator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	"github.com/emaland/ions/internal/githubstub"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/reusable"
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
	MatrixFilter    map[string]string // filter matrix combinations (e.g. os=ubuntu-latest)
	EventPayload    map[string]any    // custom event JSON for github.event
	DryRun          bool
	Verbose         bool
	RepoPath        string
	ArtifactDir     string
	ReuseContainers bool
	Platform        string
	GitHubToken     string
}

// RunResult captures the outcome of the full orchestrated run.
type RunResult struct {
	Success    bool
	JobResults map[string]*JobRunResult
	Duration   time.Duration
}

// Orchestrator coordinates the execution of a workflow.
type Orchestrator struct {
	opts           Options
	broker         *broker.Server
	runnerMgr      *runner.Manager
	logger         *LogStreamer
	masker         *SecretMasker
	jobIDToNodeID  *sync.Map // broker jobID (UUID) → orchestrator nodeID
	progress       *ProgressUI
	concurrency    *ConcurrencyTracker
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

	// Set up progress UI when stdout is a TTY and not in verbose/dry-run mode.
	var progress *ProgressUI
	logger := NewLogStreamer(masker, opts.Verbose)
	if !opts.Verbose && !opts.DryRun {
		progress = NewProgressUI()
	}

	var mgr *runner.Manager
	if !opts.DryRun {
		var err error
		mgr, err = runner.NewManager()
		if err != nil {
			return nil, fmt.Errorf("runner manager: %w", err)
		}
	}

	return &Orchestrator{
		opts:        opts,
		runnerMgr:   mgr,
		logger:      logger,
		masker:      masker,
		progress:    progress,
		concurrency: NewConcurrencyTracker(),
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

	// Build initial context for planning.
	runID := uuid.New().String()[:8]
	initialCtx := o.buildContext(w, nil, nil, nil, runID)

	// Display run-name if set (may contain ${{ }} expressions).
	// Evaluated after initialCtx so it has access to github, env, inputs contexts.
	if w.RunName != "" {
		displayName := w.RunName
		if strings.Contains(w.RunName, "${{") {
			if val, evalErr := expression.EvalInterpolation(w.RunName, initialCtx); evalErr == nil {
				displayName = val
			}
		}
		fmt.Fprintf(os.Stdout, "Run: %s\n", displayName)
	}

	// Warn if the repo has no git remote — actions/checkout won't be able
	// to clone, but the workspace is pre-populated so most workflows work.
	if ghCtx, ok := initialCtx["github"]; ok {
		if fields := ghCtx.ObjectFields(); fields != nil {
			if repo, ok := fields["repository"]; ok && repo.StringVal() == "local/repo" {
				fmt.Fprintf(os.Stderr, "warning: no git remote detected — actions/checkout may fail; workspace is pre-populated with local files\n")
			}
		}
	}

	// Set up expression functions with real hashFiles() that uses the repo directory.
	fns := expression.BuiltinFunctions()
	expression.SetHashFilesWorkDir(fns, o.opts.RepoPath)

	// Pre-evaluate dynamic matrix expressions (e.g. fromJSON(vars.matrix))
	// before graph building, so matrix expansion can produce concrete nodes.
	resolveMatrixExpressions(w, initialCtx, fns)

	// Build graph.
	g, err := graph.Build(w)
	if err != nil {
		return nil, fmt.Errorf("graph error: %w", err)
	}
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("graph error: %w", err)
	}

	// Create execution plan.
	plan, err := g.Plan(initialCtx, graph.PlanOptions{Functions: fns})
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

	// Filter matrix combinations if requested.
	if len(o.opts.MatrixFilter) > 0 {
		plan = filterPlanByMatrix(plan, o.opts.MatrixFilter)
		if len(plan.Groups) == 0 {
			return nil, fmt.Errorf("no matrix combinations match filter")
		}
	}

	// Dry run — print plan and return.
	if o.opts.DryRun {
		return o.dryRun(plan, start)
	}

	// Workflow-level concurrency: acquire the group before running.
	if w.Concurrency != nil && w.Concurrency.Group != "" {
		group := w.Concurrency.Group
		if strings.Contains(group, "${{") {
			if val, evalErr := expression.EvalInterpolation(group, initialCtx); evalErr == nil {
				group = val
			}
		}
		var release func()
		ctx, release = o.concurrency.Acquire(ctx, group, w.Concurrency.CancelInProgress)
		defer release()
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
		GitHubToken:  o.opts.GitHubToken,
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

	// Register GitHub API stub on the broker.
	repoInfo := repoInfoFromContext(initialCtx, o.opts.RepoPath)
	stubSrv := githubstub.NewServer(repoInfo, brokerSrv.URL(), githubstub.Options{
		Token:   o.opts.GitHubToken,
		Verbose: o.opts.Verbose,
		Vars:    o.opts.Vars,
	})
	brokerSrv.RegisterRoutes(stubSrv)

	if err := brokerSrv.Start(ctx); err != nil {
		return nil, fmt.Errorf("broker start: %w", err)
	}
	defer brokerSrv.Stop(context.Background())

	// Register all job nodes with the progress UI so it knows the full
	// set of jobs to display.
	if o.progress != nil {
		var allNodeIDs []string
		for _, group := range plan.Groups {
			for _, node := range group.Nodes {
				allNodeIDs = append(allNodeIDs, node.NodeID)
			}
		}
		o.progress.RegisterJobs(allNodeIDs)
		// Print initial blank lines for the progress UI to overwrite.
		for i := 0; i < len(allNodeIDs)+2; i++ {
			fmt.Fprintln(os.Stdout)
		}
	}

	// Set up step progress callback so the orchestrator can log step
	// status changes (InProgress, Completed) in real time.
	o.jobIDToNodeID = &sync.Map{}
	brokerSrv.OnStepUpdate(func(jobID, stepName, state string, result *string) {
		nodeID, ok := o.jobIDToNodeID.Load(jobID)
		if !ok {
			return
		}
		nid := nodeID.(string)
		switch state {
		case "InProgress":
			o.logger.StepOutput(nid, fmt.Sprintf(">> %s", stepName))
		case "Completed":
			r := "succeeded"
			if result != nil {
				r = *result
			}
			o.logger.StepOutput(nid, fmt.Sprintf("<< %s (%s)", stepName, r))
		}
		// Update progress UI.
		if o.progress != nil {
			o.progress.StepUpdate(nid, stepName, state, result)
		}
	})

	// Set up expression functions for runtime if: evaluation.
	runtimeFns := expression.BuiltinFunctions()
	expression.SetHashFilesWorkDir(runtimeFns, o.opts.RepoPath)

	// Execute plan group by group.
	allResults := make(map[string]*JobRunResult)
	jobOutputs := make(map[string]*ionsctx.JobResult) // for needs context

	success := true
	for _, group := range plan.Groups {
		// Check for context cancellation between groups.
		if ctx.Err() != nil {
			// Mark all remaining jobs as cancelled.
			for _, node := range group.Nodes {
				allResults[node.NodeID] = &JobRunResult{
					NodeID: node.NodeID,
					Status: "cancelled",
				}
			}
			continue
		}

		groupResults := o.executeGroup(ctx, group, w, jobOutputs, brokerSrv, runnerDir, runID, runtimeFns)

		for nodeID, result := range groupResults {
			allResults[nodeID] = result

			// Determine the conclusion (effective result for dependency purposes).
			// If continue-on-error is true, a failure is treated as success.
			conclusion := result.Status
			node := findNode(plan, nodeID)
			isContinueOnError := node != nil && result.Status == "failure" &&
				evalContinueOnError(node.Job, jobOutputs, o.opts.RepoPath, runtimeFns)
			if isContinueOnError {
				conclusion = "success"
			}

			// Only fail the overall run for real failures (not continue-on-error).
			if result.Status == "failure" && !isContinueOnError {
				success = false
			}

			// Store outputs for needs context of subsequent groups.
			jobOutputs[nodeID] = &ionsctx.JobResult{
				Result:  conclusion,
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

	// Clean up work directories on success.
	workBase := filepath.Join(o.opts.RepoPath, ".ions-work")
	if success {
		os.RemoveAll(workBase)
	}

	// Finish progress UI before printing the final summary.
	if o.progress != nil {
		o.progress.Finish()
	} else {
		o.logger.Summary(allResults)
	}

	return &RunResult{
		Success:    success,
		JobResults: allResults,
		Duration:   time.Since(start),
	}, nil
}

// executeGroup runs all jobs in a parallel group concurrently.
// Jobs whose dependencies failed are skipped unless they have always() or
// failure() in their if: condition. Deferred runtime if: conditions are
// evaluated here with full needs context. max-parallel is enforced via a
// per-job semaphore.
func (o *Orchestrator) executeGroup(
	ctx context.Context,
	group graph.ParallelGroup,
	w *workflow.Workflow,
	jobOutputs map[string]*ionsctx.JobResult,
	brokerSrv *broker.Server,
	runnerDir string,
	runID string,
	fns map[string]expression.Function,
) map[string]*JobRunResult {
	results := make(map[string]*JobRunResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Build per-job semaphores for max-parallel enforcement.
	semaphores := make(map[string]chan struct{})
	for _, node := range group.Nodes {
		if node.Job.Strategy != nil && node.Job.Strategy.MaxParallel != nil {
			if _, ok := semaphores[node.JobID]; !ok {
				semaphores[node.JobID] = make(chan struct{}, *node.Job.Strategy.MaxParallel)
			}
		}
	}

	// Build per-job-group cancel functions for fail-fast support.
	// When fail-fast is true (default) and a matrix job fails, cancel siblings.
	failFastCancels := make(map[string]context.CancelFunc)
	failFastContexts := make(map[string]context.Context)
	for _, node := range group.Nodes {
		if _, ok := failFastContexts[node.JobID]; ok {
			continue
		}
		failFast := true // GitHub default
		if node.Job.Strategy != nil && node.Job.Strategy.FailFast != nil {
			failFast = *node.Job.Strategy.FailFast
		}
		if failFast && node.JobTotal > 1 {
			ffCtx, ffCancel := context.WithCancel(ctx)
			failFastContexts[node.JobID] = ffCtx
			failFastCancels[node.JobID] = ffCancel
		}
	}
	// Ensure all fail-fast cancel functions are called on exit.
	defer func() {
		for _, cancel := range failFastCancels {
			cancel()
		}
	}()

	for _, node := range group.Nodes {
		// Determine dependency status for this job.
		depStatus := dependencyStatus(node, jobOutputs)

		// Evaluate runtime if: conditions now that needs context is available.
		if node.NeedsRuntimeEval {
			runtimeCtx := o.buildContext(w, node, jobOutputs, nil, runID)
			result, evalErr := expression.EvalExpressionWithStatus(
				node.Job.If, runtimeCtx, fns, depStatus,
			)
			if evalErr != nil || !expression.IsTruthy(result) {
				o.skipJob(node.NodeID, results)
				continue
			}
		} else if depStatus != "success" {
			// No runtime condition — skip if any dependency failed,
			// unless the job has always() in its if: condition.
			if !strings.Contains(node.Job.If, "always()") {
				o.skipJob(node.NodeID, results)
				continue
			}
		}

		wg.Add(1)
		go func(node *graph.JobNode) {
			defer wg.Done()

			// Enforce max-parallel if applicable.
			if sem, ok := semaphores[node.JobID]; ok {
				sem <- struct{}{}
				defer func() { <-sem }()
			}

			// Use fail-fast context if available for this job group.
			jobCtx := ctx
			if ffCtx, ok := failFastContexts[node.JobID]; ok {
				jobCtx = ffCtx
			}

			result := o.executeJob(jobCtx, node, w, jobOutputs, brokerSrv, runnerDir, runID)
			mu.Lock()
			results[node.NodeID] = result
			mu.Unlock()

			// If this job failed and fail-fast is active, cancel siblings.
			if result.Status == "failure" {
				if cancel, ok := failFastCancels[node.JobID]; ok {
					cancel()
				}
			}
		}(node)
	}

	wg.Wait()
	return results
}

// skipJob marks a job as skipped in the results map and logs it.
func (o *Orchestrator) skipJob(nodeID string, results map[string]*JobRunResult) {
	o.logger.JobStarted(nodeID)
	o.logger.JobCompleted(nodeID, "skipped", 0)
	if o.progress != nil {
		o.progress.JobCompleted(nodeID, "skipped")
	}
	results[nodeID] = &JobRunResult{
		NodeID: nodeID,
		Status: "skipped",
	}
}

// dependencyStatus returns the aggregate status of a job's dependencies.
// Returns "success" if all deps succeeded, "cancelled" if any was cancelled,
// "failure" if any failed. Returns "success" if there are no dependencies.
func dependencyStatus(node *graph.JobNode, jobOutputs map[string]*ionsctx.JobResult) string {
	hasCancelled := false
	for _, dep := range node.DependsOn {
		if jr, ok := jobOutputs[dep]; ok {
			switch jr.Result {
			case "failure":
				return "failure"
			case "cancelled":
				hasCancelled = true
			}
		}
	}
	if hasCancelled {
		return "cancelled"
	}
	return "success"
}

// evalContinueOnError evaluates a job's continue-on-error field.
// Returns true if the job has continue-on-error set to true (boolean or expression).
func evalContinueOnError(job *workflow.Job, jobOutputs map[string]*ionsctx.JobResult, repoPath string, fns map[string]expression.Function) bool {
	coe := job.ContinueOnError
	if !coe.IsExpr {
		return coe.Value
	}
	// Expression form — evaluate it.
	result, err := expression.EvalExpressionWithFunctions(coe.Expression, expression.MapContext{}, fns)
	if err != nil {
		return false
	}
	return expression.IsTruthy(result)
}

// findNode searches the execution plan for a node by ID.
func findNode(plan *graph.ExecutionPlan, nodeID string) *graph.JobNode {
	for _, group := range plan.Groups {
		for _, node := range group.Nodes {
			if node.NodeID == nodeID {
				return node
			}
		}
	}
	return nil
}

// executeJob runs a single job via the broker/runner.
// For reusable workflow jobs (uses: at job level), it resolves the called
// workflow and runs it as a nested orchestration.
func (o *Orchestrator) executeJob(
	ctx context.Context,
	node *graph.JobNode,
	w *workflow.Workflow,
	jobOutputs map[string]*ionsctx.JobResult,
	brokerSrv *broker.Server,
	runnerDir string,
	runID string,
) *JobRunResult {
	// Handle reusable workflow calls.
	if node.Job.Uses != "" {
		return o.executeReusableWorkflow(ctx, node, jobOutputs, brokerSrv, runnerDir, runID)
	}

	// Job-level concurrency: acquire the group before running.
	if node.Job.Concurrency != nil && node.Job.Concurrency.Group != "" {
		group := node.Job.Concurrency.Group
		if strings.Contains(group, "${{") {
			jobCtx := o.buildContext(w, node, jobOutputs, nil, runID)
			if val, evalErr := expression.EvalInterpolation(group, jobCtx); evalErr == nil {
				group = val
			}
		}
		var release func()
		ctx, release = o.concurrency.Acquire(ctx, group, node.Job.Concurrency.CancelInProgress)
		defer release()
	}

	// Apply job-level timeout.
	timeout := 360 * time.Minute // GitHub default: 6 hours
	if node.Job.TimeoutMinutes != nil {
		timeout = time.Duration(*node.Job.TimeoutMinutes) * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	o.logger.JobStarted(node.NodeID)
	if o.progress != nil {
		o.progress.JobStarted(node.NodeID)
	}

	// Determine whether the runner should manage containers natively.
	// The runner's built-in container support only works on Linux.
	// When enabled, both the job container and service containers are
	// delegated to the runner; the orchestrator does not manage them.
	useRunnerContainers := node.Job.Container != nil && runtime.GOOS == "linux"

	if node.Job.Container != nil && runtime.GOOS != "linux" {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf(
			"warning: job container (%s) requires Linux — the runner's container support is not available on %s; steps will run on the host",
			node.Job.Container.Image, runtime.GOOS))
	}

	// Set up Docker service containers if the job defines any
	// AND the runner isn't managing them (non-container jobs, or non-Linux).
	var dockerEnv *docker.JobEnvironment
	if len(node.Job.Services) > 0 && !useRunnerContainers {
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
			cfg := docker.ServiceConfig{
				Image:   svc.Image,
				Env:     svc.Env,
				Ports:   svc.Ports,
				Volumes: svc.Volumes,
				Options: svc.Options,
			}
			if svc.Credentials != nil {
				cfg.Credentials = &docker.RegistryCredentials{
					Username: svc.Credentials.Username,
					Password: svc.Credentials.Password,
				}
			}
			services[name] = cfg
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

	// Apply workflow-level defaults to job if the job doesn't define its own.
	applyWorkflowDefaults(w, node.Job)

	// Build context for this job.
	jobCtx := o.buildContext(w, node, jobOutputs, nil, runID)

	// Build job message.
	var msgOpts []broker.JobMessageOptions
	if useRunnerContainers {
		msgOpts = append(msgOpts, broker.JobMessageOptions{UseRunnerContainers: true})
	}
	msg, err := broker.BuildJobMessage(node, node.Job, jobCtx, brokerSrv.URL(), runID, o.opts.Secrets, msgOpts...)
	if err != nil {
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{
			NodeID:   node.NodeID,
			Status:   "failure",
			Duration: time.Since(start),
		}
	}

	// Enqueue job and register the jobID→nodeID mapping for step callbacks.
	if o.jobIDToNodeID != nil {
		o.jobIDToNodeID.Store(msg.JobID, node.NodeID)
	}
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
		ExtraEnv: []string{
			"GITHUB_SERVER_URL=" + brokerSrv.URL(),
			"GITHUB_API_URL=" + brokerSrv.URL() + "/api/v3",
			"GITHUB_GRAPHQL_URL=" + brokerSrv.URL() + "/api/v3/graphql",
		},
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

	// On failure, report which steps failed and show their last log lines.
	if status == "failure" {
		o.reportFailedSteps(node.NodeID, jobResult)
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
	if o.progress != nil {
		o.progress.JobCompleted(node.NodeID, status)
		if status == "failure" {
			// Feed failed step log lines to the progress UI for the final summary.
			for _, record := range jobResult.Timeline {
				if record.Result != nil && *record.Result == "failed" && record.Log != nil {
					if logLines, ok := jobResult.Logs[strconv.Itoa(record.Log.ID)]; ok {
						for _, line := range logLines {
							o.progress.LogLine(node.NodeID, line)
						}
					}
				}
			}
		}
	}

	return &JobRunResult{
		NodeID:   node.NodeID,
		Status:   status,
		Duration: duration,
		Outputs:  outputs,
	}
}

// reportFailedSteps logs details about which steps failed and their last output lines.
func (o *Orchestrator) reportFailedSteps(nodeID string, result *broker.JobCompletionResult) {
	const maxLogLines = 10

	for _, record := range result.Timeline {
		if record.Result == nil {
			continue
		}
		if *record.Result != "failed" {
			continue
		}
		stepName := record.Name
		if stepName == "" {
			stepName = record.RefName
		}
		if stepName == "" {
			continue
		}
		o.logger.StepOutput(nodeID, fmt.Sprintf("FAILED: %s", stepName))

		// Show last N log lines for this step.
		if record.Log != nil {
			if logLines, ok := result.Logs[strconv.Itoa(record.Log.ID)]; ok {
				start := 0
				if len(logLines) > maxLogLines {
					start = len(logLines) - maxLogLines
					o.logger.StepOutput(nodeID, fmt.Sprintf("  ... (%d lines omitted)", start))
				}
				for _, line := range logLines[start:] {
					o.logger.StepOutput(nodeID, "  "+line)
				}
			}
		}
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
		EventPayload: o.opts.EventPayload,
		StepResults:  stepResults,
		JobResults:   jobOutputs,
		GitHubToken:  o.opts.GitHubToken,
	}

	// If the broker is running, route API calls through it.
	if o.broker != nil {
		opts.APIBaseURL = o.broker.URL() + "/api/v3"
	}

	if node != nil {
		opts.JobEnv = node.Job.Env
		opts.MatrixValues = node.MatrixValues
		opts.JobNeeds = node.Job.Needs
		opts.JobIndex = node.JobIndex
		opts.JobTotal = node.JobTotal
		if node.Job.Strategy != nil {
			opts.FailFast = node.Job.Strategy.FailFast
			opts.MaxParallel = node.Job.Strategy.MaxParallel
		}
		if node.Job.Environment != nil {
			opts.EnvironmentName = node.Job.Environment.Name
		}
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

// filterPlanByMatrix filters the execution plan to only include matrix nodes
// where all specified key=value pairs match. Non-matrix nodes are always included.
func filterPlanByMatrix(plan *graph.ExecutionPlan, filter map[string]string) *graph.ExecutionPlan {
	filtered := &graph.ExecutionPlan{}

	for _, group := range plan.Groups {
		var nodes []*graph.JobNode
		for _, node := range group.Nodes {
			if node.MatrixValues == nil {
				// Non-matrix nodes pass through.
				nodes = append(nodes, node)
				continue
			}
			// Check all filter keys match.
			match := true
			for k, v := range filter {
				mv, ok := node.MatrixValues[k]
				if !ok || fmt.Sprintf("%v", mv) != v {
					match = false
					break
				}
			}
			if match {
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

// copyDir recursively copies src to dst, skipping the .ions-work and .git
// directories. Uses hardlinks when possible for performance; falls back to
// regular copy if hardlinking fails (e.g., cross-device).
func copyDir(src, dst string) error {
	src = filepath.Clean(src)

	// Detect whether hardlinks work by testing once up front.
	canHardlink := testHardlink(src, dst)

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)

		// Skip directories that are large, internal, or unnecessary for the runner.
		if d.IsDir() {
			base := filepath.Base(rel)
			switch rel {
			case ".ions-work", ".git", ".letta":
				return filepath.SkipDir
			}
			switch base {
			case "node_modules", "__pycache__", ".tox", ".mypy_cache", ".pytest_cache":
				return filepath.SkipDir
			}
		}

		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if canHardlink {
			if err := os.Link(path, target); err == nil {
				return nil
			}
		}
		return copyFile(path, target)
	})
}

// testHardlink checks whether hardlinking is possible between src and dst.
// Returns false if they're on different filesystems or hardlinks aren't supported.
func testHardlink(src, dst string) bool {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return false
	}
	probe := filepath.Join(dst, ".ions-link-test")
	// Find any regular file in src to test with.
	var testFile string
	filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		testFile = path
		return filepath.SkipAll
	})
	if testFile == "" {
		return false
	}
	err := os.Link(testFile, probe)
	os.Remove(probe)
	return err == nil
}

// repoInfoFromContext extracts repo info from the initial expression context
// for use by the GitHub API stub.
func repoInfoFromContext(ctx expression.MapContext, repoPath string) githubstub.RepoInfo {
	info := githubstub.RepoInfo{RepoPath: repoPath}

	if ghCtx, ok := ctx["github"]; ok {
		if fields := ghCtx.ObjectFields(); fields != nil {
			if v, ok := fields["repository"]; ok {
				parts := strings.SplitN(v.StringVal(), "/", 2)
				if len(parts) == 2 {
					info.Owner = parts[0]
					info.Repo = parts[1]
				}
			}
			if v, ok := fields["ref"]; ok {
				info.CurrentRef = v.StringVal()
			}
			if v, ok := fields["sha"]; ok {
				info.CurrentSHA = v.StringVal()
			}
			if v, ok := fields["ref_name"]; ok {
				info.DefaultBranch = v.StringVal()
			}
			if v, ok := fields["server_url"]; ok {
				if info.Owner != "" && info.Repo != "" {
					info.CloneURL = v.StringVal() + "/" + info.Owner + "/" + info.Repo + ".git"
				}
			}
		}
	}

	return info
}

// executeReusableWorkflow handles a job that calls a reusable workflow via uses:.
// It resolves the called workflow, maps inputs/secrets, builds a sub-graph,
// and executes the called workflow's jobs using the existing broker/runner.
func (o *Orchestrator) executeReusableWorkflow(
	ctx context.Context,
	node *graph.JobNode,
	jobOutputs map[string]*ionsctx.JobResult,
	brokerSrv *broker.Server,
	runnerDir string,
	runID string,
) *JobRunResult {
	start := time.Now()
	o.logger.JobStarted(node.NodeID)
	o.logger.StepOutput(node.NodeID, fmt.Sprintf("Resolving reusable workflow: %s", node.Job.Uses))

	// Parse the reference.
	ref, err := reusable.ParseReference(node.Job.Uses)
	if err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("invalid reusable workflow reference: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{NodeID: node.NodeID, Status: "failure", Duration: time.Since(start)}
	}

	// Resolve the workflow file.
	resolver := reusable.NewResolver(reusable.ResolverOptions{
		RepoPath:    o.opts.RepoPath,
		GitHubToken: o.opts.GitHubToken,
	})

	calledWorkflow, err := resolver.Resolve(ctx, ref)
	if err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("failed to resolve reusable workflow: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{NodeID: node.NodeID, Status: "failure", Duration: time.Since(start)}
	}

	o.logger.StepOutput(node.NodeID, fmt.Sprintf("Resolved workflow %q with %d job(s)", calledWorkflow.Name, len(calledWorkflow.Jobs)))

	// Map inputs from the caller's with: to the called workflow's inputs.
	calledInputs := reusable.MapInputs(node.Job.With, calledWorkflow.On)

	// Map secrets.
	calledSecrets := reusable.MapSecrets(o.opts.Secrets, node.Job.Secrets)

	// Build a graph from the called workflow.
	calledGraph, err := graph.Build(calledWorkflow)
	if err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("graph error in called workflow: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{NodeID: node.NodeID, Status: "failure", Duration: time.Since(start)}
	}
	if err := calledGraph.Validate(); err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("graph validation error in called workflow: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{NodeID: node.NodeID, Status: "failure", Duration: time.Since(start)}
	}

	// Build a context for the called workflow, with the mapped inputs.
	calledOpts := Options{
		RepoPath:        o.opts.RepoPath,
		EventName:       o.opts.EventName,
		Secrets:         calledSecrets,
		Vars:            o.opts.Vars,
		Env:             o.opts.Env,
		Inputs:          calledInputs,
		Verbose:         o.opts.Verbose,
		ArtifactDir:     o.opts.ArtifactDir,
		ReuseContainers: o.opts.ReuseContainers,
		Platform:        o.opts.Platform,
		GitHubToken:     o.opts.GitHubToken,
	}

	// Build the initial context for planning the called workflow.
	calledOrch := &Orchestrator{
		opts:      calledOpts,
		broker:    brokerSrv,
		runnerMgr: o.runnerMgr,
		logger:    o.logger,
		masker:    o.masker,
	}

	initialCtx := calledOrch.buildContext(calledWorkflow, nil, nil, nil, runID)
	fns := expression.BuiltinFunctions()
	expression.SetHashFilesWorkDir(fns, o.opts.RepoPath)

	plan, err := calledGraph.Plan(initialCtx, graph.PlanOptions{Functions: fns})
	if err != nil {
		o.logger.StepOutput(node.NodeID, fmt.Sprintf("plan error in called workflow: %v", err))
		o.logger.JobCompleted(node.NodeID, "failure", time.Since(start))
		return &JobRunResult{NodeID: node.NodeID, Status: "failure", Duration: time.Since(start)}
	}

	// Execute the called workflow's jobs group by group.
	calledJobOutputs := make(map[string]*ionsctx.JobResult)
	success := true

	for _, group := range plan.Groups {
		groupResults := calledOrch.executeGroup(ctx, group, calledWorkflow, calledJobOutputs, brokerSrv, runnerDir, runID, fns)
		for calledNodeID, result := range groupResults {
			if result.Status == "failure" {
				success = false
			}
			calledJobOutputs[calledNodeID] = &ionsctx.JobResult{
				Result:  result.Status,
				Outputs: result.Outputs,
			}
		}
	}

	// Collect outputs from the called workflow.
	// The workflow_call trigger defines outputs that map to job outputs.
	outputs := make(map[string]string)
	outputDefs := calledWorkflow.On.WorkflowCallOutputs()
	for outName, outDef := range outputDefs {
		// Output values are expressions like ${{ jobs.build.outputs.version }}.
		// Evaluate them against the called workflow's job results.
		if outDef.Value != "" {
			outCtx := calledOrch.buildContext(calledWorkflow, nil, calledJobOutputs, nil, runID)
			val, evalErr := expression.EvalExpressionWithFunctions(outDef.Value, outCtx, fns)
			if evalErr == nil {
				outputs[outName] = val.StringVal()
			}
		}
	}

	status := "success"
	if !success {
		status = "failure"
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

// resolveMatrixExpressions evaluates dynamic matrix expressions (e.g.
// ${{ fromJSON(vars.matrix) }}) and replaces them with concrete matrix
// definitions. This must be called before graph.Build() so that matrix
// expansion can produce concrete job nodes.
func resolveMatrixExpressions(w *workflow.Workflow, ctx expression.MapContext, fns map[string]expression.Function) {
	for _, job := range w.Jobs {
		if job.Strategy == nil || job.Strategy.Matrix == nil {
			continue
		}
		m := job.Strategy.Matrix
		if m.Expression == "" {
			continue
		}

		// Strip ${{ }} wrapper if present.
		expr := m.Expression
		expr = strings.TrimSpace(expr)
		if strings.HasPrefix(expr, "${{") && strings.HasSuffix(expr, "}}") {
			expr = strings.TrimSpace(expr[3 : len(expr)-2])
		}

		// Skip expressions that reference needs.* — those can't be resolved statically.
		if strings.Contains(expr, "needs.") {
			continue
		}

		result, err := expression.EvalExpressionWithFunctions(expr, ctx, fns)
		if err != nil {
			continue // Can't resolve — leave as expression for runtime
		}

		// The result should be an object (map) with dimension keys → arrays.
		fields := result.ObjectFields()
		if fields == nil {
			continue
		}

		// Convert expression result back to a concrete Matrix.
		newMatrix := &workflow.Matrix{
			Dimensions: make(map[string][]interface{}),
		}

		for key, val := range fields {
			switch key {
			case "include":
				newMatrix.Include = expressionValueToListOfMaps(val)
			case "exclude":
				newMatrix.Exclude = expressionValueToListOfMaps(val)
			default:
				// Dimension: expect an array of values.
				arr := val.ArrayItems()
				if arr != nil {
					vals := make([]interface{}, len(arr))
					for i, elem := range arr {
						vals[i] = expressionValueToInterface(elem)
					}
					newMatrix.Dimensions[key] = vals
				}
			}
		}

		job.Strategy.Matrix = newMatrix
	}
}

// expressionValueToInterface converts an expression.Value to a Go interface{}.
func expressionValueToInterface(v expression.Value) interface{} {
	switch v.Kind() {
	case expression.KindNull:
		return nil
	case expression.KindBool:
		return v.BoolVal()
	case expression.KindNumber:
		n := v.NumberVal()
		if n == float64(int(n)) {
			return int(n)
		}
		return n
	case expression.KindString:
		return v.StringVal()
	}
	// Fall through for arrays and objects.
	if fields := v.ObjectFields(); fields != nil {
		m := make(map[string]interface{}, len(fields))
		for k, fv := range fields {
			m[k] = expressionValueToInterface(fv)
		}
		return m
	}
	if arr := v.ArrayItems(); arr != nil {
		result := make([]interface{}, len(arr))
		for i, elem := range arr {
			result[i] = expressionValueToInterface(elem)
		}
		return result
	}
	return v.StringVal()
}

// expressionValueToListOfMaps converts an expression array value to []map[string]interface{}.
func expressionValueToListOfMaps(v expression.Value) []map[string]interface{} {
	arr := v.ArrayItems()
	if arr == nil {
		return nil
	}
	var result []map[string]interface{}
	for _, elem := range arr {
		fields := elem.ObjectFields()
		if fields == nil {
			continue
		}
		m := make(map[string]interface{}, len(fields))
		for k, fv := range fields {
			m[k] = expressionValueToInterface(fv)
		}
		result = append(result, m)
	}
	return result
}

// applyWorkflowDefaults merges workflow-level defaults into a job when the
// job doesn't define its own. Job-level values take precedence over workflow-level.
func applyWorkflowDefaults(w *workflow.Workflow, job *workflow.Job) {
	if w.Defaults == nil || w.Defaults.Run == nil {
		return
	}
	wfRun := w.Defaults.Run

	if job.Defaults == nil {
		// No job defaults — inherit workflow defaults entirely.
		job.Defaults = &workflow.Defaults{
			Run: &workflow.RunDefaults{
				Shell:            wfRun.Shell,
				WorkingDirectory: wfRun.WorkingDirectory,
			},
		}
		return
	}

	if job.Defaults.Run == nil {
		job.Defaults.Run = &workflow.RunDefaults{
			Shell:            wfRun.Shell,
			WorkingDirectory: wfRun.WorkingDirectory,
		}
		return
	}

	// Merge: workflow fills in gaps that job doesn't set.
	if job.Defaults.Run.Shell == "" {
		job.Defaults.Run.Shell = wfRun.Shell
	}
	if job.Defaults.Run.WorkingDirectory == "" {
		job.Defaults.Run.WorkingDirectory = wfRun.WorkingDirectory
	}
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
