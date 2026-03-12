package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)

// ProblemMatcher defines a set of patterns for extracting annotations from log output.
type ProblemMatcher struct {
	Owner   string           `json:"owner"`
	Pattern []MatcherPattern `json:"pattern"`
}

// MatcherPattern is a single regex pattern with named capture groups.
type MatcherPattern struct {
	Regexp   string `json:"regexp"`
	File     int    `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity int    `json:"severity,omitempty"`
	Message  int    `json:"message,omitempty"`
	compiled *regexp.Regexp
}

// problemMatcherFile is the JSON format for matcher files.
type problemMatcherFile struct {
	ProblemMatcher []ProblemMatcher `json:"problemMatcher"`
}

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
	matchers    []ProblemMatcher // active problem matchers
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
		case "set-output", "save-state":
			// Deprecated workflow commands — silently consumed.
			// The runner handles these via GITHUB_OUTPUT and GITHUB_STATE files.
			return
		case "add-matcher":
			if msg != "" {
				if err := l.loadProblemMatcher(msg); err != nil && l.verbose {
					prefix := l.jobColor(nodeID).Sprintf("[%s]", nodeID)
					fmt.Fprintf(l.writer, "%s  Problem matcher load error: %s\n", prefix, err)
				}
			}
			return
		case "remove-matcher":
			owner := msg
			if v, ok := params["owner"]; ok {
				owner = v
			}
			if owner != "" {
				l.removeProblemMatcher(owner)
			}
			return
		case "stop-commands":
			// Token-based command disabling — not implemented, just log.
			return
		}
	}

	// Apply problem matchers to extract annotations from regular output.
	l.applyMatchers(nodeID, trimmed)

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

// loadProblemMatcher loads a problem matcher from a JSON file path.
func (l *LogStreamer) loadProblemMatcher(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading matcher file %s: %w", path, err)
	}

	var mf problemMatcherFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("parsing matcher file %s: %w", path, err)
	}

	for i := range mf.ProblemMatcher {
		pm := &mf.ProblemMatcher[i]
		for j := range pm.Pattern {
			compiled, err := regexp.Compile(pm.Pattern[j].Regexp)
			if err != nil {
				return fmt.Errorf("compiling pattern %q in matcher %s: %w", pm.Pattern[j].Regexp, pm.Owner, err)
			}
			pm.Pattern[j].compiled = compiled
		}
		l.matchers = append(l.matchers, *pm)
	}
	return nil
}

// removeProblemMatcher removes all matchers with the given owner.
func (l *LogStreamer) removeProblemMatcher(owner string) {
	filtered := l.matchers[:0]
	for _, m := range l.matchers {
		if m.Owner != owner {
			filtered = append(filtered, m)
		}
	}
	l.matchers = filtered
}

// applyMatchers runs all active problem matchers against a log line.
// Single-pattern matchers are supported (the common case). Multi-pattern
// matchers (where patterns span consecutive lines) are not yet implemented.
func (l *LogStreamer) applyMatchers(nodeID, line string) {
	for _, m := range l.matchers {
		if len(m.Pattern) == 0 {
			continue
		}
		// Use only single-pattern matchers (most common).
		p := m.Pattern[0]
		if p.compiled == nil {
			continue
		}
		matches := p.compiled.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		ann := Annotation{NodeID: nodeID}

		// Extract fields from capture groups.
		if p.File > 0 && p.File < len(matches) {
			ann.File = matches[p.File]
		}
		if p.Line > 0 && p.Line < len(matches) {
			ann.Line = matches[p.Line]
		}
		if p.Column > 0 && p.Column < len(matches) {
			ann.Col = matches[p.Column]
		}
		if p.Severity > 0 && p.Severity < len(matches) {
			ann.Level = strings.ToLower(matches[p.Severity])
		}
		if p.Message > 0 && p.Message < len(matches) {
			ann.Message = matches[p.Message]
		}

		// Default severity to error if not specified by the pattern.
		if ann.Level == "" {
			ann.Level = "error"
		}

		l.annotations = append(l.annotations, ann)
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
