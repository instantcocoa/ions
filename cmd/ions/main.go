package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/orchestrator"
	"github.com/emaland/ions/internal/runner"
	"github.com/emaland/ions/internal/workflow"
)

var verbose bool

func main() {
	root := &cobra.Command{
		Use:   "ions",
		Short: "Local GitHub Actions runner with high-fidelity execution",
	}

	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	root.AddCommand(validateCmd())
	root.AddCommand(listCmd())
	root.AddCommand(runCmd())
	root.AddCommand(runnerCmd())
	root.AddCommand(cleanCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [workflow-file]",
		Short: "Validate a workflow YAML file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveWorkflowPath(args)
			if err != nil {
				return err
			}
			w, err := workflow.ParseFile(path)
			if err != nil {
				return fmt.Errorf("parse error: %w", err)
			}

			errs := workflow.Validate(w)
			if len(errs) == 0 {
				green := color.New(color.FgGreen)
				green.Printf("✓")
				fmt.Printf(" %s is valid\n", path)
				if verbose {
					fmt.Printf("  name: %s\n", w.Name)
					fmt.Printf("  jobs: %d\n", len(w.Jobs))
				}
				return nil
			}

			red := color.New(color.FgRed)
			red.Printf("✗")
			fmt.Printf(" %s has %d validation error(s):\n", path, len(errs))
			for _, e := range errs {
				fmt.Printf("  - %s\n", e)
			}
			return fmt.Errorf("validation failed")
		},
	}
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [workflow-file]",
		Short: "List jobs in a workflow",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveWorkflowPath(args)
			if err != nil {
				return err
			}
			w, err := workflow.ParseFile(path)
			if err != nil {
				return fmt.Errorf("parse error: %w", err)
			}

			errs := workflow.Validate(w)
			if len(errs) > 0 {
				red := color.New(color.FgRed)
				red.Printf("✗")
				fmt.Printf(" workflow has validation errors:\n")
				for _, e := range errs {
					fmt.Printf("  - %s\n", e)
				}
				return fmt.Errorf("validation failed")
			}

			bold := color.New(color.Bold)
			bold.Printf("Workflow: %s\n", w.Name)
			fmt.Println()

			g, err := graph.Build(w)
			if err != nil {
				return fmt.Errorf("graph error: %w", err)
			}

			if err := g.Validate(); err != nil {
				return fmt.Errorf("graph error: %w", err)
			}

			groups, err := g.ParallelGroups()
			if err != nil {
				return fmt.Errorf("graph error: %w", err)
			}

			cyan := color.New(color.FgCyan)
			yellow := color.New(color.FgYellow)
			dim := color.New(color.Faint)

			for i, group := range groups {
				cyan.Printf("Stage %d", i+1)
				fmt.Printf(" (%d job(s)):\n", len(group.Nodes))
				for _, node := range group.Nodes {
					fmt.Printf("  ")
					bold.Printf("%s", node.NodeID)
					if node.JobName != node.NodeID && node.JobName != node.JobID {
						dim.Printf(" (%s)", node.JobName)
					}
					fmt.Println()

					if verbose {
						job := node.Job
						if len(job.RunsOn.Labels) > 0 {
							fmt.Printf("    runs-on: %s\n", strings.Join(job.RunsOn.Labels, ", "))
						}
						if len(node.DependsOn) > 0 {
							fmt.Printf("    needs: %s\n", strings.Join(node.DependsOn, ", "))
						}
						if job.If != "" {
							yellow.Printf("    if: %s\n", job.If)
						}
						if node.MatrixValues != nil {
							keys := make([]string, 0, len(node.MatrixValues))
							for k := range node.MatrixValues {
								keys = append(keys, k)
							}
							sort.Strings(keys)
							parts := make([]string, len(keys))
							for j, k := range keys {
								parts[j] = fmt.Sprintf("%s=%v", k, node.MatrixValues[k])
							}
							fmt.Printf("    matrix: %s\n", strings.Join(parts, ", "))
						}
						if len(job.Steps) > 0 {
							fmt.Printf("    steps: %d\n", len(job.Steps))
						}
						if job.Uses != "" {
							fmt.Printf("    uses: %s\n", job.Uses)
						}
					}
				}
			}

			// Summary
			fmt.Println()
			totalNodes := 0
			for _, group := range groups {
				totalNodes += len(group.Nodes)
			}
			dim.Printf("Total: %d job(s) in %d stage(s)\n", totalNodes, len(groups))
			return nil
		},
	}
}

