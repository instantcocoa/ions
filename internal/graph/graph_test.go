package graph

import (
	"testing"

	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Matrix expansion tests
// ---------------------------------------------------------------------------

func TestExpandMatrix_Nil(t *testing.T) {
	assert.Nil(t, ExpandMatrix(nil))
}

func TestExpandMatrix_Expression(t *testing.T) {
	m := &workflow.Matrix{Expression: "${{ fromJSON(needs.setup.outputs.matrix) }}"}
	assert.Nil(t, ExpandMatrix(m))
}

func TestExpandMatrix_NoDimensionsNoIncludes(t *testing.T) {
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{},
	}
	assert.Nil(t, ExpandMatrix(m))
}

func TestExpandMatrix_Simple2x3(t *testing.T) {
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"os":      {"ubuntu", "windows"},
			"version": {14, 16, 18},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 6)

	// Verify all expected combinations are present.
	expected := []MatrixCombination{
		{"os": "ubuntu", "version": 14},
		{"os": "ubuntu", "version": 16},
		{"os": "ubuntu", "version": 18},
		{"os": "windows", "version": 14},
		{"os": "windows", "version": 16},
		{"os": "windows", "version": 18},
	}
	for _, exp := range expected {
		assert.Contains(t, combos, exp, "expected combo %v not found", exp)
	}
}

func TestExpandMatrix_WithExclude(t *testing.T) {
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"os":      {"ubuntu", "windows"},
			"version": {14, 16, 18},
		},
		Exclude: []map[string]interface{}{
			{"os": "windows", "version": 14},
			{"os": "ubuntu", "version": 18},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 4)

	// These should be excluded.
	for _, combo := range combos {
		if combo["os"] == "windows" {
			assert.NotEqual(t, 14, combo["version"], "windows/14 should be excluded")
		}
		if combo["os"] == "ubuntu" {
			assert.NotEqual(t, 18, combo["version"], "ubuntu/18 should be excluded")
		}
	}
}

func TestExpandMatrix_IncludeMergesExtraKeys(t *testing.T) {
	// Include entry matches on existing dimension keys and merges extra keys.
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"os":      {"ubuntu", "windows"},
			"version": {14},
		},
		Include: []map[string]interface{}{
			{"os": "ubuntu", "version": 14, "arch": "arm64"},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 2) // Still 2 combos from 2x1.

	// Find the ubuntu/14 combo and check it has arch.
	found := false
	for _, combo := range combos {
		if combo["os"] == "ubuntu" && combo["version"] == 14 {
			assert.Equal(t, "arm64", combo["arch"])
			found = true
		}
	}
	assert.True(t, found, "expected ubuntu/14 combo with arch=arm64")
}

func TestExpandMatrix_IncludeAddsNewCombo(t *testing.T) {
	// Include entry that doesn't match any existing combo on its dimension keys.
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"os":      {"ubuntu"},
			"version": {14},
		},
		Include: []map[string]interface{}{
			{"os": "macos", "version": 12},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 2) // 1 from dimensions + 1 from include.

	// Check the new combo.
	found := false
	for _, combo := range combos {
		if combo["os"] == "macos" && combo["version"] == 12 {
			found = true
		}
	}
	assert.True(t, found, "expected macos/12 combo from include")
}

func TestExpandMatrix_IncludeOnlyNoDimensions(t *testing.T) {
	// No dimensions, just include entries.
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{},
		Include: []map[string]interface{}{
			{"os": "ubuntu", "version": 20},
			{"os": "macos", "version": 12},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 2)
	assert.Contains(t, combos, MatrixCombination{"os": "ubuntu", "version": 20})
	assert.Contains(t, combos, MatrixCombination{"os": "macos", "version": 12})
}

func TestExpandMatrix_ExcludeWithStringComparison(t *testing.T) {
	// Ensure exclude works with interface{} values using fmt.Sprintf comparison.
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"node": {"14", "16"},
		},
		Exclude: []map[string]interface{}{
			{"node": "14"},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 1)
	assert.Equal(t, "16", combos[0]["node"])
}

