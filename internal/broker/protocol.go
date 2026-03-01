package broker

import "encoding/json"

// PipelineContextData is the tagged-union format the runner uses for context values.
// Type tags: 0=string, 1=array, 2=dictionary, 3=boolean, 4=number, 5=expression
type PipelineContextData struct {
	Type        int                    `json:"t"`
	StringValue *string                `json:"s,omitempty"`
	ArrayValue  []PipelineContextData  `json:"a,omitempty"`
	DictValue   []DictEntry            `json:"d,omitempty"`
	BoolValue   *bool                  `json:"b,omitempty"`
	NumberValue *float64               `json:"n,omitempty"`
	ExprValue   *string                `json:"expr,omitempty"`
}

// DictEntry is a key-value pair in a PipelineContextData dictionary.
// The C# runner expects "k" to be a plain string, not a nested PipelineContextData object.
type DictEntry struct {
	Key   string              `json:"k"`
	Value PipelineContextData `json:"v"`
}

// AgentJobRequestMessage is the payload sent to the runner for job execution.
type AgentJobRequestMessage struct {
	MessageType          string                          `json:"messageType"`
	Plan                 *TaskOrchestrationPlanReference `json:"plan"`
	Timeline             *TimelineReference              `json:"timeline"`
	JobID                string                          `json:"jobId"`
	JobDisplayName       string                          `json:"jobDisplayName"`
	JobName              string                          `json:"jobName"`
	RequestID            int64                           `json:"requestId"`
	LockedUntil          string                          `json:"lockedUntil"`
	Resources            JobResources                    `json:"resources"`
	ContextData          map[string]PipelineContextData  `json:"contextData"`
	Variables            map[string]VariableValue        `json:"variables"`
	EnvironmentVariables []TemplateToken                   `json:"environmentVariables,omitempty"`
	MaskHints            []MaskHint                      `json:"maskHints"`
	Steps                []JobStep                       `json:"steps"`
	JobOutputs           *TemplateToken                   `json:"jobOutputs,omitempty"`
	Defaults             *JobDefaults                    `json:"defaults,omitempty"`
	ActionsEnvironment   *ActionsEnvironment             `json:"actionsEnvironment,omitempty"`
	JobContainer         *TemplateToken                   `json:"jobContainer,omitempty"`
	JobServiceContainers *TemplateToken                   `json:"jobServiceContainers,omitempty"`
}

// TaskOrchestrationPlanReference identifies the plan for a job.
type TaskOrchestrationPlanReference struct {
	ScopeIdentifier string `json:"scopeIdentifier"`
	PlanType        string `json:"planType"`
	PlanID          string `json:"planId"`
	Version         int    `json:"version"`
}

// TimelineReference identifies the timeline for status updates.
type TimelineReference struct {
	ID       string  `json:"id"`
	Location *string `json:"location,omitempty"`
}

// JobResources holds service endpoints the runner needs.
type JobResources struct {
	Endpoints []ServiceEndpoint `json:"endpoints"`
}

// ServiceEndpoint describes how the runner connects to a service.
type ServiceEndpoint struct {
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Authorization EndpointAuth      `json:"authorization"`
	Data          map[string]string `json:"data,omitempty"`
}

// EndpointAuth holds authentication details for a service endpoint.
type EndpointAuth struct {
	Scheme     string            `json:"scheme"`
	Parameters map[string]string `json:"parameters"`
}

// VariableValue is a key-value pair with optional secret masking.
type VariableValue struct {
	Value    string `json:"value"`
	IsSecret bool   `json:"issecret"`
}

// MaskHint tells the runner to mask a value in log output.
type MaskHint struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// JobStep describes a single step for the runner to execute.
// In the C# runner, all steps are ActionStep : JobStep : Step.
// StepType.Action = 4 is the only step type.
type JobStep struct {
	ID               string         `json:"id"`
	Type             int            `json:"type"`                       // Always 4 (StepType.Action)
	Reference        StepReference  `json:"reference"`
	DisplayName      string         `json:"displayName,omitempty"`
	Name             string         `json:"name,omitempty"`
	ContextName      string         `json:"contextName,omitempty"`
	DisplayNameToken *string        `json:"displayNameToken,omitempty"` // TemplateToken (simple string)
	Environment      *TemplateToken `json:"environment,omitempty"`
	Inputs           *TemplateToken `json:"inputs,omitempty"`
	Condition        string         `json:"condition,omitempty"`
	ContinueOnError  *TemplateToken `json:"continueOnError,omitempty"`
	TimeoutInMinutes *TemplateToken `json:"timeoutInMinutes,omitempty"`
	Enabled          *bool          `json:"enabled,omitempty"`
}

