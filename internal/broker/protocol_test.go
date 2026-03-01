package broker

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineContextData_JSONRoundTrip_String(t *testing.T) {
	s := "hello"
	data := PipelineContextData{Type: 0, StringValue: &s}

	b, err := json.Marshal(data)
	require.NoError(t, err)

	var decoded PipelineContextData
	err = json.Unmarshal(b, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 0, decoded.Type)
	require.NotNil(t, decoded.StringValue)
	assert.Equal(t, "hello", *decoded.StringValue)
}

func TestPipelineContextData_JSONRoundTrip_Bool(t *testing.T) {
	b := true
	data := PipelineContextData{Type: 3, BoolValue: &b}

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	var decoded PipelineContextData
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 3, decoded.Type)
	require.NotNil(t, decoded.BoolValue)
	assert.True(t, *decoded.BoolValue)
}

func TestPipelineContextData_JSONRoundTrip_Number(t *testing.T) {
	n := 42.5
	data := PipelineContextData{Type: 4, NumberValue: &n}

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	var decoded PipelineContextData
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 4, decoded.Type)
	require.NotNil(t, decoded.NumberValue)
	assert.Equal(t, 42.5, *decoded.NumberValue)
}

func TestPipelineContextData_JSONRoundTrip_Array(t *testing.T) {
	s1, s2 := "a", "b"
	data := PipelineContextData{
		Type: 1,
		ArrayValue: []PipelineContextData{
			{Type: 0, StringValue: &s1},
			{Type: 0, StringValue: &s2},
		},
	}

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	var decoded PipelineContextData
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 1, decoded.Type)
	require.Len(t, decoded.ArrayValue, 2)
	assert.Equal(t, "a", *decoded.ArrayValue[0].StringValue)
	assert.Equal(t, "b", *decoded.ArrayValue[1].StringValue)
}

func TestPipelineContextData_JSONRoundTrip_Dict(t *testing.T) {
	k := "key"
	v := "value"
	data := PipelineContextData{
		Type: 2,
		DictValue: []DictEntry{
			{
				Key:   k,
				Value: PipelineContextData{Type: 0, StringValue: &v},
			},
		},
	}

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	var decoded PipelineContextData
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 2, decoded.Type)
	require.Len(t, decoded.DictValue, 1)
	assert.Equal(t, "key", decoded.DictValue[0].Key)
	assert.Equal(t, "value", *decoded.DictValue[0].Value.StringValue)
}

func TestPipelineContextData_OmitsEmptyFields(t *testing.T) {
	s := "test"
	data := PipelineContextData{Type: 0, StringValue: &s}

	raw, err := json.Marshal(data)
	require.NoError(t, err)

	// Should not contain array, dict, bool, number, or expr keys
	var m map[string]interface{}
	err = json.Unmarshal(raw, &m)
	require.NoError(t, err)

	assert.Contains(t, m, "t")
	assert.Contains(t, m, "s")
	assert.NotContains(t, m, "a")
	assert.NotContains(t, m, "d")
	assert.NotContains(t, m, "b")
	assert.NotContains(t, m, "n")
	assert.NotContains(t, m, "expr")
}

func TestAgentJobRequestMessage_JSONRoundTrip(t *testing.T) {
	msg := AgentJobRequestMessage{
		MessageType:    "PipelineAgentJobRequest",
		JobID:          "job-123",
		JobDisplayName: "My Job",
		JobName:        "my-job",
		RequestID:      1,
		LockedUntil:    "2024-01-01T00:00:00Z",
		Plan: &TaskOrchestrationPlanReference{
			ScopeIdentifier: "scope-1",
			PlanType:        "Build",
			PlanID:          "plan-1",
			Version:         1,
		},
		Timeline: &TimelineReference{
			ID: "timeline-1",
		},
		Resources: JobResources{
			Endpoints: []ServiceEndpoint{
				{
					Name: "SystemVssConnection",
					URL:  "http://localhost:8080",
					Authorization: EndpointAuth{
						Scheme:     "OAuth",
						Parameters: map[string]string{"AccessToken": "token"},
					},
				},
			},
		},
		Variables: map[string]VariableValue{
			"var1": {Value: "val1", IsSecret: false},
		},
		Steps: []JobStep{
			{
				ID:          "step-1",
				Type:        StepTypeAction,
				DisplayName: "Run echo",
				ContextName: "step1",
				Reference:   StepReference{Type: ActionSourceScript},
				Inputs:      NewTemplateTokenMapping(map[string]string{"script": "echo hello"}),
			},
		},
	}

	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded AgentJobRequestMessage
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, msg.MessageType, decoded.MessageType)
	assert.Equal(t, msg.JobID, decoded.JobID)
	assert.Equal(t, msg.JobDisplayName, decoded.JobDisplayName)
	assert.Equal(t, msg.JobName, decoded.JobName)
	assert.Equal(t, msg.Plan.PlanID, decoded.Plan.PlanID)
	assert.Equal(t, msg.Timeline.ID, decoded.Timeline.ID)
	require.Len(t, decoded.Resources.Endpoints, 1)
	assert.Equal(t, "SystemVssConnection", decoded.Resources.Endpoints[0].Name)
	require.Len(t, decoded.Steps, 1)
	assert.Equal(t, StepTypeAction, decoded.Steps[0].Type)
}