func TestExpandMatrix_IncludeMergesIntoAllMatching(t *testing.T) {
	// Include should merge into ALL matching combos, not just the first.
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"os":      {"ubuntu", "ubuntu"},
			"version": {20},
		},
		Include: []map[string]interface{}{
			{"os": "ubuntu", "extra": "yes"},
		},
	}
	combos := ExpandMatrix(m)
	// Both combos are os=ubuntu, version=20, so include should merge into both.
	for _, combo := range combos {
		if combo["os"] == "ubuntu" {
			assert.Equal(t, "yes", combo["extra"], "include should merge into matching combo")
		}
	}
}

func TestExpandMatrix_SingleDimension(t *testing.T) {
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"node": {12, 14, 16},
		},
	}
	combos := ExpandMatrix(m)
	require.Len(t, combos, 3)
}

// ---------------------------------------------------------------------------
// Graph build tests
// ---------------------------------------------------------------------------

func makeWorkflow(jobs map[string]*workflow.Job) *workflow.Workflow {
	return &workflow.Workflow{Jobs: jobs}
}

func TestBuild_SingleJob(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo hello"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 1)

	node := g.Nodes["build"]
	require.NotNil(t, node)
	assert.Equal(t, "build", node.NodeID)
	assert.Equal(t, "build", node.JobName)
	assert.Empty(t, node.DependsOn)
	assert.Nil(t, node.MatrixValues)
}

