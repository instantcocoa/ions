package broker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/workflow"
)

// requestIDCounter generates unique request IDs across all jobs.
var requestIDCounter atomic.Int64

// JobMessageOptions configures job message construction.
type JobMessageOptions struct {
	// UseRunnerContainers delegates container management to the runner.
	// When true, JobContainer and JobServiceContainers are set in the
	// message and the runner handles Docker orchestration natively.
	// This only works on Linux.
	UseRunnerContainers bool
}

// BuildJobMessage constructs an AgentJobRequestMessage from a job node and context.
func BuildJobMessage(
	node *graph.JobNode,
	job *workflow.Job,
	ctx expression.MapContext,
	brokerURL string,
	runID string,
	secrets map[string]string,
	opts ...JobMessageOptions,
) (*AgentJobRequestMessage, error) {
	jobID := uuid.New().String()
	planID := uuid.New().String()
	timelineID := uuid.New().String()

	steps, err := convertSteps(job.Steps)
	if err != nil {
		return nil, fmt.Errorf("converting steps: %w", err)
	}

	msg := &AgentJobRequestMessage{
		MessageType:    "PipelineAgentJobRequest",
		JobID:          jobID,
		JobDisplayName: node.JobName,
		JobName:        node.JobID,
		RequestID:      requestIDCounter.Add(1),
		LockedUntil:    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Plan: &TaskOrchestrationPlanReference{
			ScopeIdentifier: uuid.New().String(),
			PlanType:        "Build",
			PlanID:          planID,
			Version:         8, // >= 8 enables PlanFeatures.JobCompletedPlanEvent
		},
		Timeline: &TimelineReference{
			ID: timelineID,
		},
		Resources:            buildResources(brokerURL, runID),
		ContextData:          MapContextToPipelineContextData(ctx),
		Variables:            buildVariables(runID),
		EnvironmentVariables: buildEnvironmentVariables(ctx),
		MaskHints:            buildMaskHints(secrets),
		Steps:                steps,
	}

	// Populate job outputs — the runner evaluates these expressions at job end
	// and sends the results back via outputVariables in the completion PATCH.
	// Values must be BasicExpression TemplateTokens (type=3) with the inner expression.
	if len(job.Outputs) > 0 {
		outputTokens := make(map[string]*TemplateToken, len(job.Outputs))
		for name, output := range job.Outputs {
			expr := stripExpressionWrapper(output.Value)
			outputTokens[name] = NewTemplateTokenExpression(expr)
		}
		msg.JobOutputs = NewTemplateTokenMappingTokens(outputTokens)
	}

	if job.Defaults != nil && job.Defaults.Run != nil {
		msg.Defaults = &JobDefaults{
			Run: &RunDefaults{
				Shell:            job.Defaults.Run.Shell,
				WorkingDirectory: job.Defaults.Run.WorkingDirectory,
			},
		}
	}

	// Container support: when UseRunnerContainers is set, delegate container
	// management to the runner. The runner creates Docker containers, networks,
	// and runs steps inside the job container natively. This is how GitHub's
	// hosted runners work and only functions on Linux.
	var useRunnerContainers bool
	for _, o := range opts {
		if o.UseRunnerContainers {
			useRunnerContainers = true
		}
	}

	if useRunnerContainers {
		if job.Container != nil {
			msg.JobContainer = containerToTemplateToken(job.Container)
		}
		if len(job.Services) > 0 {
			msg.JobServiceContainers = serviceContainersToTemplateToken(job.Services)
		}
	}

	return msg, nil
}

// convertSteps converts workflow steps to JobSteps for the runner protocol.
func convertSteps(steps []workflow.Step) ([]JobStep, error) {
	result := make([]JobStep, len(steps))
	for i, s := range steps {
		js, err := convertStep(s, i)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		result[i] = js
	}
	return result, nil
}

// Step type and reference type constants matching the C# runner.
const (
	StepTypeAction = 4 // StepType.Action — the only step type

	ActionSourceRepository       = 1 // ActionSourceType.Repository
	ActionSourceContainerRegistry = 2 // ActionSourceType.ContainerRegistry
	ActionSourceScript           = 3 // ActionSourceType.Script
)