// StepReference identifies the action or script a step runs.
// The C# runner uses ActionStepDefinitionReference with polymorphic deserialization:
//   ActionSourceType: Repository=1, ContainerRegistry=2, Script=3
type StepReference struct {
	Type           int    `json:"type"`                       // 1=Repository, 2=ContainerRegistry, 3=Script
	Name           string `json:"name,omitempty"`
	Ref            string `json:"ref,omitempty"`              // Branch/tag/commit for repository references
	RepositoryType string `json:"repositoryType,omitempty"`
	Path           string `json:"path,omitempty"`
	Image          string `json:"image,omitempty"`            // For container registry references
}

// TemplateToken represents a TemplateToken in the runner's object templating system.
// Simple values (string, bool, number) serialize as raw JSON values.
// Complex values (mapping, sequence, expression) use a typed object format.
type TemplateToken struct {
	// For simple tokens, one of these is set:
	StringValue *string
	BoolValue   *bool
	NumberValue *float64

	// For complex tokens:
	TokenType       *int                   // 0=String, 1=Sequence, 2=Mapping, 3=BasicExpression, 5=Boolean, 6=Number, 7=Null
	MapPairs        []TemplateTokenMapPair // For Mapping tokens (type=2)
	SeqItems        []*TemplateToken       // For Sequence tokens (type=1)
	ExpressionValue *string                // For BasicExpression tokens (type=3)
}

// TemplateTokenMapPair is a key-value pair in a TemplateToken mapping.
// Value is a *TemplateToken so map entries can hold nested tokens (e.g., expression tokens
// for job outputs). For simple string mappings, use NewTemplateTokenString as the value.
type TemplateTokenMapPair struct {
	Key   string
	Value *TemplateToken
}

// NewTemplateTokenMapping creates a TemplateToken mapping from a string→string map.
// Values are wrapped in StringTokens.
func NewTemplateTokenMapping(m map[string]string) *TemplateToken {
	if len(m) == 0 {
		return nil
	}
	pairs := make([]TemplateTokenMapPair, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, TemplateTokenMapPair{Key: k, Value: NewTemplateTokenString(v)})
	}
	t := 2 // Mapping
	return &TemplateToken{TokenType: &t, MapPairs: pairs}
}

// NewTemplateTokenMappingTokens creates a TemplateToken mapping where values are
// arbitrary TemplateTokens (e.g., expression tokens for job outputs).
func NewTemplateTokenMappingTokens(pairs map[string]*TemplateToken) *TemplateToken {
	if len(pairs) == 0 {
		return nil
	}
	mapPairs := make([]TemplateTokenMapPair, 0, len(pairs))
	for k, v := range pairs {
		mapPairs = append(mapPairs, TemplateTokenMapPair{Key: k, Value: v})
	}
	t := 2 // Mapping
	return &TemplateToken{TokenType: &t, MapPairs: mapPairs}
}

// NewTemplateTokenString creates a simple string TemplateToken.
func NewTemplateTokenString(s string) *TemplateToken {
	return &TemplateToken{StringValue: &s}
}

// NewTemplateTokenBool creates a simple boolean TemplateToken.
func NewTemplateTokenBool(b bool) *TemplateToken {
	return &TemplateToken{BoolValue: &b}
}

// NewTemplateTokenSequence creates a SequenceToken from a list of strings.
func NewTemplateTokenSequence(items []string) *TemplateToken {
	seq := make([]*TemplateToken, len(items))
	for i, s := range items {
		seq[i] = NewTemplateTokenString(s)
	}
	t := 1 // Sequence
	return &TemplateToken{TokenType: &t, SeqItems: seq}
}

