package orchestrator

import (
	"bytes"
	"testing"
	"time"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Disable color output in tests for deterministic assertions.
	color.NoColor = true
}

func newTestStreamer(secrets map[string]string) (*LogStreamer, *bytes.Buffer) {
	masker := NewSecretMasker(secrets)
	ls := NewLogStreamer(masker, false)
	buf := &bytes.Buffer{}
	ls.SetWriter(buf)
	return ls, buf
}

func TestLogStreamer_JobLifecycle(t *testing.T) {
	ls, buf := newTestStreamer(nil)

	ls.JobStarted("build")
	ls.StepStarted("build", "Checkout", 1, 3)
	ls.StepOutput("build", "Checking out code...")
	ls.StepCompleted("build", "Checkout", "success", 500*time.Millisecond)
	ls.StepStarted("build", "Build", 2, 3)
	ls.StepOutput("build", "Building project...")
	ls.StepCompleted("build", "Build", "success", 2100*time.Millisecond)
	ls.StepStarted("build", "Test", 3, 3)
	ls.StepOutput("build", "Running tests...")
	ls.StepOutput("build", "FAIL: test_foo")
	ls.StepCompleted("build", "Test", "failure", 1300*time.Millisecond)
	ls.JobCompleted("build", "failure", 3900*time.Millisecond)

	output := buf.String()
	assert.Contains(t, output, "[build] Job started")
	assert.Contains(t, output, "[build] Step 1/3: Checkout")
	assert.Contains(t, output, "[build]   Checking out code...")
	assert.Contains(t, output, "[build] Step completed: Checkout \u2713 (0.5s)")
	assert.Contains(t, output, "[build] Step 2/3: Build")
	assert.Contains(t, output, "[build]   Building project...")
	assert.Contains(t, output, "[build] Step completed: Build \u2713 (2.1s)")
	assert.Contains(t, output, "[build] Step 3/3: Test")
	assert.Contains(t, output, "[build]   Running tests...")
	assert.Contains(t, output, "[build]   FAIL: test_foo")
	assert.Contains(t, output, "[build] Step completed: Test \u2717 (1.3s)")
	assert.Contains(t, output, "[build] Job failed (3.9s)")
}

func TestLogStreamer_Summary(t *testing.T) {
	ls, buf := newTestStreamer(nil)

	// Use an ordered slice to test predictable output.
	// Since maps don't guarantee order, we test each line individually.
	results := map[string]*JobRunResult{
		"build": {
			NodeID:   "build",
			Status:   "success",
			Duration: 3900 * time.Millisecond,
		},
		"test": {
			NodeID:   "test",
			Status:   "failure",
			Duration: 1200 * time.Millisecond,
		},
		"deploy": {
			NodeID:   "deploy",
			Status:   "skipped",
			Duration: 0,
		},
	}
	ls.Summary(results)

	output := buf.String()
	assert.Contains(t, output, "Summary:")
	assert.Contains(t, output, "\u2713 build (3.9s)")
	assert.Contains(t, output, "\u2717 test (1.2s)")
	assert.Contains(t, output, "\u2298 deploy (skipped)")
}

func TestLogStreamer_SummaryCancelled(t *testing.T) {
	ls, buf := newTestStreamer(nil)

	results := map[string]*JobRunResult{
		"job1": {
			NodeID:   "job1",
			Status:   "cancelled",
			Duration: 0,
		},
	}
	ls.Summary(results)

	output := buf.String()
	assert.Contains(t, output, "\u2298 job1 (cancelled)")
}

func TestLogStreamer_SecretMaskingInOutput(t *testing.T) {
	ls, buf := newTestStreamer(map[string]string{
		"TOKEN": "supersecret",
	})

	ls.StepOutput("job1", "using token supersecret to authenticate")

	output := buf.String()
	assert.Contains(t, output, "using token *** to authenticate")
	assert.NotContains(t, output, "supersecret")
}

func TestLogStreamer_SecretMaskingInStepName(t *testing.T) {
	ls, buf := newTestStreamer(map[string]string{
		"DB_PASS": "mypass",
	})

	ls.StepStarted("job1", "Connect with mypass", 1, 1)

	output := buf.String()
	assert.Contains(t, output, "Connect with ***")
	assert.NotContains(t, output, "mypass")
}

func TestLogStreamer_DurationFormatting(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"sub-second", 500 * time.Millisecond, "0.5s"},
		{"seconds", 2100 * time.Millisecond, "2.1s"},
		{"exact-seconds", 3 * time.Second, "3.0s"},
		{"just-under-minute", 59900 * time.Millisecond, "59.9s"},
		{"one-minute", 60 * time.Second, "1m 0s"},
		{"minutes-and-seconds", 90 * time.Second, "1m 30s"},
		{"multi-minute", 150 * time.Second, "2m 30s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatDuration(tt.duration))
		})
	}
}

func TestLogStreamer_MultipleJobs(t *testing.T) {
	ls, buf := newTestStreamer(nil)

	ls.JobStarted("build")
	ls.JobStarted("lint")
	ls.StepOutput("build", "compiling...")
	ls.StepOutput("lint", "linting...")

	output := buf.String()
	assert.Contains(t, output, "[build] Job started")
	assert.Contains(t, output, "[lint] Job started")
	assert.Contains(t, output, "[build]   compiling...")
	assert.Contains(t, output, "[lint]   linting...")
}

func TestLogStreamer_JobSucceeded(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.JobCompleted("build", "success", 1*time.Second)
	assert.Contains(t, buf.String(), "[build] Job succeeded (1.0s)")
}

func TestLogStreamer_JobCancelled(t *testing.T) {
	ls, buf := newTestStreamer(nil)
	ls.JobCompleted("deploy", "cancelled", 500*time.Millisecond)
	assert.Contains(t, buf.String(), "[deploy] Job cancelled (0.5s)")
}

func TestLogStreamer_SetWriter(t *testing.T) {
	masker := NewSecretMasker(nil)
	ls := NewLogStreamer(masker, false)

	buf := &bytes.Buffer{}
	ls.SetWriter(buf)

	ls.JobStarted("test")
	require.NotEmpty(t, buf.String())
	assert.Contains(t, buf.String(), "[test] Job started")
}

func TestStatusIndicator(t *testing.T) {
	assert.Equal(t, "\u2713", statusIndicator("success"))
	assert.Equal(t, "\u2717", statusIndicator("failure"))
	assert.Equal(t, "\u2298", statusIndicator("skipped"))
	assert.Equal(t, "\u2298", statusIndicator("cancelled"))
	assert.Equal(t, "?", statusIndicator("unknown"))
}