// convertStep converts a single workflow.Step to a JobStep.
func convertStep(s workflow.Step, index int) (JobStep, error) {
	// The protocol step ID must always be a GUID — the C# runner's ActionStep.Id is System.Guid.
	// The YAML step id (e.g. "version") goes in contextName for expression evaluation.
	stepID := uuid.New().String()

	contextName := s.ID
	if contextName == "" {
		contextName = fmt.Sprintf("__%d", index)
	}

	displayName := s.Name
	if displayName == "" {
		if s.Run != "" {
			displayName = "Run " + truncate(s.Run, 60)
		} else if s.Uses != "" {
			displayName = "Run " + s.Uses
		} else {
			displayName = fmt.Sprintf("Step %d", index+1)
		}
	}

	condition := s.If
	if condition == "" {
		condition = "success()"
	}

	enabled := true
	js := JobStep{
		ID:          stepID,
		Type:        StepTypeAction,
		DisplayName: displayName,
		ContextName: contextName,
		Condition:   condition,
		Enabled:     &enabled,
	}

	if len(s.Env) > 0 {
		js.Environment = newTemplateTokenMappingWithExprs(s.Env)
	}

	if s.ContinueOnError.IsExpr {
		js.ContinueOnError = NewTemplateTokenBool(false)
	} else if s.ContinueOnError.Value {
		js.ContinueOnError = NewTemplateTokenBool(true)
	}

	if s.TimeoutMinutes != nil {
		n := float64(*s.TimeoutMinutes)
		js.TimeoutInMinutes = &TemplateToken{NumberValue: &n}
	}

	if s.Run != "" {
		js.Reference = StepReference{
			Type: ActionSourceScript,
		}
		inputs := map[string]string{
			"script": s.Run,
		}
		if s.Shell != "" {
			inputs["shell"] = s.Shell
		}
		if s.WorkingDirectory != "" {
			inputs["workingDirectory"] = s.WorkingDirectory
		}
		js.Inputs = newTemplateTokenMappingWithExprs(inputs)
	} else if s.Uses != "" {
		ref, err := parseActionReference(s.Uses)
		if err != nil {
			return JobStep{}, err
		}
		js.Reference = ref
		if len(s.With) > 0 {
			js.Inputs = newTemplateTokenMappingWithExprs(s.With)
		}
	} else {
		return JobStep{}, fmt.Errorf("step %q has neither 'run' nor 'uses'", displayName)
	}

	return js, nil
}

// parseActionReference parses a uses: field into a StepReference.
//
// Formats:
//   - "owner/repo@ref" → repository action
//   - "owner/repo/path@ref" → repository action with path
//   - "./path/to/action" → local action
//   - "docker://image:tag" → docker action
func parseActionReference(uses string) (StepReference, error) {
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "../") {
		return StepReference{
			Type:           ActionSourceRepository,
			RepositoryType: "self",
			Path:           strings.TrimPrefix(uses, "./"),
		}, nil
	}

	if strings.HasPrefix(uses, "docker://") {
		return StepReference{
			Type:  ActionSourceContainerRegistry,
			Image: uses,
		}, nil
	}

	// Parse "owner/repo@ref" or "owner/repo/path@ref"
	atIdx := strings.LastIndex(uses, "@")
	if atIdx < 0 {
		return StepReference{}, fmt.Errorf("invalid action reference %q: missing @version", uses)
	}

	nameWithPath := uses[:atIdx]
	ref := uses[atIdx+1:]

	// Split into name and path: "owner/repo/sub/path" -> name="owner/repo", path="sub/path"
	parts := strings.SplitN(nameWithPath, "/", 3)
	name := nameWithPath
	path := ""
	if len(parts) >= 3 {
		name = parts[0] + "/" + parts[1]
		path = parts[2]
	}

	return StepReference{
		Type:           ActionSourceRepository,
		RepositoryType: "GitHub",
		Name:           name,
		Ref:            ref,
		Path:           path,
	}, nil
}

// generateJWT creates a minimal valid JWT token that the runner can parse.
// Uses algorithm "none" (unsigned) since we control both sides.
// runBackendID must be stable across all jobs in a workflow run so that
// artifacts uploaded by one job can be found by another.
func generateJWT(runBackendID string) string {
	header := map[string]string{
		"typ": "JWT",
		"alg": "None",
	}
	now := time.Now().Unix()
	// The scp claim must include Actions.Results:<runBackendId>:<jobBackendId>
	// for actions/upload-artifact@v4 and actions/download-artifact@v4 to extract
	// backend IDs from the token.
	jobBackendID := uuid.New().String()
	payload := map[string]any{
		"iss":   "ions",
		"aud":   "ions",
		"nbf":   now,
		"exp":   now + 86400, // 24 hours
		"iat":   now,
		"sub":   "ions-runner",
		"scp":   "Actions.GenericRead Actions.GenericWrite Actions.Results:" + runBackendID + ":" + jobBackendID,
		"appid": uuid.New().String(),
	}

	hJSON, _ := json.Marshal(header)
	pJSON, _ := json.Marshal(payload)

	encode := func(data []byte) string {
		s := base64.RawURLEncoding.EncodeToString(data)
		return s
	}

	return encode(hJSON) + "." + encode(pJSON) + "."
}