// NewTemplateTokenExpression creates a BasicExpression TemplateToken.
// The expression should be without the ${{ }} wrapper (e.g., "steps.version.outputs.version").
func NewTemplateTokenExpression(expr string) *TemplateToken {
	t := 3 // BasicExpression
	return &TemplateToken{TokenType: &t, ExpressionValue: &expr}
}

// MarshalJSON implements custom JSON marshaling for TemplateToken.
// Simple tokens (string, bool, number) serialize as raw JSON values.
// Mapping tokens serialize as {"type": 2, "map": [{"Key": k, "Value": v}, ...]}.
// This matches the C# runner's Newtonsoft.Json serialization of
// MappingToken.m_items (List<KeyValuePair<ScalarToken, TemplateToken>>).
func (t TemplateToken) MarshalJSON() ([]byte, error) {
	if t.StringValue != nil {
		return json.Marshal(*t.StringValue)
	}
	if t.BoolValue != nil {
		return json.Marshal(*t.BoolValue)
	}
	if t.NumberValue != nil {
		return json.Marshal(*t.NumberValue)
	}
	if t.TokenType != nil {
		switch *t.TokenType {
		case 1:
			// Sequence token: {"type": 1, "seq": [...]}
			return json.Marshal(map[string]any{
				"type": 1,
				"seq":  t.SeqItems,
			})
		case 2:
			// Mapping token: {"type": 2, "map": [{"Key": k, "Value": v}, ...]}
			// Keys are serialized as TemplateTokens (StringToken → plain string).
			// Values are serialized as TemplateTokens (StringToken → plain string,
			// BasicExpressionToken → {"type": 3, "expr": "..."}, etc.).
			type kvPair struct {
				Key   *TemplateToken `json:"Key"`
				Value *TemplateToken `json:"Value"`
			}
			pairs := make([]kvPair, 0, len(t.MapPairs))
			for _, p := range t.MapPairs {
				pairs = append(pairs, kvPair{
					Key:   NewTemplateTokenString(p.Key),
					Value: p.Value,
				})
			}
			return json.Marshal(map[string]any{
				"type": 2,
				"map":  pairs,
			})
		case 3:
			// BasicExpression token: {"type": 3, "expr": "..."}
			expr := ""
			if t.ExpressionValue != nil {
				expr = *t.ExpressionValue
			}
			return json.Marshal(map[string]any{
				"type": 3,
				"expr": expr,
			})
		}
	}
	return []byte("null"), nil
}

// UnmarshalJSON implements custom JSON unmarshaling for TemplateToken.
func (t *TemplateToken) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		t.StringValue = &s
		return nil
	}
	// Try bool.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		t.BoolValue = &b
		return nil
	}
	// Try number.
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		t.NumberValue = &n
		return nil
	}
	// Try typed object format (mapping, expression, etc.).
	var obj struct {
		Type int              `json:"type"`
		Map  []json.RawMessage `json:"map"`
		Expr *string          `json:"expr"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		switch obj.Type {
		case 2: // Mapping
			t.TokenType = &obj.Type
			for _, raw := range obj.Map {
				var kv struct {
					Key   *TemplateToken `json:"Key"`
					Value *TemplateToken `json:"Value"`
				}
				if err := json.Unmarshal(raw, &kv); err == nil {
					key := ""
					if kv.Key != nil && kv.Key.StringValue != nil {
						key = *kv.Key.StringValue
					}
					t.MapPairs = append(t.MapPairs, TemplateTokenMapPair{Key: key, Value: kv.Value})
				}
			}
			return nil
		case 3: // BasicExpression
			t.TokenType = &obj.Type
			t.ExpressionValue = obj.Expr
			return nil
		}
	}
	return nil
}

// JobDefaults holds default settings for job steps.
type JobDefaults struct {
	Run *RunDefaults `json:"run,omitempty"`
}

// RunDefaults holds default settings for run steps.
type RunDefaults struct {
	Shell            string `json:"shell,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
}

// ActionsEnvironment provides the URL for the actions environment.
type ActionsEnvironment struct {
	URL string `json:"url,omitempty"`
}

