package orchestrator

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fatih/color"
)

// JobRunResult holds the outcome of a single job execution.
type JobRunResult struct {
	NodeID   string
	Status   string // "success", "failure", "skipped", "cancelled"
	Duration time.Duration
	Outputs  map[string]string
}

// LogStreamer formats and outputs job/step execution logs.
type LogStreamer struct {
	masker  *SecretMasker
	verbose bool
	writer  io.Writer
	colors  map[string]*color.Color
	mu      sync.Mutex
	colorIdx int
}

var jobColors = []*color.Color{
	color.New(color.FgCyan),
	color.New(color.FgYellow),
	color.New(color.FgGreen),
	color.New(color.FgMagenta),
	color.New(color.FgBlue),
	color.New(color.FgRed),
}

// NewLogStreamer creates a new log streamer.
func NewLogStreamer(masker *SecretMasker, verbose bool) *LogStreamer {
	return &LogStreamer{
		masker:  masker,
		verbose: verbose,
		writer:  os.Stdout,
		colors:  make(map[string]*color.Color),
	}
}

// SetWriter sets the output writer (for testing).
func (l *LogStreamer) SetWriter(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writer = w
}

// jobColor returns a consistent color for a given job nodeID.
func (l *LogStreamer) jobColor(nodeID string) *color.Color {
	if c, ok := l.colors[nodeID]; ok {
		return c
	}
	c := jobColors[l.colorIdx%len(jobColors)]
	l.colorIdx++
	l.colors[nodeID] = c
	return c
}

// writeLine writes a formatted log line with the job prefix.
func (l *LogStreamer) writeLine(nodeID, text string) {
	masked := l.masker.Mask(text)
	c := l.jobColor(nodeID)
	prefix := c.Sprintf("[%s]", nodeID)
	fmt.Fprintf(l.writer, "%s %s\n", prefix, masked)
}

// JobStarted logs the start of a job.
func (l *LogStreamer) JobStarted(nodeID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeLine(nodeID, "Job started")
}

// StepStarted logs the start of a step.
func (l *LogStreamer) StepStarted(nodeID, stepName string, stepNum, totalSteps int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeLine(nodeID, fmt.Sprintf("Step %d/%d: %s", stepNum, totalSteps, stepName))
}

// StepOutput logs a line of step output.
func (l *LogStreamer) StepOutput(nodeID, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeLine(nodeID, "  "+line)
}

// StepCompleted logs step completion with status and duration.
func (l *LogStreamer) StepCompleted(nodeID, stepName, status string, duration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	indicator := statusIndicator(status)
	l.writeLine(nodeID, fmt.Sprintf("Step completed: %s %s (%s)", stepName, indicator, formatDuration(duration)))
}

// JobCompleted logs job completion with overall status and duration.
func (l *LogStreamer) JobCompleted(nodeID, status string, duration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var label string
	switch status {
	case "success":
		label = "Job succeeded"
	case "failure":
		label = "Job failed"
	case "cancelled":
		label = "Job cancelled"
	default:
		label = "Job completed"
	}
	l.writeLine(nodeID, fmt.Sprintf("%s (%s)", label, formatDuration(duration)))
}

// Summary prints a final summary of all job results.
func (l *LogStreamer) Summary(results map[string]*JobRunResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.writer)
	fmt.Fprintln(l.writer, "Summary:")
	for _, r := range results {
		indicator := statusIndicator(r.Status)
		switch r.Status {
		case "skipped", "cancelled":
			fmt.Fprintf(l.writer, "  %s %s (%s)\n", indicator, r.NodeID, r.Status)
		default:
			fmt.Fprintf(l.writer, "  %s %s (%s)\n", indicator, r.NodeID, formatDuration(r.Duration))
		}
	}
}

func statusIndicator(status string) string {
	switch status {
	case "success":
		return "\u2713"
	case "failure":
		return "\u2717"
	case "skipped", "cancelled":
		return "\u2298"
	default:
		return "?"
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := d.Seconds() - float64(minutes)*60
	return fmt.Sprintf("%dm %.0fs", minutes, seconds)
}