func runCmd() *cobra.Command {
	var (
		jobFilter       string
		eventName       string
		secrets         []string
		vars            []string
		envVars         []string
		inputs          []string
		dryRun          bool
		jsonOutput      bool
		artifactDir     string
		reuseContainers bool
		platform        string
		githubToken     string
		watch           bool
	)

	cmd := &cobra.Command{
		Use:   "run [workflow-file]",
		Short: "Run a workflow locally",
		Long: `Run a GitHub Actions workflow locally using the real runner binary.

If no workflow file is given, discovers workflows in .github/workflows/
and runs the first one found (or lists them if multiple exist).`,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workflowPath, err := resolveWorkflowPath(args)
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			// Fall back to GITHUB_TOKEN env var if --github-token not set.
			token := githubToken
			if token == "" {
				token = os.Getenv("GITHUB_TOKEN")
			}

			opts := orchestrator.Options{
				WorkflowPath:    workflowPath,
				JobFilter:       jobFilter,
				EventName:       eventName,
				Secrets:         parseKeyValues(secrets),
				Vars:            parseKeyValues(vars),
				Env:             parseKeyValues(envVars),
				Inputs:          parseKeyValues(inputs),
				DryRun:          dryRun,
				Verbose:         verbose,
				ArtifactDir:     artifactDir,
				ReuseContainers: reuseContainers,
				Platform:        platform,
				GitHubToken:     token,
			}

			if watch {
				return orchestrator.WatchAndRun(ctx, opts)
			}

			o, err := orchestrator.New(opts)
			if err != nil {
				return err
			}

			result, err := o.Run(ctx)
			if err != nil {
				return err
			}

			if jsonOutput {
				out := jsonRunResult(result)
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			if !result.Success {
				var failedJobs []string
				for name, jr := range result.JobResults {
					if jr.Status == "failure" {
						failedJobs = append(failedJobs, name)
					}
				}
				if len(failedJobs) > 0 {
					return fmt.Errorf("workflow failed: job(s) %s", strings.Join(failedJobs, ", "))
				}
				return fmt.Errorf("workflow failed")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&jobFilter, "job", "", "run only this job")
	cmd.Flags().StringVar(&eventName, "event", "push", "event name")
	cmd.Flags().StringSliceVar(&secrets, "secret", nil, "secret KEY=VALUE (repeatable)")
	cmd.Flags().StringSliceVar(&vars, "var", nil, "variable KEY=VALUE (repeatable)")
	cmd.Flags().StringSliceVar(&envVars, "env", nil, "environment KEY=VALUE (repeatable)")
	cmd.Flags().StringSliceVar(&inputs, "input", nil, "input KEY=VALUE (repeatable)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print execution plan without running")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output results as JSON")
	cmd.Flags().StringVar(&artifactDir, "artifact-dir", "", "override artifact storage location")
	cmd.Flags().BoolVar(&reuseContainers, "reuse-containers", false, "don't remove containers after run (debugging)")
	cmd.Flags().StringVar(&platform, "platform", "", "override platform detection (e.g. linux/amd64)")
	cmd.Flags().StringVar(&githubToken, "github-token", "", "GitHub token for API passthrough (optional)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "re-run workflow on file changes")

	return cmd
}

func runnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Manage the GitHub Actions runner binary",
	}

	var version string
	var latest bool
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install or update the runner binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := runner.NewManager()
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			if version == "" || latest {
				fmt.Println("Checking for latest runner version...")
				v, err := mgr.LatestVersion(ctx)
				if err != nil {
					return err
				}
				version = v
			}

			// Check if this version is already installed.
			installed, _ := mgr.InstalledVersion()
			if installed == version && !latest {
				green := color.New(color.FgGreen)
				green.Printf("✓")
				fmt.Printf(" Runner v%s is already installed\n", version)
				return nil
			}

			fmt.Printf("Installing runner v%s...\n", version)
			if err := mgr.Install(ctx, version); err != nil {
				return err
			}

			green := color.New(color.FgGreen)
			green.Printf("✓")
			fmt.Printf(" Runner v%s installed to %s\n", version, mgr.VersionDir(version))
			return nil
		},
	}
	installCmd.Flags().StringVar(&version, "version", "", "specific version to install")
	installCmd.Flags().BoolVar(&latest, "latest", false, "install the latest version")
	cmd.AddCommand(installCmd)

	return cmd
}

func cleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove runner caches and work directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := runner.NewManager()
			if err != nil {
				return err
			}

			removed, err := mgr.Clean()
			if err != nil {
				return err
			}

			if len(removed) == 0 {
				fmt.Println("Nothing to clean.")
				return nil
			}

			green := color.New(color.FgGreen)
			for _, dir := range removed {
				green.Printf("✓")
				fmt.Printf(" Removed %s\n", dir)
			}
			return nil
		},
	}
}

// jsonRunResult converts the orchestrator result to a JSON-friendly structure.
func jsonRunResult(result *orchestrator.RunResult) map[string]interface{} {
	jobs := make(map[string]interface{})
	for name, jr := range result.JobResults {
		job := map[string]interface{}{
			"status":   jr.Status,
			"duration": jr.Duration.String(),
		}
		if len(jr.Outputs) > 0 {
			job["outputs"] = jr.Outputs
		}
		jobs[name] = job
	}
	return map[string]interface{}{
		"success":  result.Success,
		"duration": result.Duration.String(),
		"jobs":     jobs,
	}
}

// resolveWorkflowPath determines which workflow file to use.
// If args contains an explicit path, use it. Otherwise, discover workflows
// in .github/workflows/. If exactly one is found, use it. If multiple are
// found, list them and ask the user to choose.
func resolveWorkflowPath(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	dir := ".github/workflows"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no workflow file specified and %s not found\nUsage: ions run <workflow-file>", dir)
	}

	var workflows []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			workflows = append(workflows, filepath.Join(dir, name))
		}
	}

	if len(workflows) == 0 {
		return "", fmt.Errorf("no workflow files found in %s", dir)
	}

	if len(workflows) == 1 {
		dim := color.New(color.Faint)
		dim.Fprintf(os.Stderr, "Using %s\n", workflows[0])
		return workflows[0], nil
	}

	// Multiple workflows — list them and ask user to pick.
	fmt.Fprintf(os.Stderr, "Multiple workflows found in %s:\n", dir)
	for i, w := range workflows {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, w)
	}
	return "", fmt.Errorf("specify which workflow to run: ions run <workflow-file>")
}

// parseKeyValues converts ["KEY=VALUE", ...] to map[string]string.
func parseKeyValues(kvs []string) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}