// TaskAgentSession represents a session between runner and broker.
type TaskAgentSession struct {
	SessionID         string         `json:"sessionId"`
	OwnerName         string         `json:"ownerName,omitempty"`
	Agent             TaskAgent      `json:"agent,omitempty"`
	UseFipsEncryption bool           `json:"useFipsEncryption,omitempty"`
	EncryptionKey     *EncryptionKey `json:"encryptionKey,omitempty"`
}

// TaskAgent identifies a runner agent.
type TaskAgent struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// EncryptionKey holds an optional encryption key for the session.
type EncryptionKey struct {
	Encrypted bool   `json:"encrypted"`
	Value     string `json:"value"`
}

// TaskAgentMessage wraps messages sent during long-poll.
type TaskAgentMessage struct {
	MessageID   int64  `json:"messageId"`
	MessageType string `json:"messageType"`
	Body        string `json:"body"` // JSON-encoded AgentJobRequestMessage
	IV          string `json:"iv,omitempty"`
}

// AcquireJobRequest is sent by the runner to acquire a job for execution.
type AcquireJobRequest struct {
	JobMessageID int64  `json:"jobMessageId"`
	StreamID     string `json:"streamId,omitempty"`
}

// AcquireJobResponse is returned from the acquire endpoint.
type AcquireJobResponse = AgentJobRequestMessage

// RenewJobRequest extends the lock on a job.
type RenewJobRequest struct {
	PlanID      string `json:"planId"`
	JobID       string `json:"jobId"`
	RequestID   int64  `json:"requestId"`
	LockedUntil string `json:"lockedUntil"`
}

// RenewJobResponse is the response to a job renewal.
type RenewJobResponse struct {
	PlanID      string `json:"planId"`
	JobID       string `json:"jobId"`
	RequestID   int64  `json:"requestId"`
	LockedUntil string `json:"lockedUntil"`
}

// CompleteJobRequest is sent when the runner finishes a job.
type CompleteJobRequest struct {
	PlanID    string                   `json:"planId"`
	JobID     string                   `json:"jobId"`
	RequestID int64                    `json:"requestId"`
	Result    string                   `json:"result"` // "succeeded", "failed", "cancelled"
	Outputs   map[string]VariableValue `json:"outputs,omitempty"`
}

// TimelineRecord is a step status update from the runner.
type TimelineRecord struct {
	ID           string        `json:"id"`
	ParentID     string        `json:"parentId,omitempty"`
	Type         string        `json:"type,omitempty"`
	Name         string        `json:"name,omitempty"`
	State        string        `json:"state,omitempty"`  // "Pending", "InProgress", "Completed"
	Result       *string       `json:"result,omitempty"` // "succeeded", "failed", "skipped", "cancelled"
	StartTime    string        `json:"startTime,omitempty"`
	FinishTime   string        `json:"finishTime,omitempty"`
	Order        int           `json:"order,omitempty"`
	RefName      string        `json:"refName,omitempty"`
	Log          *LogReference `json:"log,omitempty"`
	ErrorCount   int           `json:"errorCount,omitempty"`
	WarningCount int           `json:"warningCount,omitempty"`
}

// LogReference points to log data for a timeline record.
type LogReference struct {
	ID       int    `json:"id"`
	Location string `json:"location,omitempty"`
}

// ConnectionData is returned from /_apis/connectionData.
type ConnectionData struct {
	AuthenticatedUser                *IdentityRef        `json:"authenticatedUser,omitempty"`
	AuthorizedUser                   *IdentityRef        `json:"authorizedUser,omitempty"`
	InstanceID                       string              `json:"instanceId"`
	DeploymentID                     string              `json:"deploymentId,omitempty"`
	DeploymentType                   string              `json:"deploymentType,omitempty"`
	LocationServiceData              LocationServiceData `json:"locationServiceData"`
	WebApplicationRelativeDirectory  string              `json:"webApplicationRelativeDirectory,omitempty"`
}

