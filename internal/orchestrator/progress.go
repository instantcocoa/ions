package orchestrator

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
)

// ProgressUI provides a compact, in-place terminal progress display for
// workflow execution. When stdout is a TTY and verbose mode is off, it
// shows a dashboard with job/step status that updates in place. When
// stdout is not a TTY or verbose is on, it falls back to the normal
// LogStreamer behavior.
type ProgressUI struct {
	mu       sync.Mutex
	writer   io.Writer
	isTTY    bool
	jobs     []*jobProgress // ordered list of jobs
	jobIndex map[string]int // nodeID → index into jobs
	started  time.Time
	done     bool
	stopTick chan struct{}  // closed to stop the refresh ticker
}

type jobProgress struct {
	nodeID      string
	status      string // "pending", "running", "success", "failure", "skipped", "cancelled"
	currentStep string // name of the currently running step
	stepsDone   int
	stepsTotal  int
	startTime   time.Time
	endTime     time.Time
	lastLines   []string // last few log lines (for non-verbose mode)
}

// NewProgressUI creates a new progress UI. If stdout is a TTY and verbose
// is false, it shows a compact dashboard. Otherwise it returns nil to
// signal the caller should use the standard LogStreamer.
func NewProgressUI() *ProgressUI {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return nil
	}
	p := &ProgressUI{
		writer:   os.Stdout,
		isTTY:    true,
		jobIndex: make(map[string]int),
		started:  time.Now(),
		stopTick: make(chan struct{}),
	}

	// Start a background ticker to refresh the spinner animation
	// and elapsed time display.
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.mu.Lock()
				if !p.done && len(p.jobs) > 0 {
					p.render()
				}
				p.mu.Unlock()
			case <-p.stopTick:
				return
			}
		}
	}()

	return p
}

// RegisterJobs registers all job node IDs so the progress UI knows the
// total set of jobs to display.
func (p *ProgressUI) RegisterJobs(nodeIDs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range nodeIDs {
		if _, ok := p.jobIndex[id]; !ok {
			p.jobIndex[id] = len(p.jobs)
			p.jobs = append(p.jobs, &jobProgress{
				nodeID: id,
				status: "pending",
			})
		}
	}
}

// JobStarted marks a job as running.
func (p *ProgressUI) JobStarted(nodeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j := p.getJob(nodeID); j != nil {
		j.status = "running"
		j.startTime = time.Now()
	}
	p.render()
}

// StepUpdate records a step status change for a job.
func (p *ProgressUI) StepUpdate(nodeID, stepName, state string, result *string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j := p.getJob(nodeID)
	if j == nil {
		return
	}
	switch state {
	case "InProgress":
		j.currentStep = stepName
		j.stepsTotal++ // increment as we discover steps
	case "Completed":
		j.stepsDone++
		r := "succeeded"
		if result != nil {
			r = *result
		}
		j.currentStep = fmt.Sprintf("%s (%s)", stepName, r)
	}
	p.render()
}

// JobCompleted marks a job as completed.
func (p *ProgressUI) JobCompleted(nodeID, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j := p.getJob(nodeID); j != nil {
		j.status = status
		j.endTime = time.Now()
		j.currentStep = ""
	}
	p.render()
}

// LogLine records a log line for a job (kept for error display).
func (p *ProgressUI) LogLine(nodeID, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j := p.getJob(nodeID)
	if j == nil {
		return
	}
	j.lastLines = append(j.lastLines, line)
	if len(j.lastLines) > 5 {
		j.lastLines = j.lastLines[len(j.lastLines)-5:]
	}
}

// Finish clears the progress display and prints the final summary.
func (p *ProgressUI) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	close(p.stopTick)
	// Clear the progress area and print the final state.
	p.clearLines(len(p.jobs) + 2) // +2 for header and blank line
	p.printFinal()
}

func (p *ProgressUI) getJob(nodeID string) *jobProgress {
	if idx, ok := p.jobIndex[nodeID]; ok {
		return p.jobs[idx]
	}
	return nil
}

// render clears the previous output and redraws the progress display.
func (p *ProgressUI) render() {
	if p.done {
		return
	}

	lines := len(p.jobs) + 2 // jobs + header + separator
	p.clearLines(lines)

	bold := color.New(color.Bold)
	dim := color.New(color.Faint)

	// Header.
	elapsed := time.Since(p.started)
	running := 0
	completed := 0
	failed := 0
	for _, j := range p.jobs {
		switch j.status {
		case "running":
			running++
		case "success":
			completed++
		case "failure":
			failed++
		}
	}
	header := fmt.Sprintf("Jobs: %d total", len(p.jobs))
	if running > 0 {
		header += fmt.Sprintf(", %d running", running)
	}
	if completed > 0 {
		header += fmt.Sprintf(", %d done", completed)
	}
	if failed > 0 {
		header += fmt.Sprintf(", %d failed", failed)
	}
	header += fmt.Sprintf(" (%s)", formatDuration(elapsed))
	bold.Fprintln(p.writer, header)
	fmt.Fprintln(p.writer)

	// Job list.
	for _, j := range p.jobs {
		p.renderJob(j, dim)
	}
}

