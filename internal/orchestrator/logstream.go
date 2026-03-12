package orchestrator

import (
	"fmt"
	"io"
	"os"
	"strings"
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
	masker      *SecretMasker
	verbose     bool
	writer      io.Writer
	colors      map[string]*color.Color
	annotations []Annotation
	mu          sync.Mutex
	colorIdx    int
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

// Annotation represents a workflow command annotation (::error::, ::warning::, ::notice::).
type Annotation struct {
	Level   string // "error", "warning", "notice"
	Message string
	File    string
	Line    string
	Col     string
	NodeID  string
}

// StepOutput logs a line of step output. Parses workflow commands
// (::error::, ::warning::, ::notice::, ::group::, ::endgroup::, ::debug::).
func (l *LogStreamer) StepOutput(nodeID, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	trimmed := strings.TrimSpace(line)

	// Parse workflow commands.
	if cmd, params, msg, ok := parseWorkflowCommand(trimmed); ok {
		switch cmd {
		case "error":
			ann := Annotation{Level: "error", Message: msg, NodeID: nodeID}
			applyAnnotationParams(&ann, params)
			l.annotations = append(l.annotations, ann)
			errColor := color.New(color.FgRed, color.Bold)
			prefix := l.jobColor(nodeID).Sprintf("[%s]", nodeID)
			loc := annotationLocation(ann)
			fmt.Fprintf(l.writer, "%s  %s %s%s\n", prefix, errColor.Sprint("Error:"), msg, loc)
			return
		case "warning":
			ann := Annotation{Level: "warning", Message: msg, NodeID: nodeID}
			applyAnnotationParams(&ann, params)
			l.annotations = append(l.annotations, ann)
			warnColor := color.New(color.FgYellow, color.Bold)
			prefix := l.jobColor(nodeID).Sprintf("[%s]", nodeID)
			loc := annotationLocation(ann)
			fmt.Fprintf(l.writer, "%s  %s %s%s\n", prefix, warnColor.Sprint("Warning:"), msg, loc)
			return
		case "notice":
			ann := Annotation{Level: "notice", Message: msg, NodeID: nodeID}
			applyAnnotationParams(&ann, params)
			l.annotations = append(l.annotations, ann)
			noticeColor := color.New(color.FgCyan)
			prefix := l.jobColor(nodeID).Sprintf("[%s]", nodeID)
			loc := annotationLocation(ann)
			fmt.Fprintf(l.writer, "%s  %s %s%s\n", prefix, noticeColor.Sprint("Notice:"), msg, loc)
			return
		case "debug":
			if l.verbose {
				dimColor := color.New(color.Faint)
				prefix := l.jobColor(nodeID).Sprintf("[%s]", nodeID)
				fmt.Fprintf(l.writer, "%s  %s\n", prefix, dimColor.Sprint(msg))
			}
			return
		case "group":
			l.writeLine(nodeID, "  >> "+msg)
			return
		case "endgroup":
			return // silently consume
		case "add-mask":
			if l.masker != nil && msg != "" {
				l.masker.AddSecret(msg)
			}
			return
		}
	}

	l.writeLine(nodeID, "  "+line)
}

// Annotations returns all collected annotations.
func (l *LogStreamer) Annotations() []Annotation {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Annotation(nil), l.annotations...)
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

// Summary prints a final summary of all job results including any annotations.
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

	// Print annotation summary if any were collected.
	if len(l.annotations) > 0 {
		var errors, warnings int
		for _, ann := range l.annotations {
			switch ann.Level {
			case "error":
				errors++
			case "warning":
				warnings++
			}
		}
		fmt.Fprintln(l.writer)
		if errors > 0 {
			errColor := color.New(color.FgRed)
			errColor.Fprintf(l.writer, "  %d error(s)", errors)
		}
		if warnings > 0 {
			if errors > 0 {
				fmt.Fprint(l.writer, ", ")
			} else {
				fmt.Fprint(l.writer, "  ")
			}
			warnColor := color.New(color.FgYellow)
			warnColor.Fprintf(l.writer, "%d warning(s)", warnings)
		}
		fmt.Fprintln(l.writer)
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

// parseWorkflowCommand parses a GitHub Actions workflow command from a log line.
// Format: ::command param1=val1,param2=val2::message
// Returns (command, params, message, ok).
func parseWorkflowCommand(line string) (string, map[string]string, string, bool) {
	if !strings.HasPrefix(line, "::") {
		return "", nil, "", false
	}

	// Find the second :: that separates command+params from message.
	rest := line[2:]
	idx := strings.Index(rest, "::")
	if idx < 0 {
		return "", nil, "", false
	}

	cmdPart := rest[:idx]
	msg := rest[idx+2:]

	// Split command from params: "error file=foo.go,line=10" or just "error"
	cmd := cmdPart
	params := make(map[string]string)

	if spaceIdx := strings.IndexByte(cmdPart, ' '); spaceIdx >= 0 {
		cmd = cmdPart[:spaceIdx]
		paramStr := cmdPart[spaceIdx+1:]
		for _, p := range strings.Split(paramStr, ",") {
			k, v, ok := strings.Cut(p, "=")
			if ok {
				params[k] = v
			}
		}
	}

	return cmd, params, msg, true
}

// applyAnnotationParams fills annotation fields from command parameters.
func applyAnnotationParams(ann *Annotation, params map[string]string) {
	if v, ok := params["file"]; ok {
		ann.File = v
	}
	if v, ok := params["line"]; ok {
		ann.Line = v
	}
	if v, ok := params["col"]; ok {
		ann.Col = v
	}
}

// annotationLocation formats a file:line:col location string for display.
func annotationLocation(ann Annotation) string {
	if ann.File == "" {
		return ""
	}
	loc := " (" + ann.File
	if ann.Line != "" {
		loc += ":" + ann.Line
		if ann.Col != "" {
			loc += ":" + ann.Col
		}
	}
	loc += ")"
	return loc
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := d.Seconds() - float64(minutes)*60
	return fmt.Sprintf("%dm %.0fs", minutes, seconds)
}