// buildResources creates the JobResources with the SystemVssConnection endpoint.
func buildResources(brokerURL, runBackendID string) JobResources {
	return JobResources{
		Endpoints: []ServiceEndpoint{
			{
				Name: "SystemVssConnection",
				URL:  brokerURL,
				Authorization: EndpointAuth{
					Scheme: "OAuth",
					Parameters: map[string]string{
						"AccessToken": generateJWT(runBackendID),
					},
				},
				Data: map[string]string{
					"ServerId":                    uuid.New().String(),
					"ServerUrl":                   brokerURL + "/",
					"CacheServerUrl":              brokerURL + "/",
					"GenerateIdTokenUrl":          brokerURL + "/_apis/actionstoken/generateidtoken",
					"ResultsServiceUrl":           brokerURL + "/",
					"ResultsReceiverTenantId":     uuid.New().String(),
					"RunnerAdminUrl":              brokerURL + "/",
					"ActionsRuntimeUrl":           brokerURL + "/",
					"ResultsReceiverClientId":     uuid.New().String(),
					"CheckSuiteUrl":               brokerURL + "/",
					"ActionsServiceAdminUrl":       brokerURL + "/",
					"ActionsServiceResultsUrl":     brokerURL + "/",
					"ActionsCacheUrl":              brokerURL + "/",
					"PipelinesServiceAdminUrl":     brokerURL + "/",
					"PipelinesServiceResultsUrl":   brokerURL + "/",
				},
			},
		},
	}
}

// buildVariables creates the system variables map.
func buildVariables(runID string) map[string]VariableValue {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	return map[string]VariableValue{
		"system.github.run_id": {Value: runID},
		"system.runner.os":     {Value: osName},
		"system.runner.arch":   {Value: arch},
		"system.runner.temp":   {Value: "/tmp"},
		// Enable the new action manifest manager which handles expression tokens
		// in default input values (e.g., ${{ github.token }}).
		"DistributedTask.NewActionMetadata": {Value: "true"},
	}
}

// buildEnvironmentVariables extracts the env context from the expression context
// and returns it as a list of TemplateToken mappings. The runner's EnvironmentVariables
// field is IList<TemplateToken> — a list of mapping layers with "last wins" overlay.
// The runner sets these as OS environment variables for run steps.
func buildEnvironmentVariables(ctx expression.MapContext) []TemplateToken {
	envCtx, ok := ctx["env"]
	if !ok {
		return nil
	}
	fields := envCtx.ObjectFields()
	if len(fields) == 0 {
		return nil
	}
	pairs := make([]TemplateTokenMapPair, 0, len(fields))
	for k, v := range fields {
		pairs = append(pairs, TemplateTokenMapPair{Key: k, Value: NewTemplateTokenString(v.StringVal())})
	}
	t := 2 // Mapping
	return []TemplateToken{{TokenType: &t, MapPairs: pairs}}
}

// buildMaskHints creates mask hints from secrets.
func buildMaskHints(secrets map[string]string) []MaskHint {
	if len(secrets) == 0 {
		return nil
	}
	hints := make([]MaskHint, 0, len(secrets))
	for _, v := range secrets {
		if v != "" {
			hints = append(hints, MaskHint{
				Type:  "regex",
				Value: v,
			})
		}
	}
	return hints
}

// containerToTemplateToken converts a workflow.Container to a TemplateToken mapping.
// The runner expects jobContainer and jobServiceContainers to be TemplateTokens
// (MappingTokens with keys: image, options, env, ports, volumes).
func containerToTemplateToken(c *workflow.Container) *TemplateToken {
	pairs := []TemplateTokenMapPair{
		{Key: "image", Value: NewTemplateTokenString(c.Image)},
	}

	if c.Options != "" {
		pairs = append(pairs, TemplateTokenMapPair{Key: "options", Value: NewTemplateTokenString(c.Options)})
	}

	if len(c.Env) > 0 {
		pairs = append(pairs, TemplateTokenMapPair{Key: "env", Value: NewTemplateTokenMapping(c.Env)})
	}

	if len(c.Ports) > 0 {
		pairs = append(pairs, TemplateTokenMapPair{Key: "ports", Value: NewTemplateTokenSequence(c.Ports)})
	}

	if len(c.Volumes) > 0 {
		pairs = append(pairs, TemplateTokenMapPair{Key: "volumes", Value: NewTemplateTokenSequence(c.Volumes)})
	}

	if c.Credentials != nil && c.Credentials.Username != "" {
		credPairs := []TemplateTokenMapPair{
			{Key: "username", Value: NewTemplateTokenString(c.Credentials.Username)},
			{Key: "password", Value: NewTemplateTokenString(c.Credentials.Password)},
		}
		ct := 2 // Mapping
		pairs = append(pairs, TemplateTokenMapPair{Key: "credentials", Value: &TemplateToken{TokenType: &ct, MapPairs: credPairs}})
	}

	t := 2 // Mapping
	return &TemplateToken{TokenType: &t, MapPairs: pairs}
}