func TestBuild_LinearChain(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Needs: workflow.StringOrSlice{"b"}, Steps: []workflow.Step{{Run: "echo c"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 3)

	assert.Empty(t, g.Nodes["a"].DependsOn)
	assert.Equal(t, []string{"a"}, g.Nodes["b"].DependsOn)
	assert.Equal(t, []string{"b"}, g.Nodes["c"].DependsOn)
}

func TestBuild_Diamond(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo c"}}},
		"d": {Needs: workflow.StringOrSlice{"b", "c"}, Steps: []workflow.Step{{Run: "echo d"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 4)

	assert.Empty(t, g.Nodes["a"].DependsOn)
	assert.Equal(t, []string{"a"}, g.Nodes["b"].DependsOn)
	assert.Equal(t, []string{"a"}, g.Nodes["c"].DependsOn)
	assert.Equal(t, []string{"b", "c"}, g.Nodes["d"].DependsOn)
}

func TestBuild_UnknownDependency(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Needs: workflow.StringOrSlice{"nonexistent"}, Steps: []workflow.Step{{Run: "echo a"}}},
	})
	_, err := Build(w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown job")
}

func TestBuild_JobNameUsed(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Name: "Build Project", Steps: []workflow.Step{{Run: "echo hello"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)
	assert.Equal(t, "Build Project", g.Nodes["build"].JobName)
}

func TestBuild_MatrixExpansion(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"test": {
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os":   {"ubuntu", "windows"},
						"node": {14, 16},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 4) // 2x2

	nodes := g.NodesByJobID("test")
	require.Len(t, nodes, 4)

	// All should have the "test" JobID.
	for _, node := range nodes {
		assert.Equal(t, "test", node.JobID)
		assert.NotNil(t, node.MatrixValues)
		assert.Empty(t, node.DependsOn)
	}
}

func TestBuild_MatrixJobWithDependencies(t *testing.T) {
	// Matrix job depends on a non-matrix job.
	w := makeWorkflow(map[string]*workflow.Job{
		"setup": {Steps: []workflow.Step{{Run: "echo setup"}}},
		"test": {
			Needs: workflow.StringOrSlice{"setup"},
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os": {"ubuntu", "windows"},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 3) // 1 setup + 2 test

	for _, node := range g.NodesByJobID("test") {
		assert.Equal(t, []string{"setup"}, node.DependsOn)
	}
}

func TestBuild_NonMatrixDependsOnMatrixJob(t *testing.T) {
	// Non-matrix job depends on a matrix job.
	w := makeWorkflow(map[string]*workflow.Job{
		"test": {
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os": {"ubuntu", "windows"},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
		"deploy": {
			Needs: workflow.StringOrSlice{"test"},
			Steps: []workflow.Step{{Run: "echo deploy"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 3) // 2 test + 1 deploy

	deployNode := g.Nodes["deploy"]
	require.NotNil(t, deployNode)
	// Deploy depends on ALL test matrix nodes.
	assert.Len(t, deployNode.DependsOn, 2)
}

func TestBuild_MatrixJobIndexAndTotal(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"test": {
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os": {"ubuntu", "windows", "macos"},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	nodes := g.NodesByJobID("test")
	require.Len(t, nodes, 3)

	for _, node := range nodes {
		assert.Equal(t, 3, node.JobTotal, "all nodes should have JobTotal=3")
		assert.GreaterOrEqual(t, node.JobIndex, 0)
		assert.Less(t, node.JobIndex, 3)
	}

	// Each node should have a unique JobIndex.
	indices := map[int]bool{}
	for _, node := range nodes {
		indices[node.JobIndex] = true
	}
	assert.Len(t, indices, 3, "all indices should be unique")
}

func TestBuild_NonMatrixJobIndexAndTotal(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	node := g.Nodes["build"]
	assert.Equal(t, 0, node.JobIndex)
	assert.Equal(t, 1, node.JobTotal)
}

func TestBuild_MatrixNodeIDFormat(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"test": {
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os":   {"ubuntu"},
						"node": {16},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 1)

	// Keys should be sorted alphabetically in the node ID.
	expectedID := "test (node: 16, os: ubuntu)"
	_, ok := g.Nodes[expectedID]
	assert.True(t, ok, "expected node ID %q, got nodes: %v", expectedID, nodeIDs(g))
}

// ---------------------------------------------------------------------------
// Validate (cycle detection) tests
// ---------------------------------------------------------------------------

func TestValidate_Valid(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)
	assert.NoError(t, g.Validate())
}

func TestValidate_SelfCycle(t *testing.T) {
	// Manually construct a self-cycle since Build wouldn't allow it normally.
	g := &Graph{
		Nodes: map[string]*JobNode{
			"a": {NodeID: "a", JobID: "a", DependsOn: []string{"a"}, Job: &workflow.Job{}},
		},
	}
	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
	assert.Contains(t, err.Error(), "a -> a")
}

func TestValidate_TwoNodeCycle(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"a": {NodeID: "a", JobID: "a", DependsOn: []string{"b"}, Job: &workflow.Job{}},
			"b": {NodeID: "b", JobID: "b", DependsOn: []string{"a"}, Job: &workflow.Job{}},
		},
	}
	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

func TestValidate_ThreeNodeCycle(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"a": {NodeID: "a", JobID: "a", DependsOn: []string{"c"}, Job: &workflow.Job{}},
			"b": {NodeID: "b", JobID: "b", DependsOn: []string{"a"}, Job: &workflow.Job{}},
			"c": {NodeID: "c", JobID: "c", DependsOn: []string{"b"}, Job: &workflow.Job{}},
		},
	}
	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

// ---------------------------------------------------------------------------
// Topological sort tests
// ---------------------------------------------------------------------------

func TestTopologicalSort_LinearChain(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Needs: workflow.StringOrSlice{"b"}, Steps: []workflow.Step{{Run: "echo c"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)
	require.Len(t, sorted, 3)
	assert.Equal(t, "a", sorted[0].NodeID)
	assert.Equal(t, "b", sorted[1].NodeID)
	assert.Equal(t, "c", sorted[2].NodeID)
}

func TestTopologicalSort_Diamond(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo c"}}},
		"d": {Needs: workflow.StringOrSlice{"b", "c"}, Steps: []workflow.Step{{Run: "echo d"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)
	require.Len(t, sorted, 4)

	// a must be first, d must be last, b and c in between.
	assert.Equal(t, "a", sorted[0].NodeID)
	assert.Equal(t, "d", sorted[3].NodeID)

	// b and c must both appear before d.
	bIdx, cIdx := -1, -1
	for i, node := range sorted {
		if node.NodeID == "b" {
			bIdx = i
		}
		if node.NodeID == "c" {
			cIdx = i
		}
	}
	assert.Greater(t, bIdx, 0, "b should be after a")
	assert.Greater(t, cIdx, 0, "c should be after a")
	assert.Less(t, bIdx, 3, "b should be before d")
	assert.Less(t, cIdx, 3, "c should be before d")
}

func TestTopologicalSort_IndependentJobs(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Steps: []workflow.Step{{Run: "echo c"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)
	require.Len(t, sorted, 3)

	// All should be in output (deterministic order by NodeID).
	assert.Equal(t, "a", sorted[0].NodeID)
	assert.Equal(t, "b", sorted[1].NodeID)
	assert.Equal(t, "c", sorted[2].NodeID)
}

func TestTopologicalSort_Cycle(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"a": {NodeID: "a", JobID: "a", DependsOn: []string{"b"}, Job: &workflow.Job{}},
			"b": {NodeID: "b", JobID: "b", DependsOn: []string{"a"}, Job: &workflow.Job{}},
		},
	}
	_, err := g.TopologicalSort()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

// ---------------------------------------------------------------------------
// NodesByJobID tests
// ---------------------------------------------------------------------------

func TestNodesByJobID(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"test": {
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os": {"ubuntu", "windows"},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
		"deploy": {Steps: []workflow.Step{{Run: "echo deploy"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	testNodes := g.NodesByJobID("test")
	assert.Len(t, testNodes, 2)

	deployNodes := g.NodesByJobID("deploy")
	assert.Len(t, deployNodes, 1)

	noneNodes := g.NodesByJobID("nonexistent")
	assert.Len(t, noneNodes, 0)
}

// ---------------------------------------------------------------------------
// Parallel groups tests
// ---------------------------------------------------------------------------

func TestParallelGroups_LinearChain(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Needs: workflow.StringOrSlice{"b"}, Steps: []workflow.Step{{Run: "echo c"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	groups, err := g.ParallelGroups()
	require.NoError(t, err)
	require.Len(t, groups, 3)

	assert.Len(t, groups[0].Nodes, 1)
	assert.Equal(t, "a", groups[0].Nodes[0].NodeID)
	assert.Len(t, groups[1].Nodes, 1)
	assert.Equal(t, "b", groups[1].Nodes[0].NodeID)
	assert.Len(t, groups[2].Nodes, 1)
	assert.Equal(t, "c", groups[2].Nodes[0].NodeID)
}

func TestParallelGroups_IndependentJobs(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Steps: []workflow.Step{{Run: "echo c"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	groups, err := g.ParallelGroups()
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Len(t, groups[0].Nodes, 3)
}

func TestParallelGroups_Diamond(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
		"c": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo c"}}},
		"d": {Needs: workflow.StringOrSlice{"b", "c"}, Steps: []workflow.Step{{Run: "echo d"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	groups, err := g.ParallelGroups()
	require.NoError(t, err)
	require.Len(t, groups, 3)

	// Group 0: [a]
	assert.Len(t, groups[0].Nodes, 1)
	assert.Equal(t, "a", groups[0].Nodes[0].NodeID)

	// Group 1: [b, c]
	assert.Len(t, groups[1].Nodes, 2)
	groupOneIDs := []string{groups[1].Nodes[0].NodeID, groups[1].Nodes[1].NodeID}
	assert.Contains(t, groupOneIDs, "b")
	assert.Contains(t, groupOneIDs, "c")

	// Group 2: [d]
	assert.Len(t, groups[2].Nodes, 1)
	assert.Equal(t, "d", groups[2].Nodes[0].NodeID)
}

func TestParallelGroups_Cycle(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"a": {NodeID: "a", JobID: "a", DependsOn: []string{"b"}, Job: &workflow.Job{}},
			"b": {NodeID: "b", JobID: "b", DependsOn: []string{"a"}, Job: &workflow.Job{}},
		},
	}
	_, err := g.ParallelGroups()
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Plan tests
// ---------------------------------------------------------------------------

func TestPlan_AllIncluded(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	assert.Len(t, plan.Skipped, 0)
	assert.Len(t, plan.Groups, 2)
}

func TestPlan_JobSkippedByIfFalse(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {If: "false", Steps: []workflow.Step{{Run: "echo a"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	assert.Len(t, plan.Skipped, 1)
	assert.Equal(t, "a", plan.Skipped[0].NodeID)
	assert.Len(t, plan.Groups, 0)
}

func TestPlan_JobIncludedByIfTrue(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {If: "true", Steps: []workflow.Step{{Run: "echo a"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	assert.Len(t, plan.Skipped, 0)
	assert.Len(t, plan.Groups, 1)
}

func TestPlan_DependencySkippedCascades(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {If: "false", Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {Needs: workflow.StringOrSlice{"a"}, Steps: []workflow.Step{{Run: "echo b"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	assert.Len(t, plan.Skipped, 2)
	assert.Len(t, plan.Groups, 0)
}

func TestPlan_AlwaysNotSkippedWhenDependencySkipped(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"a": {If: "false", Steps: []workflow.Step{{Run: "echo a"}}},
		"b": {
			Needs: workflow.StringOrSlice{"a"},
			If:    "always()",
			Steps: []workflow.Step{{Run: "echo b"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	// a is skipped, but b has always() so it should not be skipped.
	assert.Len(t, plan.Skipped, 1)
	assert.Equal(t, "a", plan.Skipped[0].NodeID)

	// b should be in a group.
	totalNodes := 0
	for _, group := range plan.Groups {
		totalNodes += len(group.Nodes)
	}
	assert.Equal(t, 1, totalNodes)
}

func TestPlan_IfWithContextExpression(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"deploy": {
			If:    "github.ref == 'refs/heads/main'",
			Steps: []workflow.Step{{Run: "echo deploy"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	// Test with matching context.
	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/main"),
		}),
	}
	plan, err := g.Plan(ctx)
	require.NoError(t, err)
	assert.Len(t, plan.Skipped, 0)
	assert.Len(t, plan.Groups, 1)

	// Test with non-matching context.
	ctx2 := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/develop"),
		}),
	}
	plan2, err := g.Plan(ctx2)
	require.NoError(t, err)
	assert.Len(t, plan2.Skipped, 1)
	assert.Len(t, plan2.Groups, 0)
}

func TestPlan_EmptyGraph(t *testing.T) {
	g := &Graph{Nodes: map[string]*JobNode{}}
	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	assert.Len(t, plan.Groups, 0)
	assert.Len(t, plan.Skipped, 0)
}

func TestPlan_MatrixJobWithIf(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"test": {
			If: "false",
			Strategy: &workflow.Strategy{
				Matrix: &workflow.Matrix{
					Dimensions: map[string][]interface{}{
						"os": {"ubuntu", "windows"},
					},
				},
			},
			Steps: []workflow.Step{{Run: "echo test"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	// Both matrix nodes should be skipped.
	assert.Len(t, plan.Skipped, 2)
	assert.Len(t, plan.Groups, 0)
}

// ---------------------------------------------------------------------------
// Runtime evaluation deferral tests
// ---------------------------------------------------------------------------

func TestPlan_DeferredRuntimeEval_NeedsRef(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
		"deploy": {
			Needs: workflow.StringOrSlice{"build"},
			If:    "needs.build.result == 'success'",
			Steps: []workflow.Step{{Run: "echo deploy"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)

	// deploy should NOT be skipped — it should be deferred to runtime.
	assert.Len(t, plan.Skipped, 0)
	assert.Len(t, plan.Groups, 2)

	// The deploy node should be marked for runtime evaluation.
	deployNode := g.Nodes["deploy"]
	assert.True(t, deployNode.NeedsRuntimeEval, "deploy should need runtime eval")
}

func TestPlan_DeferredRuntimeEval_Failure(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
		"cleanup": {
			Needs: workflow.StringOrSlice{"build"},
			If:    "failure()",
			Steps: []workflow.Step{{Run: "echo cleanup"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)

	// cleanup should NOT be skipped at plan time — failure() needs runtime eval.
	assert.Len(t, plan.Skipped, 0)

	cleanupNode := g.Nodes["cleanup"]
	assert.True(t, cleanupNode.NeedsRuntimeEval, "cleanup should need runtime eval")
}

func TestPlan_DeferredRuntimeEval_Success(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
		"deploy": {
			Needs: workflow.StringOrSlice{"build"},
			If:    "success()",
			Steps: []workflow.Step{{Run: "echo deploy"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)

	assert.Len(t, plan.Skipped, 0)
	deployNode := g.Nodes["deploy"]
	assert.True(t, deployNode.NeedsRuntimeEval, "deploy with success() should need runtime eval")
}

func TestPlan_DeferredRuntimeEval_Cancelled(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
		"notify": {
			Needs: workflow.StringOrSlice{"build"},
			If:    "cancelled()",
			Steps: []workflow.Step{{Run: "echo notify"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)

	assert.Len(t, plan.Skipped, 0)
	notifyNode := g.Nodes["notify"]
	assert.True(t, notifyNode.NeedsRuntimeEval, "notify with cancelled() should need runtime eval")
}

func TestPlan_AlwaysNotDeferred(t *testing.T) {
	// always() evaluates to true at plan time — no deferral needed.
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
		"cleanup": {
			Needs: workflow.StringOrSlice{"build"},
			If:    "always()",
			Steps: []workflow.Step{{Run: "echo cleanup"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)

	assert.Len(t, plan.Skipped, 0)
	cleanupNode := g.Nodes["cleanup"]
	assert.False(t, cleanupNode.NeedsRuntimeEval, "always() should not be deferred")
}

func TestPlan_StaticFalseNotDeferred(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"skip": {If: "false", Steps: []workflow.Step{{Run: "echo skip"}}},
	})
	g, err := Build(w)
	require.NoError(t, err)

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)

	assert.Len(t, plan.Skipped, 1)
	assert.Len(t, plan.Groups, 0)
	assert.False(t, g.Nodes["skip"].NeedsRuntimeEval)
}

func TestPlan_CompoundConditionWithNeeds(t *testing.T) {
	w := makeWorkflow(map[string]*workflow.Job{
		"build": {Steps: []workflow.Step{{Run: "echo build"}}},
		"deploy": {
			Needs: workflow.StringOrSlice{"build"},
			If:    "github.ref == 'refs/heads/main' && needs.build.result == 'success'",
			Steps: []workflow.Step{{Run: "echo deploy"}},
		},
	})
	g, err := Build(w)
	require.NoError(t, err)

	ctx := expression.MapContext{
		"github": expression.Object(map[string]expression.Value{
			"ref": expression.String("refs/heads/main"),
		}),
	}
	plan, err := g.Plan(ctx)
	require.NoError(t, err)

	// Should be deferred because it references needs.
	assert.Len(t, plan.Skipped, 0)
	deployNode := g.Nodes["deploy"]
	assert.True(t, deployNode.NeedsRuntimeEval)
}

func TestNeedsRuntimeEval(t *testing.T) {
	tests := []struct {
		name     string
		ifCond   string
		expected bool
	}{
		{"empty", "", false},
		{"static true", "true", false},
		{"static false", "false", false},
		{"github ref", "github.ref == 'refs/heads/main'", false},
		{"needs ref", "needs.build.result == 'success'", true},
		{"failure()", "failure()", true},
		{"success()", "success()", true},
		{"cancelled()", "cancelled()", true},
		{"always()", "always()", false},
		{"compound with needs", "github.ref == 'main' && needs.build.result == 'success'", true},
		{"failure() with always()", "failure() || always()", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, needsRuntimeEval(tt.ifCond))
		})
	}
}

// ---------------------------------------------------------------------------
// Matrix edge cases — comboMatchesEntry, matchesOnKeys
// ---------------------------------------------------------------------------

func TestExpandMatrix_AllExcluded(t *testing.T) {
	// All combos are excluded — should return nil.
	m := &workflow.Matrix{
		Dimensions: map[string][]interface{}{
			"os": {"ubuntu"},
		},
		Exclude: []map[string]interface{}{
			{"os": "ubuntu"},
		},
	}
	combos := ExpandMatrix(m)
	assert.Nil(t, combos)
}

func TestComboMatchesEntry_MissingKey(t *testing.T) {
	combo := MatrixCombination{"os": "ubuntu"}
	entry := map[string]any{"os": "ubuntu", "version": "20.04"}
	// Combo doesn't have "version", so it shouldn't match.
	assert.False(t, comboMatchesEntry(combo, entry))
}

func TestComboMatchesEntry_ValueMismatch(t *testing.T) {
	combo := MatrixCombination{"os": "ubuntu", "version": "18.04"}
	entry := map[string]any{"os": "ubuntu", "version": "20.04"}
	assert.False(t, comboMatchesEntry(combo, entry))
}

func TestMatchesOnKeys_ValueMismatch(t *testing.T) {
	combo := MatrixCombination{"os": "ubuntu"}
	entry := map[string]any{"os": "windows"}
	assert.False(t, matchesOnKeys(combo, entry, []string{"os"}))
}

func TestMatchesOnKeys_MissingKeyInCombo(t *testing.T) {
	combo := MatrixCombination{}
	entry := map[string]any{"os": "ubuntu"}
	assert.False(t, matchesOnKeys(combo, entry, []string{"os"}))
}

func TestMatchesOnKeys_MissingKeyInEntry(t *testing.T) {
	combo := MatrixCombination{"os": "ubuntu"}
	entry := map[string]any{}
	assert.False(t, matchesOnKeys(combo, entry, []string{"os"}))
}

func TestCartesianProduct_EmptyDims(t *testing.T) {
	result := cartesianProduct(nil)
	assert.Nil(t, result)
}

// ---------------------------------------------------------------------------
// Plan — custom functions, static eval error, ParallelGroups error
// ---------------------------------------------------------------------------

func TestPlan_WithCustomFunctions(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"job1": {
				NodeID:  "job1",
				JobID:   "job1",
				JobName: "build",
				Job: &workflow.Job{
					If: "custom_fn()",
				},
			},
		},
	}

	customFns := expression.BuiltinFunctions()
	customFns["custom_fn"] = func(args []expression.Value) (expression.Value, error) {
		return expression.Bool(true), nil
	}

	plan, err := g.Plan(expression.MapContext{}, PlanOptions{Functions: customFns})
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Len(t, plan.Groups, 1)
	assert.Len(t, plan.Groups[0].Nodes, 1)
	assert.Empty(t, plan.Skipped)
}

func TestPlan_StaticEvalError(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"job1": {
				NodeID:  "job1",
				JobID:   "job1",
				JobName: "build",
				Job: &workflow.Job{
					If: "invalid_expression(!!!",
				},
			},
		},
	}

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	require.NotNil(t, plan)
	// Job should be skipped because expression evaluation failed.
	assert.Empty(t, plan.Groups)
	assert.Len(t, plan.Skipped, 1)
}

func TestPlan_StaticEvalFalse(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"job1": {
				NodeID:  "job1",
				JobID:   "job1",
				JobName: "build",
				Job: &workflow.Job{
					If: "false",
				},
			},
		},
	}

	plan, err := g.Plan(expression.MapContext{})
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Empty(t, plan.Groups)
	assert.Len(t, plan.Skipped, 1)
}

func TestPlan_CircularDependency(t *testing.T) {
	g := &Graph{
		Nodes: map[string]*JobNode{
			"a": {NodeID: "a", JobID: "a", JobName: "A", Job: &workflow.Job{}, DependsOn: []string{"b"}},
			"b": {NodeID: "b", JobID: "b", JobName: "B", Job: &workflow.Job{}, DependsOn: []string{"a"}},
		},
	}

	_, err := g.Plan(expression.MapContext{})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nodeIDs(g *Graph) []string {
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	return ids
}
