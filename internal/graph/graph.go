package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/emaland/ions/internal/workflow"
)

// JobNode represents a concrete job instance (after matrix expansion).
type JobNode struct {
	JobID        string            // original job ID from workflow
	JobName      string            // display name
	Job          *workflow.Job     // reference to original job definition
	MatrixValues MatrixCombination // matrix values for this instance (nil if no matrix)
	DependsOn    []string          // node IDs this depends on
	NodeID       string            // unique node ID (jobID for non-matrix, jobID (key: val, ...) for matrix)
}

// Graph represents the dependency graph of jobs.
type Graph struct {
	Nodes    map[string]*JobNode
	workflow *workflow.Workflow
}

// Build creates a Graph from a Workflow.
// - For each job, expand its matrix (if any) into multiple JobNodes
// - Wire up DependsOn based on job needs
// - For matrix jobs, each expanded node depends on ALL expanded nodes of its dependency jobs
func Build(w *workflow.Workflow) (*Graph, error) {
	g := &Graph{
		Nodes:    make(map[string]*JobNode),
		workflow: w,
	}

	// Sort job IDs for deterministic ordering.
	jobIDs := make([]string, 0, len(w.Jobs))
	for id := range w.Jobs {
		jobIDs = append(jobIDs, id)
	}
	sort.Strings(jobIDs)

	// Track which original job ID maps to which node IDs.
	jobToNodes := make(map[string][]string)

	// First pass: create all nodes (expanding matrices).
	for _, jobID := range jobIDs {
		job := w.Jobs[jobID]
		baseName := job.Name
		if baseName == "" {
			baseName = jobID
		}

		var matrix *workflow.Matrix
		if job.Strategy != nil {
			matrix = job.Strategy.Matrix
		}

		combos := ExpandMatrix(matrix)
		if len(combos) == 0 {
			// No matrix expansion: single node.
			node := &JobNode{
				JobID:   jobID,
				JobName: baseName,
				Job:     job,
				NodeID:  jobID,
			}
			g.Nodes[node.NodeID] = node
			jobToNodes[jobID] = append(jobToNodes[jobID], node.NodeID)
		} else {
			// Matrix expansion: one node per combo.
			for _, combo := range combos {
				nodeID := matrixNodeID(jobID, combo)
				nodeName := baseName + " " + matrixSuffix(combo)
				node := &JobNode{
					JobID:        jobID,
					JobName:      nodeName,
					Job:          job,
					MatrixValues: combo,
					NodeID:       nodeID,
				}
				g.Nodes[node.NodeID] = node
				jobToNodes[jobID] = append(jobToNodes[jobID], node.NodeID)
			}
		}
	}

	// Second pass: wire up dependencies.
	for _, jobID := range jobIDs {
		job := w.Jobs[jobID]
		if len(job.Needs) == 0 {
			continue
		}

		// Validate that all needed jobs exist.
		for _, need := range job.Needs {
			if _, ok := w.Jobs[need]; !ok {
				return nil, fmt.Errorf("job %q depends on unknown job %q", jobID, need)
			}
		}

		// Collect all dependency node IDs.
		var depNodeIDs []string
		for _, need := range job.Needs {
			depNodeIDs = append(depNodeIDs, jobToNodes[need]...)
		}
		sort.Strings(depNodeIDs)

		// Every node from this job depends on all nodes from its dependency jobs.
		for _, nodeID := range jobToNodes[jobID] {
			g.Nodes[nodeID].DependsOn = depNodeIDs
		}
	}

	return g, nil
}

// matrixNodeID generates a unique node ID for a matrix-expanded job.
// Format: "jobID (key1: val1, key2: val2)" with keys sorted alphabetically.
func matrixNodeID(jobID string, combo MatrixCombination) string {
	return jobID + " " + matrixSuffix(combo)
}

// matrixSuffix generates the "(key1: val1, key2: val2)" suffix.
func matrixSuffix(combo MatrixCombination) string {
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %v", k, combo[k])
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Validate checks for cycles using DFS. Returns an error with the cycle path if found.
func (g *Graph) Validate() error {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int, len(g.Nodes))
	path := make([]string, 0)

	var dfs func(nodeID string) error
	dfs = func(nodeID string) error {
		state[nodeID] = visiting
		path = append(path, nodeID)

		node := g.Nodes[nodeID]
		for _, dep := range node.DependsOn {
			switch state[dep] {
			case visiting:
				// Found a cycle. Build the cycle path.
				cycleStart := -1
				for i, p := range path {
					if p == dep {
						cycleStart = i
						break
					}
				}
				cyclePath := append(path[cycleStart:], dep)
				return fmt.Errorf("cycle detected: %s", strings.Join(cyclePath, " -> "))
			case unvisited:
				if err := dfs(dep); err != nil {
					return err
				}
			}
			// visited: already fully processed, skip.
		}

		path = path[:len(path)-1]
		state[nodeID] = visited
		return nil
	}

	// Process nodes in deterministic order.
	nodeIDs := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	for _, id := range nodeIDs {
		if state[id] == unvisited {
			if err := dfs(id); err != nil {
				return err
			}
		}
	}

	return nil
}

// TopologicalSort returns nodes in topological order using Kahn's algorithm.
// Returns error if graph has cycles.
func (g *Graph) TopologicalSort() ([]*JobNode, error) {
	// Compute in-degrees.
	// Edge: dep -> nodeID means nodeID depends on dep, dep must come first.
	// In Kahn's, in-degree of nodeID = number of deps it has.
	inDegree := make(map[string]int, len(g.Nodes))
	for id, node := range g.Nodes {
		inDegree[id] = len(node.DependsOn)
	}

	// Build adjacency list (forward edges: dep -> dependents).
	dependents := make(map[string][]string, len(g.Nodes))
	for id, node := range g.Nodes {
		for _, dep := range node.DependsOn {
			dependents[dep] = append(dependents[dep], id)
		}
	}

	// Initialize queue with zero in-degree nodes.
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue) // Deterministic ordering.

	var result []*JobNode
	for len(queue) > 0 {
		// Pop first element.
		current := queue[0]
		queue = queue[1:]

		result = append(result, g.Nodes[current])

		// For each dependent of current, decrement in-degree.
		deps := dependents[current]
		sort.Strings(deps)
		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
		// Re-sort queue for determinism.
		sort.Strings(queue)
	}

	if len(result) != len(g.Nodes) {
		return nil, fmt.Errorf("cycle detected: topological sort could not process all %d nodes (processed %d)", len(g.Nodes), len(result))
	}

	return result, nil
}

// NodesByJobID returns all nodes for a given original job ID.
func (g *Graph) NodesByJobID(jobID string) []*JobNode {
	var nodes []*JobNode
	for _, node := range g.Nodes {
		if node.JobID == jobID {
			nodes = append(nodes, node)
		}
	}
	// Sort by NodeID for determinism.
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes
}
