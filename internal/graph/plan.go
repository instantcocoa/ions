package graph

import (
	"sort"
	"strings"

	"github.com/emaland/ions/internal/expression"
)

// needsRuntimeEval returns true if a job's if: condition references data
// that is only available at runtime (needs context, status functions that
// inspect dependency results). These conditions are deferred to execution
// time rather than evaluated at plan time.
func needsRuntimeEval(ifCond string) bool {
	if ifCond == "" {
		return false
	}
	// References to the needs context.
	if strings.Contains(ifCond, "needs.") {
		return true
	}
	// Status functions that depend on dependency results.
	// Note: always() is NOT deferred — it always evaluates to true.
	for _, fn := range []string{"failure()", "success()", "cancelled()"} {
		if strings.Contains(ifCond, fn) {
			return true
		}
	}
	return false
}

// ParallelGroup is a set of jobs that can run concurrently.
type ParallelGroup struct {
	Nodes []*JobNode
}

// ExecutionPlan is the ordered list of parallel groups.
type ExecutionPlan struct {
	Groups  []ParallelGroup
	Skipped []*JobNode // jobs skipped due to if: condition
}

// ParallelGroups groups nodes by topological level.
// Level 0 = no dependencies, Level 1 = depends only on Level 0, etc.
func (g *Graph) ParallelGroups() ([]ParallelGroup, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}

	// Assign levels: level of a node = max(level of its dependencies) + 1.
	// Nodes with no dependencies are level 0.
	levels := make(map[string]int, len(g.Nodes))

	// We need to compute levels in topological order.
	sorted, err := g.TopologicalSort()
	if err != nil {
		return nil, err
	}

	if len(sorted) == 0 {
		return nil, nil
	}

	for _, node := range sorted {
		maxDepLevel := -1
		for _, dep := range node.DependsOn {
			if levels[dep] > maxDepLevel {
				maxDepLevel = levels[dep]
			}
		}
		levels[node.NodeID] = maxDepLevel + 1
	}

	// Group by level.
	maxLevel := 0
	for _, level := range levels {
		if level > maxLevel {
			maxLevel = level
		}
	}

	groups := make([]ParallelGroup, maxLevel+1)
	for _, node := range sorted {
		level := levels[node.NodeID]
		groups[level].Nodes = append(groups[level].Nodes, node)
	}

	// Sort within each group by NodeID for determinism.
	for i := range groups {
		sort.Slice(groups[i].Nodes, func(a, b int) bool {
			return groups[i].Nodes[a].NodeID < groups[i].Nodes[b].NodeID
		})
	}

	return groups, nil
}

// PlanOptions configures expression evaluation during planning.
type PlanOptions struct {
	// Functions overrides the default built-in function map for expression evaluation.
	// When set, hashFiles() etc. can use real file-system access.
	Functions map[string]expression.Function
}

// Plan produces an ExecutionPlan by:
// 1. Computing parallel groups
// 2. Evaluating job-level if: conditions (using the expression evaluator)
// 3. Moving skipped jobs to Skipped list
// Jobs whose dependencies were skipped are also skipped (unless they have always() in their if:)
func (g *Graph) Plan(ctx expression.Context, opts ...PlanOptions) (*ExecutionPlan, error) {
	groups, err := g.ParallelGroups()
	if err != nil {
		return nil, err
	}

	// Merge options.
	var fns map[string]expression.Function
	for _, o := range opts {
		if o.Functions != nil {
			fns = o.Functions
		}
	}

	// evalExpr evaluates a condition using custom functions if provided.
	evalExpr := func(input string) (expression.Value, error) {
		if fns != nil {
			return expression.EvalExpressionWithFunctions(input, ctx, fns)
		}
		return expression.EvalExpression(input, ctx)
	}

	plan := &ExecutionPlan{}
	skippedNodes := make(map[string]bool)

	for _, group := range groups {
		var included []*JobNode
		for _, node := range group.Nodes {
			// Check if any dependency was skipped.
			depSkipped := false
			for _, dep := range node.DependsOn {
				if skippedNodes[dep] {
					depSkipped = true
					break
				}
			}

			skip := false

			if depSkipped {
				// If a dependency was skipped, skip this job too,
				// unless its if: condition contains "always()".
				ifCond := node.Job.If
				if !strings.Contains(ifCond, "always()") {
					skip = true
				}
			}

			// If the condition references runtime data (needs, failure(), etc.),
			// defer evaluation to execution time.
			if !skip && node.Job.If != "" && needsRuntimeEval(node.Job.If) {
				node.NeedsRuntimeEval = true
				// Include in plan — the orchestrator will evaluate at runtime.
			} else if !skip && node.Job.If != "" {
				// Static condition — evaluate at plan time.
				result, evalErr := evalExpr(node.Job.If)
				if evalErr != nil {
					// If the expression fails to evaluate, skip the job.
					skip = true
				} else {
					if !expression.IsTruthy(result) {
						skip = true
					}
				}
			}

			if skip {
				skippedNodes[node.NodeID] = true
				plan.Skipped = append(plan.Skipped, node)
			} else {
				included = append(included, node)
			}
		}

		if len(included) > 0 {
			plan.Groups = append(plan.Groups, ParallelGroup{Nodes: included})
		}
	}

	return plan, nil
}