func (p *ProgressUI) renderJob(j *jobProgress, dim *color.Color) {
	var icon string
	var nameColor *color.Color

	switch j.status {
	case "pending":
		icon = "\u25cb" // ○
		nameColor = dim
	case "running":
		icon = spinnerFrame()
		nameColor = color.New(color.FgCyan, color.Bold)
	case "success":
		icon = color.GreenString("\u2713") // ✓
		nameColor = color.New(color.FgGreen)
	case "failure":
		icon = color.RedString("\u2717") // ✗
		nameColor = color.New(color.FgRed)
	case "skipped":
		icon = color.YellowString("\u2298") // ⊘
		nameColor = dim
	case "cancelled":
		icon = color.YellowString("\u2298")
		nameColor = dim
	default:
		icon = "?"
		nameColor = dim
	}

	line := fmt.Sprintf(" %s %s", icon, nameColor.Sprint(j.nodeID))

	switch j.status {
	case "running":
		if j.currentStep != "" {
			line += dim.Sprintf("  %s", j.currentStep)
		}
		elapsed := time.Since(j.startTime)
		line += dim.Sprintf("  %s", formatDuration(elapsed))
	case "success", "failure":
		dur := j.endTime.Sub(j.startTime)
		line += dim.Sprintf("  %s", formatDuration(dur))
	}

	fmt.Fprintln(p.writer, line)
}

func (p *ProgressUI) clearLines(n int) {
	if !p.isTTY || n == 0 {
		return
	}
	// Move cursor up n lines and clear each line.
	for i := 0; i < n; i++ {
		fmt.Fprintf(p.writer, "\033[A\033[2K")
	}
	fmt.Fprintf(p.writer, "\r")
}

func (p *ProgressUI) printFinal() {
	bold := color.New(color.Bold)
	dim := color.New(color.Faint)

	elapsed := time.Since(p.started)
	bold.Fprintf(p.writer, "Workflow completed in %s\n", formatDuration(elapsed))
	fmt.Fprintln(p.writer)

	for _, j := range p.jobs {
		p.renderJob(j, dim)
		// Show last log lines for failed jobs.
		if j.status == "failure" && len(j.lastLines) > 0 {
			for _, line := range j.lastLines {
				fmt.Fprintf(p.writer, "    %s\n", line)
			}
		}
	}
}

// spinnerFrames for the running indicator.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame() string {
	idx := int(time.Now().UnixMilli()/100) % len(spinnerFrames)
	return color.CyanString(spinnerFrames[idx])
}

// ProgressLogger wraps LogStreamer to also update the ProgressUI.
// When a ProgressUI is active, it intercepts log calls to update
// the dashboard display. The underlying LogStreamer still receives
// all calls for potential verbose output.
type ProgressLogger struct {
	streamer *LogStreamer
	progress *ProgressUI
}

// NewProgressLogger creates a logger that updates both the streamer
// and the progress UI.
func NewProgressLogger(masker *SecretMasker, verbose bool) (*ProgressLogger, *ProgressUI) {
	streamer := NewLogStreamer(masker, verbose)

	// Only use the progress UI when not in verbose mode and stdout is a TTY.
	var progress *ProgressUI
	if !verbose {
		progress = NewProgressUI()
	}

	if progress != nil {
		// Redirect the log streamer to discard — the progress UI handles display.
		streamer.SetWriter(io.Discard)
	}

	return &ProgressLogger{
		streamer: streamer,
		progress: progress,
	}, progress
}

// Streamer returns the underlying LogStreamer for use by the orchestrator.
func (pl *ProgressLogger) Streamer() *LogStreamer {
	return pl.streamer
}

// HasProgress returns true if the progress UI is active.
func (pl *ProgressLogger) HasProgress() bool {
	return pl.progress != nil
}

// buildProgressNodeIDs collects all node IDs from an execution plan.
func buildProgressNodeIDs(groups []struct{ Nodes []string }) []string {
	var ids []string
	for _, g := range groups {
		ids = append(ids, g.Nodes...)
	}
	return ids
}

// MaxWidth returns the maximum display width for job names, used
// to align the progress columns.
func maxWidth(jobs []*jobProgress) int {
	max := 0
	for _, j := range jobs {
		if len(j.nodeID) > max {
			max = len(j.nodeID)
		}
	}
	return max
}

// pad pads a string to the given width.
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