func TestJobDefaults_OmittedWhenNil(t *testing.T) {
	msg := AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "test",
	}

	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	var m map[string]interface{}
	err = json.Unmarshal(raw, &m)
	require.NoError(t, err)

	assert.NotContains(t, m, "defaults")
	assert.NotContains(t, m, "actionsEnvironment")
	assert.NotContains(t, m, "jobContainer")
	assert.NotContains(t, m, "jobServiceContainers")
}

func TestCompleteJobRequest_JSON(t *testing.T) {
	req := CompleteJobRequest{
		PlanID:    "plan-1",
		JobID:     "job-1",
		RequestID: 42,
		Result:    "succeeded",
		Outputs: map[string]VariableValue{
			"out1": {Value: "result1"},
		},
	}

	raw, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded CompleteJobRequest
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "succeeded", decoded.Result)
	assert.Equal(t, "result1", decoded.Outputs["out1"].Value)
}

func TestTimelineRecord_JSON(t *testing.T) {
	result := "succeeded"
	rec := TimelineRecord{
		ID:     "rec-1",
		Name:   "Build Step",
		State:  "Completed",
		Result: &result,
		Order:  1,
	}

	raw, err := json.Marshal(rec)
	require.NoError(t, err)

	var decoded TimelineRecord
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "rec-1", decoded.ID)
	assert.Equal(t, "Completed", decoded.State)
	require.NotNil(t, decoded.Result)
	assert.Equal(t, "succeeded", *decoded.Result)
}

func TestTaskAgentSession_JSON(t *testing.T) {
	sess := TaskAgentSession{
		SessionID: "sess-1",
		OwnerName: "ions",
		Agent: TaskAgent{
			ID:   1,
			Name: "local-runner",
		},
	}

	raw, err := json.Marshal(sess)
	require.NoError(t, err)

	var decoded TaskAgentSession
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "sess-1", decoded.SessionID)
	assert.Equal(t, "ions", decoded.OwnerName)
	assert.Equal(t, 1, decoded.Agent.ID)
	assert.Equal(t, "local-runner", decoded.Agent.Name)
}

func TestTaskAgentMessage_JSON(t *testing.T) {
	msg := TaskAgentMessage{
		MessageID:   100,
		MessageType: "PipelineAgentJobRequest",
		Body:        `{"jobId":"abc"}`,
	}

	raw, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded TaskAgentMessage
	err = json.Unmarshal(raw, &decoded)
	require.NoError(t, err)

	assert.Equal(t, int64(100), decoded.MessageID)
	assert.Equal(t, `{"jobId":"abc"}`, decoded.Body)
}

func TestTemplateToken_MappingJSON(t *testing.T) {
	// Test that mapping tokens produce KeyValuePair format expected by C# runner.
	token := NewTemplateTokenMapping(map[string]string{
		"script": "echo hello",
	})

	raw, err := json.Marshal(token)
	require.NoError(t, err)

	// Verify the JSON structure has "Key"/"Value" pairs, not flat strings.
	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))
	assert.Equal(t, float64(2), obj["type"])

	mapArray, ok := obj["map"].([]any)
	require.True(t, ok, "map should be an array")
	require.Len(t, mapArray, 1)

	pair, ok := mapArray[0].(map[string]any)
	require.True(t, ok, "each map entry should be an object")
	assert.Equal(t, "script", pair["Key"])
	assert.Equal(t, "echo hello", pair["Value"])

	// Round-trip test.
	var decoded TemplateToken
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Len(t, decoded.MapPairs, 1)
	assert.Equal(t, "script", decoded.MapPairs[0].Key)
	require.NotNil(t, decoded.MapPairs[0].Value)
	require.NotNil(t, decoded.MapPairs[0].Value.StringValue)
	assert.Equal(t, "echo hello", *decoded.MapPairs[0].Value.StringValue)
}

func TestTemplateToken_SimpleValues(t *testing.T) {
	// String
	s := NewTemplateTokenString("hello")
	raw, _ := json.Marshal(s)
	assert.Equal(t, `"hello"`, string(raw))

	// Bool
	b := NewTemplateTokenBool(true)
	raw, _ = json.Marshal(b)
	assert.Equal(t, `true`, string(raw))

	// Number
	n := 42.0
	num := &TemplateToken{NumberValue: &n}
	raw, _ = json.Marshal(num)
	assert.Equal(t, `42`, string(raw))
}
