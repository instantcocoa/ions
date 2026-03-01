package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/emaland/ions/internal/broker"
	ionsctx "github.com/emaland/ions/internal/context"
	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/runner"
	"github.com/emaland/ions/internal/workflow"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	wfPath := "testdata/workflows/hello-world.yml"
	if len(os.Args) > 1 {
		wfPath = os.Args[1]
	}

	log.Printf("=== E2E Test: %s ===", wfPath)

	// Parse workflow.
	w, err := workflow.ParseFile(wfPath)
	if err != nil {
		log.Fatalf("parse: %v", err)
	}

	g, err := graph.Build(w)
	if err != nil {
		log.Fatalf("graph: %v", err)
	}

	groups, err := g.ParallelGroups()
	if err != nil {
		log.Fatalf("groups: %v", err)
	}

	log.Printf("Workflow: %s (%d groups, %d total jobs)", w.Name, len(groups), len(g.Nodes))
	for i, group := range groups {
		for _, node := range group.Nodes {
			log.Printf("  Group %d: %s (%d steps, needs: %v)", i, node.NodeID, len(node.Job.Steps), node.DependsOn)
		}
	}

	// Start broker.
	srv, err := broker.NewServer(broker.ServerConfig{Verbose: true})
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("start server: %v", err)
	}
	defer srv.Stop(context.Background())

	log.Printf("Broker at %s", srv.URL())

	// Configure runner.
	runnerDir := os.ExpandEnv("$HOME/.ions/runner/2.332.0")
	proc, err := runner.NewProcess(runner.ProcessConfig{
		RunnerDir: runnerDir,
		BrokerURL: srv.URL(),
		Name:      "ions-e2e",
	})
	if err != nil {
		log.Fatalf("runner process: %v", err)
	}

	if err := proc.Configure(ctx); err != nil {
		log.Fatalf("configure: %v", err)
	}

	// Start runner (non-ephemeral — it will keep polling for jobs).
	if err := proc.Start(ctx); err != nil {
		log.Fatalf("start runner: %v", err)
	}
	go streamOutput("STDOUT", proc.Stdout())
	go streamOutput("STDERR", proc.Stderr())

	// Execute groups sequentially.
	jobOutputs := make(map[string]*ionsctx.JobResult)
	allSuccess := true

	for groupIdx, group := range groups {
		log.Printf("=== Executing group %d (%d jobs) ===", groupIdx, len(group.Nodes))

		// For each job in the group, build context and enqueue.
		type jobRun struct {
			node     *graph.JobNode
			resultCh <-chan *broker.JobCompletionResult
		}
		var runs []jobRun

		for _, node := range group.Nodes {
			// Check if dependencies failed — skip unless always().
			depFailed := false
			for _, dep := range node.DependsOn {
				if jr, ok := jobOutputs[dep]; ok && jr.Result != "success" {
					depFailed = true
					break
				}
			}
			if depFailed {
				hasAlways := false
				if node.Job.If != "" {
					// Simple check for always() in if condition.
					hasAlways = containsAlways(node.Job.If)
				}
				if !hasAlways {
					log.Printf("[%s] SKIPPED (dependency failed)", node.NodeID)
					jobOutputs[node.NodeID] = &ionsctx.JobResult{
						Result: "skipped",
					}
					continue
				}
			}

			// Build context.
			exprCtx := buildJobContext(w, node, srv.URL(), jobOutputs)

			// Build job message.
			msg, err := broker.BuildJobMessage(node, node.Job, exprCtx, srv.URL(), "1", nil)
			if err != nil {
				log.Fatalf("[%s] build job: %v", node.NodeID, err)
			}

			log.Printf("[%s] Enqueuing job (requestId=%d, outputs=%v)", node.NodeID, msg.RequestID, msg.JobOutputs)
			resultCh := srv.EnqueueJob(msg)
			runs = append(runs, jobRun{node: node, resultCh: resultCh})
		}

		// Wait for all jobs in this group to complete.
		for _, run := range runs {
			select {
			case result := <-run.resultCh:
				status := "success"
				if result.Result != "succeeded" {
					status = "failure"
					allSuccess = false
				}

				outputs := make(map[string]string)
				for k, v := range result.Outputs {
					outputs[k] = v.Value
				}

				log.Printf("[%s] === RESULT: %s ===", run.node.NodeID, result.Result)
				if len(outputs) > 0 {
					log.Printf("[%s] Outputs: %v", run.node.NodeID, outputs)
				}
				for _, rec := range result.Timeline {
					r := "nil"
					if rec.Result != nil {
						r = *rec.Result
					}
					log.Printf("[%s]   Timeline: %s [%s] result=%s", run.node.NodeID, rec.Name, rec.State, r)
				}
				for id, lines := range result.Logs {
					log.Printf("[%s]   Log %s: %d lines", run.node.NodeID, id, len(lines))
					for _, l := range lines {
						log.Printf("[%s]     %s", run.node.NodeID, l)
					}
				}

				jobOutputs[run.node.NodeID] = &ionsctx.JobResult{
					Result:  status,
					Outputs: outputs,
				}

			case <-time.After(90 * time.Second):
				log.Printf("[%s] TIMEOUT waiting for result", run.node.NodeID)
				allSuccess = false
				jobOutputs[run.node.NodeID] = &ionsctx.JobResult{Result: "failure"}
			}
		}
	}

	proc.Stop()

	// Summary.
	log.Println()
	log.Println("=== SUMMARY ===")
	for nodeID, jr := range jobOutputs {
		log.Printf("  %s: %s", nodeID, jr.Result)
		if len(jr.Outputs) > 0 {
			log.Printf("    outputs: %v", jr.Outputs)
		}
	}
	if allSuccess {
		log.Println("=== ALL JOBS SUCCEEDED ===")
	} else {
		log.Println("=== SOME JOBS FAILED ===")
	}

	// Print request log.
	log.Println()
	log.Println("=== Request Log ===")
	for _, entry := range srv.RequestLog() {
		fmt.Printf("  %s %s → %d\n", entry.Method, entry.Path, entry.StatusCode)
	}
}