// serviceContainersToTemplateToken converts a map of service containers to a
// TemplateToken mapping where keys are service names and values are container
// TemplateToken mappings.
func serviceContainersToTemplateToken(services map[string]*workflow.Container) *TemplateToken {
	pairs := make([]TemplateTokenMapPair, 0, len(services))
	for name, svc := range services {
		pairs = append(pairs, TemplateTokenMapPair{
			Key:   name,
			Value: containerToTemplateToken(svc),
		})
	}
	t := 2 // Mapping
	return &TemplateToken{TokenType: &t, MapPairs: pairs}
}

// stripExpressionWrapper removes the ${{ }} wrapper from an expression string.
// "${{ steps.version.outputs.version }}" → "steps.version.outputs.version"
// If the string doesn't have the wrapper, it's returned as-is.
func stripExpressionWrapper(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		return strings.TrimSpace(s[3 : len(s)-2])
	}
	return s
}

// parseStringWithExpressions converts a string that may contain embedded ${{ }} expressions
// into the appropriate TemplateToken. This mirrors what GitHub's server-side template parser does:
//
//   - No expressions: returns a plain StringToken (type 0)
//   - Single expression covering the whole value "${{ expr }}": returns BasicExpressionToken (type 3)
//   - Mixed text and expressions: returns a BasicExpressionToken with format() call
//
// For example: `echo "${{ needs.build.outputs.version }}"` becomes:
// BasicExpressionToken: format('echo "{0}"', needs.build.outputs.version)
func parseStringWithExpressions(s string) *TemplateToken {
	if !strings.Contains(s, "${{") {
		return NewTemplateTokenString(s)
	}

	// Parse the string into segments of literal text and expressions.
	type segment struct {
		isExpr bool
		text   string // literal text or expression body
	}
	var segments []segment
	i := 0
	for i < len(s) {
		exprStart := strings.Index(s[i:], "${{")
		if exprStart < 0 {
			// Remaining text is literal.
			segments = append(segments, segment{isExpr: false, text: s[i:]})
			break
		}
		exprStart += i // absolute position

		// Add any literal text before the expression.
		if exprStart > i {
			segments = append(segments, segment{isExpr: false, text: s[i:exprStart]})
		}

		// Find the closing }}.
		closeIdx := -1
		inString := false
		for j := exprStart + 3; j < len(s); j++ {
			if s[j] == '\'' {
				inString = !inString
			} else if !inString && j > 0 && s[j] == '}' && s[j-1] == '}' {
				closeIdx = j
				break
			}
		}
		if closeIdx < 0 {
			// Unclosed expression — treat rest as literal.
			segments = append(segments, segment{isExpr: false, text: s[exprStart:]})
			break
		}

		expr := strings.TrimSpace(s[exprStart+3 : closeIdx-1])
		segments = append(segments, segment{isExpr: true, text: expr})
		i = closeIdx + 1
	}

	// If there's exactly one segment and it's an expression, return BasicExpressionToken directly.
	if len(segments) == 1 && segments[0].isExpr {
		return NewTemplateTokenExpression(segments[0].text)
	}

	// If there's only one segment and it's literal (shouldn't happen since we checked for ${{ above),
	// return a string token.
	if len(segments) == 1 && !segments[0].isExpr {
		return NewTemplateTokenString(segments[0].text)
	}

	// Build a format() expression.
	// Literal segments become part of the format string, expression segments become arguments.
	var formatStr strings.Builder
	var args []string
	argIdx := 0
	for _, seg := range segments {
		if seg.isExpr {
			formatStr.WriteString(fmt.Sprintf("{%d}", argIdx))
			args = append(args, seg.text)
			argIdx++
		} else {
			// Escape single quotes and braces for the format() function.
			escaped := strings.ReplaceAll(seg.text, "'", "''")
			escaped = strings.ReplaceAll(escaped, "{", "{{")
			escaped = strings.ReplaceAll(escaped, "}", "}}")
			formatStr.WriteString(escaped)
		}
	}

	expr := fmt.Sprintf("format('%s', %s)", formatStr.String(), strings.Join(args, ", "))
	return NewTemplateTokenExpression(expr)
}

// newTemplateTokenMappingWithExprs creates a TemplateToken mapping where string values
// are parsed for embedded ${{ }} expressions and converted to the proper token types.
func newTemplateTokenMappingWithExprs(m map[string]string) *TemplateToken {
	if len(m) == 0 {
		return nil
	}
	pairs := make([]TemplateTokenMapPair, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, TemplateTokenMapPair{Key: k, Value: parseStringWithExpressions(v)})
	}
	t := 2 // Mapping
	return &TemplateToken{TokenType: &t, MapPairs: pairs}
}

// truncate shortens a string to maxLen, appending "..." if truncated.
// Newlines are replaced with spaces first.
func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