// IdentityRef is a minimal identity object to satisfy the runner's deserialization.
type IdentityRef struct {
	DisplayName string              `json:"displayName,omitempty"`
	ID          string              `json:"id,omitempty"`
	UniqueName  string              `json:"uniqueName,omitempty"`
	Descriptor  *IdentityDescriptor `json:"descriptor,omitempty"`
}

// IdentityDescriptor identifies an identity.
// The C# runner deserializes this as a plain JSON string in the format "type;identifier".
type IdentityDescriptor string

// NewIdentityDescriptor creates a descriptor in "type;identifier" format.
func NewIdentityDescriptor(identityType, identifier string) IdentityDescriptor {
	return IdentityDescriptor(identityType + ";" + identifier)
}

// LocationServiceData holds service definitions the runner uses to discover endpoints.
type LocationServiceData struct {
	ServiceOwner                string              `json:"serviceOwner,omitempty"`
	DefaultAccessMappingMoniker string              `json:"defaultAccessMappingMoniker"`
	ClientCacheFresh            bool                `json:"clientCacheFresh"`
	ClientCacheTimeToLive       int                 `json:"clientCacheTimeToLive"`
	ServiceDefinitions          []ServiceDefinition `json:"serviceDefinitions"`
	AccessMappings              []AccessMapping     `json:"accessMappings"`
	LastChangeID                int                 `json:"lastChangeId"`
	LastChangeID64              int64               `json:"lastChangeId64"`
}

// ServiceDefinition describes a service the runner can use.
// The runner converts these to ApiResourceLocation objects via FromServiceDefinition().
// MinVersion/MaxVersion/ReleasedVersion are required — without them, NegotiateRequestVersion
// fails with ArgumentNullException on apiVersion.
type ServiceDefinition struct {
	ServiceType      string            `json:"serviceType"`
	Identifier       string            `json:"identifier"`
	DisplayName      string            `json:"displayName"`
	RelativePath     string            `json:"relativePath"`
	ServiceOwner     string            `json:"serviceOwner"`
	ResourceVersion  int               `json:"resourceVersion"`
	MinVersion       string            `json:"minVersion,omitempty"`
	MaxVersion       string            `json:"maxVersion,omitempty"`
	ReleasedVersion  string            `json:"releasedVersion,omitempty"`
	LocationMappings []LocationMapping `json:"locationMappings,omitempty"`
}

// LocationMapping maps a service definition to a specific access point.
type LocationMapping struct {
	AccessMappingMoniker string `json:"accessMappingMoniker"`
	Location             string `json:"location"`
}

// AccessMapping defines how to access the server — hostname + virtual directory.
type AccessMapping struct {
	DisplayName    string `json:"displayName"`
	Moniker        string `json:"moniker"`
	AccessPoint    string `json:"accessPoint"`
	ServiceOwner   string `json:"serviceOwner"`
	VirtualDirectory string `json:"virtualDirectory"`
}

// ResourceArea describes a service resource area returned from /_apis/ResourceAreas.
type ResourceArea struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	LocationURL string `json:"locationUrl"`
}

// ApiResourceLocation describes an API endpoint's location and how to construct
// URLs for it. Returned from OPTIONS _apis/ and cached by the runner.
// The runner substitutes {placeholders} in RouteTemplate with actual values.
type ApiResourceLocation struct {
	ID              string `json:"id"`
	Area            string `json:"area"`
	ResourceName    string `json:"resourceName"`
	RouteTemplate   string `json:"routeTemplate"`
	ResourceVersion int    `json:"resourceVersion"`
	MinVersion      string `json:"minVersion"`
	MaxVersion      string `json:"maxVersion"`
	ReleasedVersion string `json:"releasedVersion"`
}

// RunnerRegistration is the runner registration request/response.
type RunnerRegistration struct {
	URL         string `json:"url,omitempty"`
	Token       string `json:"token,omitempty"`
	TokenSchema string `json:"tokenSchema,omitempty"`
}

// AgentPool represents a pool of runners.
type AgentPool struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// JobCompletionResult captures the final state of a completed job.
type JobCompletionResult struct {
	JobID    string
	Result   string // "succeeded", "failed", "cancelled"
	Outputs  map[string]VariableValue
	Timeline []TimelineRecord
	Logs     map[string][]string // logId -> lines
}