func buildJobContext(w *workflow.Workflow, node *graph.JobNode, brokerURL string, jobOutputs map[string]*ionsctx.JobResult) expression.MapContext {
	// Build needs context from completed job results.
	needsFields := make(map[string]expression.Value)
	for _, dep := range node.Job.Needs {
		if jr, ok := jobOutputs[dep]; ok {
			outputs := make(map[string]expression.Value)
			for k, v := range jr.Outputs {
				outputs[k] = expression.String(v)
			}
			needsFields[dep] = expression.Object(map[string]expression.Value{
				"result":  expression.String(jr.Result),
				"outputs": expression.Object(outputs),
			})
		}
	}

	// Build env context from workflow-level and job-level env.
	envMap := make(map[string]expression.Value)
	for k, v := range w.Env {
		envMap[k] = expression.String(v)
	}
	for k, v := range node.Job.Env {
		envMap[k] = expression.String(v)
	}

	return expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"event_name": expression.String("push"),
			"ref":        expression.String("refs/heads/main"),
			"sha":        expression.String("abc123def456"),
			"repository": expression.String("test/repo"),
			"actor":      expression.String("test-user"),
			"workflow":   expression.String(w.Name),
			"run_id":     expression.String("1"),
			"run_number": expression.String("1"),
			"workspace":  expression.String("/tmp/workspace"),
			"server_url": expression.String(brokerURL),
			"api_url":    expression.String(brokerURL),
		}),
		"runner": expression.Object(map[string]expression.Value{
			"os":         expression.String("macOS"),
			"arch":       expression.String("ARM64"),
			"temp":       expression.String("/tmp"),
			"tool_cache": expression.String("/tmp/tool_cache"),
		}),
		"env":   expression.Object(envMap),
		"needs": expression.Object(needsFields),
	}
}

func containsAlways(s string) bool {
	for i := 0; i+8 <= len(s); i++ {
		if s[i:i+8] == "always()" {
			return true
		}
	}
	return false
}

func streamOutput(prefix string, r io.ReadCloser) {
	if r == nil {
		return
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		log.Printf("[%s] %s", prefix, scanner.Text())
	}
}
