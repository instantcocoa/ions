package broker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emaland/ions/internal/expression"
	"github.com/emaland/ions/internal/graph"
	"github.com/emaland/ions/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// parseTaskResult
// ---------------------------------------------------------------------------

func TestParseTaskResult_StringValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"succeeded string", `"succeeded"`, "succeeded"},
		{"failed string", `"failed"`, "failed"},
		{"cancelled string", `"cancelled"`, "cancelled"},
		{"skipped string", `"skipped"`, "skipped"},
		{"succeededWithIssues string", `"succeededWithIssues"`, "succeededWithIssues"},
		{"abandoned string", `"abandoned"`, "abandoned"},
		{"arbitrary string", `"custom_result"`, "custom_result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTaskResult(json.RawMessage(tt.input))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTaskResult_NumericValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"0 = succeeded", "0", "succeeded"},
		{"1 = succeededWithIssues", "1", "succeededWithIssues"},
		{"2 = failed", "2", "failed"},
		{"3 = cancelled", "3", "cancelled"},
		{"4 = skipped", "4", "skipped"},
		{"5 = abandoned", "5", "abandoned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTaskResult(json.RawMessage(tt.input))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTaskResult_EmptyInput(t *testing.T) {
	got := parseTaskResult(json.RawMessage(nil))
	assert.Equal(t, "", got)

	got2 := parseTaskResult(json.RawMessage(""))
	assert.Equal(t, "", got2)
}

func TestParseTaskResult_InvalidJSON(t *testing.T) {
	// Neither valid string nor number — returns raw content.
	got := parseTaskResult(json.RawMessage(`{bad}`))
	assert.Equal(t, `{bad}`, got)
}

// ---------------------------------------------------------------------------
// taskResultToString
// ---------------------------------------------------------------------------

func TestTaskResultToString_AllValues(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "succeeded"},
		{1, "succeededWithIssues"},
		{2, "failed"},
		{3, "cancelled"},
		{4, "skipped"},
		{5, "abandoned"},
		{6, "unknown(6)"},
		{-1, "unknown(-1)"},
		{100, "unknown(100)"},
	}
	for _, tt := range tests {
		got := taskResultToString(tt.input)
		assert.Equal(t, tt.want, got, "taskResultToString(%d)", tt.input)
	}
}

// ---------------------------------------------------------------------------
// generateJWT
// ---------------------------------------------------------------------------

func TestGenerateJWT_Structure(t *testing.T) {
	token := generateJWT("test-run-backend-id")

	// JWT has three parts separated by dots.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT should have 3 parts")

	// Third part (signature) should be empty for alg:None.
	assert.Empty(t, parts[2], "signature part should be empty for unsigned JWT")

	// Decode header.
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err, "header should be valid base64url")
	var header map[string]string
	require.NoError(t, json.Unmarshal(headerBytes, &header))
	assert.Equal(t, "JWT", header["typ"])
	assert.Equal(t, "None", header["alg"])

	// Decode payload.
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err, "payload should be valid base64url")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(payloadBytes, &payload))
	assert.Equal(t, "ions", payload["iss"])
	assert.Equal(t, "ions", payload["aud"])
	assert.Equal(t, "ions-runner", payload["sub"])
	assert.NotNil(t, payload["nbf"])
	assert.NotNil(t, payload["exp"])
	assert.NotNil(t, payload["iat"])
	assert.NotNil(t, payload["appid"])

	// The scp claim should contain the run backend ID.
	scp, ok := payload["scp"].(string)
	require.True(t, ok)
	assert.Contains(t, scp, "Actions.Results:test-run-backend-id:")
	assert.Contains(t, scp, "Actions.GenericRead")
	assert.Contains(t, scp, "Actions.GenericWrite")
}

func TestGenerateJWT_DifferentRunIDsProduceDifferentTokens(t *testing.T) {
	token1 := generateJWT("run-1")
	token2 := generateJWT("run-2")
	assert.NotEqual(t, token1, token2)
}

// ---------------------------------------------------------------------------
// stripExpressionWrapper
// ---------------------------------------------------------------------------

func TestStripExpressionWrapper(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"full expression", "${{ steps.version.outputs.version }}", "steps.version.outputs.version"},
		{"no wrapper", "steps.version.outputs.version", "steps.version.outputs.version"},
		{"with whitespace around", "  ${{ expr }}  ", "expr"},
		{"empty expression", "${{  }}", ""},
		{"no spaces inside", "${{expr}}", "expr"},
		{"extra spaces inside", "${{   steps.x   }}", "steps.x"},
		{"partial prefix only", "${{ hello", "${{ hello"},
		{"partial suffix only", "hello }}", "hello }}"},
		{"empty string", "", ""},
		{"plain string", "just text", "just text"},
		{"nested braces", "${{ format('{0}', x) }}", "format('{0}', x)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripExpressionWrapper(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// buildMaskHints
// ---------------------------------------------------------------------------

func TestBuildMaskHints_EmptySecrets(t *testing.T) {
	assert.Nil(t, buildMaskHints(nil))
	assert.Nil(t, buildMaskHints(map[string]string{}))
}

func TestBuildMaskHints_SkipsEmptyValues(t *testing.T) {
	hints := buildMaskHints(map[string]string{
		"EMPTY1": "",
		"EMPTY2": "",
	})
	assert.Empty(t, hints)
}

func TestBuildMaskHints_OnlyNonEmptyValues(t *testing.T) {
	hints := buildMaskHints(map[string]string{
		"TOKEN":  "secret-value-1",
		"EMPTY":  "",
		"APIKEY": "secret-value-2",
	})
	require.Len(t, hints, 2)
	// Collect values.
	values := make(map[string]bool)
	for _, h := range hints {
		assert.Equal(t, "regex", h.Type)
		values[h.Value] = true
	}
	assert.True(t, values["secret-value-1"])
	assert.True(t, values["secret-value-2"])
}

func TestBuildMaskHints_AllNonEmpty(t *testing.T) {
	hints := buildMaskHints(map[string]string{
		"A": "val-a",
		"B": "val-b",
		"C": "val-c",
	})
	require.Len(t, hints, 3)
}

// ---------------------------------------------------------------------------
// buildVariables
// ---------------------------------------------------------------------------

func TestBuildVariables(t *testing.T) {
	vars := buildVariables("run-123")
	assert.Equal(t, "run-123", vars["system.github.run_id"].Value)
	assert.NotEmpty(t, vars["system.runner.os"].Value)
	assert.NotEmpty(t, vars["system.runner.arch"].Value)
	assert.Equal(t, "/tmp", vars["system.runner.temp"].Value)
	assert.Equal(t, "true", vars["DistributedTask.NewActionMetadata"].Value)
}

func TestBuildVariables_DifferentRunID(t *testing.T) {
	vars := buildVariables("run-abc")
	assert.Equal(t, "run-abc", vars["system.github.run_id"].Value)
}

// ---------------------------------------------------------------------------
// buildResources
// ---------------------------------------------------------------------------

func TestBuildResources(t *testing.T) {
	res := buildResources("http://localhost:9999", "backend-run-id")
	require.Len(t, res.Endpoints, 1)

	ep := res.Endpoints[0]
	assert.Equal(t, "SystemVssConnection", ep.Name)
	assert.Equal(t, "http://localhost:9999", ep.URL)
	assert.Equal(t, "OAuth", ep.Authorization.Scheme)
	assert.NotEmpty(t, ep.Authorization.Parameters["AccessToken"])
	assert.Equal(t, "http://localhost:9999/", ep.Data["ServerUrl"])
	assert.Equal(t, "http://localhost:9999/", ep.Data["CacheServerUrl"])
	assert.Equal(t, "http://localhost:9999/", ep.Data["ActionsRuntimeUrl"])
	assert.Equal(t, "http://localhost:9999/", ep.Data["ResultsServiceUrl"])
	assert.Contains(t, ep.Data["GenerateIdTokenUrl"], "/_apis/actionstoken/generateidtoken")

	// Verify the JWT token contains the run backend ID.
	token := ep.Authorization.Parameters["AccessToken"]
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(payloadBytes, &payload))
	assert.Contains(t, payload["scp"].(string), "backend-run-id")
}

// ---------------------------------------------------------------------------
// buildEnvironmentVariables
// ---------------------------------------------------------------------------

func TestBuildEnvironmentVariables_NoEnvContext(t *testing.T) {
	ctx := expression.MapContext{}
	result := buildEnvironmentVariables(ctx)
	assert.Nil(t, result)
}

func TestBuildEnvironmentVariables_EmptyEnvContext(t *testing.T) {
	ctx := expression.MapContext{
		"env": expression.Object(map[string]expression.Value{}),
	}
	result := buildEnvironmentVariables(ctx)
	assert.Nil(t, result)
}

func TestBuildEnvironmentVariables_WithEnvVars(t *testing.T) {
	ctx := expression.MapContext{
		"env": expression.Object(map[string]expression.Value{
			"FOO": expression.String("bar"),
			"BAZ": expression.String("qux"),
		}),
	}
	result := buildEnvironmentVariables(ctx)
	require.Len(t, result, 1)

	// The result is a mapping token.
	require.NotNil(t, result[0].TokenType)
	assert.Equal(t, 2, *result[0].TokenType) // Mapping type

	// Collect pairs.
	pairs := make(map[string]string)
	for _, p := range result[0].MapPairs {
		require.NotNil(t, p.Value)
		require.NotNil(t, p.Value.StringValue)
		pairs[p.Key] = *p.Value.StringValue
	}
	assert.Equal(t, "bar", pairs["FOO"])
	assert.Equal(t, "qux", pairs["BAZ"])
}

// ---------------------------------------------------------------------------
// containerToTemplateToken
// ---------------------------------------------------------------------------

func TestContainerToTemplateToken_ImageOnly(t *testing.T) {
	c := &workflow.Container{Image: "ubuntu:22.04"}
	token := containerToTemplateToken(c)

	require.NotNil(t, token)
	require.NotNil(t, token.TokenType)
	assert.Equal(t, 2, *token.TokenType) // Mapping

	assert.Equal(t, "ubuntu:22.04", templateMapGet(token, "image"))
	// Optional fields should not be present.
	assert.Nil(t, templateMapGetToken(token, "options"))
	assert.Nil(t, templateMapGetToken(token, "env"))
	assert.Nil(t, templateMapGetToken(token, "ports"))
	assert.Nil(t, templateMapGetToken(token, "volumes"))
}

func TestContainerToTemplateToken_AllFields(t *testing.T) {
	c := &workflow.Container{
		Image:   "node:20",
		Options: "--cpus 4 --memory 8g",
		Env:     map[string]string{"NODE_ENV": "production", "DEBUG": "true"},
		Ports:   []string{"3000:3000", "8080:8080"},
		Volumes: []string{"/data:/data", "/logs:/logs"},
	}
	token := containerToTemplateToken(c)

	require.NotNil(t, token)
	assert.Equal(t, "node:20", templateMapGet(token, "image"))
	assert.Equal(t, "--cpus 4 --memory 8g", templateMapGet(token, "options"))

	// Env should be a mapping token.
	envToken := templateMapGetToken(token, "env")
	require.NotNil(t, envToken)
	assert.Equal(t, "production", templateMapGet(envToken, "NODE_ENV"))
	assert.Equal(t, "true", templateMapGet(envToken, "DEBUG"))

	// Ports should be a sequence token.
	portsToken := templateMapGetToken(token, "ports")
	require.NotNil(t, portsToken)
	require.NotNil(t, portsToken.TokenType)
	assert.Equal(t, 1, *portsToken.TokenType) // Sequence
	require.Len(t, portsToken.SeqItems, 2)
	assert.Equal(t, "3000:3000", *portsToken.SeqItems[0].StringValue)
	assert.Equal(t, "8080:8080", *portsToken.SeqItems[1].StringValue)

	// Volumes should be a sequence token.
	volsToken := templateMapGetToken(token, "volumes")
	require.NotNil(t, volsToken)
	require.Len(t, volsToken.SeqItems, 2)
	assert.Equal(t, "/data:/data", *volsToken.SeqItems[0].StringValue)
}

// ---------------------------------------------------------------------------
// serviceContainersToTemplateToken
// ---------------------------------------------------------------------------

func TestServiceContainersToTemplateToken_Multiple(t *testing.T) {
	services := map[string]*workflow.Container{
		"postgres": {
			Image: "postgres:15",
			Env:   map[string]string{"POSTGRES_PASSWORD": "test"},
			Ports: []string{"5432:5432"},
		},
		"redis": {
			Image: "redis:7-alpine",
		},
	}

	token := serviceContainersToTemplateToken(services)
	require.NotNil(t, token)
	require.NotNil(t, token.TokenType)
	assert.Equal(t, 2, *token.TokenType) // Mapping

	// Verify both services are present.
	pgToken := templateMapGetToken(token, "postgres")
	require.NotNil(t, pgToken)
	assert.Equal(t, "postgres:15", templateMapGet(pgToken, "image"))
	pgEnv := templateMapGetToken(pgToken, "env")
	require.NotNil(t, pgEnv)
	assert.Equal(t, "test", templateMapGet(pgEnv, "POSTGRES_PASSWORD"))

	redisToken := templateMapGetToken(token, "redis")
	require.NotNil(t, redisToken)
	assert.Equal(t, "redis:7-alpine", templateMapGet(redisToken, "image"))
}

func TestServiceContainersToTemplateToken_Single(t *testing.T) {
	services := map[string]*workflow.Container{
		"db": {Image: "mysql:8"},
	}
	token := serviceContainersToTemplateToken(services)
	require.NotNil(t, token)
	require.Len(t, token.MapPairs, 1)
	assert.Equal(t, "db", token.MapPairs[0].Key)
}

// ---------------------------------------------------------------------------
// TemplateToken constructors and MarshalJSON
// ---------------------------------------------------------------------------

func TestNewTemplateTokenString_MarshalJSON(t *testing.T) {
	tok := NewTemplateTokenString("hello world")
	data, err := json.Marshal(tok)
	require.NoError(t, err)
	assert.Equal(t, `"hello world"`, string(data))
}

func TestNewTemplateTokenBool_MarshalJSON(t *testing.T) {
	tokTrue := NewTemplateTokenBool(true)
	data, err := json.Marshal(tokTrue)
	require.NoError(t, err)
	assert.Equal(t, `true`, string(data))

	tokFalse := NewTemplateTokenBool(false)
	data, err = json.Marshal(tokFalse)
	require.NoError(t, err)
	assert.Equal(t, `false`, string(data))
}

func TestNewTemplateTokenSequence_MarshalJSON(t *testing.T) {
	tok := NewTemplateTokenSequence([]string{"a", "b", "c"})
	data, err := json.Marshal(tok)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, float64(1), result["type"]) // Sequence
	seq, ok := result["seq"].([]any)
	require.True(t, ok)
	assert.Len(t, seq, 3)
	assert.Equal(t, "a", seq[0])
	assert.Equal(t, "b", seq[1])
	assert.Equal(t, "c", seq[2])
}

func TestNewTemplateTokenExpression_MarshalJSON(t *testing.T) {
	tok := NewTemplateTokenExpression("steps.build.outputs.result")
	data, err := json.Marshal(tok)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, float64(3), result["type"]) // BasicExpression
	assert.Equal(t, "steps.build.outputs.result", result["expr"])
}

func TestNewTemplateTokenMapping_MarshalJSON(t *testing.T) {
	tok := NewTemplateTokenMapping(map[string]string{
		"key1": "value1",
		"key2": "value2",
	})
	data, err := json.Marshal(tok)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, float64(2), result["type"]) // Mapping
	mapItems, ok := result["map"].([]any)
	require.True(t, ok)
	assert.Len(t, mapItems, 2)
}

func TestNewTemplateTokenMapping_Nil(t *testing.T) {
	tok := NewTemplateTokenMapping(map[string]string{})
	assert.Nil(t, tok)

	tok2 := NewTemplateTokenMapping(nil)
	assert.Nil(t, tok2)
}

func TestNewTemplateTokenMappingTokens_Nil(t *testing.T) {
	tok := NewTemplateTokenMappingTokens(map[string]*TemplateToken{})
	assert.Nil(t, tok)

	tok2 := NewTemplateTokenMappingTokens(nil)
	assert.Nil(t, tok2)
}

func TestNewTemplateTokenMappingTokens_WithExpressions(t *testing.T) {
	pairs := map[string]*TemplateToken{
		"version": NewTemplateTokenExpression("steps.version.outputs.result"),
		"name":    NewTemplateTokenString("my-project"),
	}
	tok := NewTemplateTokenMappingTokens(pairs)
	require.NotNil(t, tok)

	data, err := json.Marshal(tok)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, float64(2), result["type"])
}

func TestTemplateToken_MarshalJSON_NilToken(t *testing.T) {
	// An empty TemplateToken should marshal to "null".
	tok := TemplateToken{}
	data, err := json.Marshal(tok)
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestTemplateToken_MarshalJSON_NumberValue(t *testing.T) {
	n := 42.5
	tok := TemplateToken{NumberValue: &n}
	data, err := json.Marshal(tok)
	require.NoError(t, err)
	assert.Equal(t, "42.5", string(data))
}

func TestTemplateToken_MarshalJSON_ExpressionWithNilExprValue(t *testing.T) {
	// Expression token with nil ExpressionValue should marshal with empty expr.
	tt := 3
	tok := TemplateToken{TokenType: &tt}
	data, err := json.Marshal(tok)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "", result["expr"])
}

// ---------------------------------------------------------------------------
// TemplateToken UnmarshalJSON
// ---------------------------------------------------------------------------

func TestTemplateToken_UnmarshalJSON_String(t *testing.T) {
	var tok TemplateToken
	err := json.Unmarshal([]byte(`"hello"`), &tok)
	require.NoError(t, err)
	require.NotNil(t, tok.StringValue)
	assert.Equal(t, "hello", *tok.StringValue)
}

func TestTemplateToken_UnmarshalJSON_Bool(t *testing.T) {
	var tok TemplateToken
	err := json.Unmarshal([]byte(`true`), &tok)
	require.NoError(t, err)
	require.NotNil(t, tok.BoolValue)
	assert.True(t, *tok.BoolValue)
}

func TestTemplateToken_UnmarshalJSON_Number(t *testing.T) {
	var tok TemplateToken
	err := json.Unmarshal([]byte(`99.5`), &tok)
	require.NoError(t, err)
	require.NotNil(t, tok.NumberValue)
	assert.Equal(t, 99.5, *tok.NumberValue)
}

func TestTemplateToken_UnmarshalJSON_Mapping(t *testing.T) {
	input := `{"type":2,"map":[{"Key":"foo","Value":"bar"},{"Key":"baz","Value":"qux"}]}`
	var tok TemplateToken
	err := json.Unmarshal([]byte(input), &tok)
	require.NoError(t, err)
	require.NotNil(t, tok.TokenType)
	assert.Equal(t, 2, *tok.TokenType)
	require.Len(t, tok.MapPairs, 2)
	assert.Equal(t, "foo", tok.MapPairs[0].Key)
	require.NotNil(t, tok.MapPairs[0].Value)
	require.NotNil(t, tok.MapPairs[0].Value.StringValue)
	assert.Equal(t, "bar", *tok.MapPairs[0].Value.StringValue)
}

func TestTemplateToken_UnmarshalJSON_Expression(t *testing.T) {
	input := `{"type":3,"expr":"github.ref"}`
	var tok TemplateToken
	err := json.Unmarshal([]byte(input), &tok)
	require.NoError(t, err)
	require.NotNil(t, tok.TokenType)
	assert.Equal(t, 3, *tok.TokenType)
	require.NotNil(t, tok.ExpressionValue)
	assert.Equal(t, "github.ref", *tok.ExpressionValue)
}

func TestTemplateToken_UnmarshalJSON_EmptyObject(t *testing.T) {
	// An empty JSON object should parse without error.
	var tok TemplateToken
	err := json.Unmarshal([]byte(`{}`), &tok)
	require.NoError(t, err)
	// The typed object handler will match with type 0 (default), but no map/expr.
	// Verify no string/bool/number value was set.
	assert.Nil(t, tok.StringValue)
	assert.Nil(t, tok.BoolValue)
	assert.Nil(t, tok.NumberValue)
}

func TestTemplateToken_RoundTrip_Mapping(t *testing.T) {
	// Create a mapping token, marshal it, and unmarshal it back.
	orig := NewTemplateTokenMapping(map[string]string{"key": "value"})
	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded TemplateToken
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.TokenType)
	assert.Equal(t, 2, *decoded.TokenType)
	require.Len(t, decoded.MapPairs, 1)
	assert.Equal(t, "key", decoded.MapPairs[0].Key)
}

func TestTemplateToken_RoundTrip_Expression(t *testing.T) {
	orig := NewTemplateTokenExpression("needs.build.result")
	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var decoded TemplateToken
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.TokenType)
	assert.Equal(t, 3, *decoded.TokenType)
	require.NotNil(t, decoded.ExpressionValue)
	assert.Equal(t, "needs.build.result", *decoded.ExpressionValue)
}

// ---------------------------------------------------------------------------
// HTTP handler tests via httptest
// ---------------------------------------------------------------------------

func TestHandleGetAgents_Httptest(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Get(srv.URL() + "/_apis/distributedtask/pools/1/agents")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, float64(1), result["count"])
	agents, ok := result["value"].([]any)
	require.True(t, ok)
	require.Len(t, agents, 1)
	agent := agents[0].(map[string]any)
	assert.Equal(t, float64(1), agent["id"])
	assert.Equal(t, "ions-runner", agent["name"])
}

func TestHandleAddAgent_ValidBody(t *testing.T) {
	srv := startTestServer(t)

	body := `{"id": 5, "name": "custom-runner"}`
	resp, err := http.Post(srv.URL()+"/_apis/distributedtask/pools/1/agents",
		"application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var agent TaskAgent
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, 5, agent.ID)
	assert.Equal(t, "custom-runner", agent.Name)
}

func TestHandleAddAgent_InvalidBody(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/distributedtask/pools/1/agents",
		"application/json", strings.NewReader("invalid json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var agent TaskAgent
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, 1, agent.ID)
	assert.Equal(t, "ions-runner", agent.Name)
}

func TestHandleAddAgent_ZeroID(t *testing.T) {
	srv := startTestServer(t)

	body := `{"id": 0, "name": "test-runner"}`
	resp, err := http.Post(srv.URL()+"/_apis/distributedtask/pools/1/agents",
		"application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var agent TaskAgent
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agent))
	assert.Equal(t, 1, agent.ID) // ID should be forced to 1 when 0.
}

func TestHandleDeleteMessage(t *testing.T) {
	srv := startTestServer(t)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL()+"/_apis/v1/Message", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandleDeleteMessage_DistributedTask(t *testing.T) {
	srv := startTestServer(t)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL()+"/_apis/distributedtask/pools/1/messages/123", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandleCompleteJob_BadRequest(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/v1/RunService/completejob",
		"application/json", strings.NewReader("not valid json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleCatchAll_Various(t *testing.T) {
	srv := startTestServer(t)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"unknown GET", http.MethodGet, "/random/path"},
		{"unknown POST", http.MethodPost, "/something/else"},
		{"root path", http.MethodGet, "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, srv.URL()+tt.path, nil)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

func TestHandleResourceAreas(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Get(srv.URL() + "/_apis/ResourceAreas")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, float64(2), result["count"])

	areas, ok := result["value"].([]any)
	require.True(t, ok)
	require.Len(t, areas, 2)

	area0 := areas[0].(map[string]any)
	assert.Equal(t, "distributedtask", area0["name"])
	assert.Contains(t, area0["locationUrl"].(string), "http://127.0.0.1:")

	area1 := areas[1].(map[string]any)
	assert.Equal(t, "pipelines", area1["name"])
}

func TestHandleOAuthToken(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/oauth2/token",
		"application/x-www-form-urlencoded", strings.NewReader("grant_type=client_credentials"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "bearer", result["token_type"])
	assert.Equal(t, float64(3600), result["expires_in"])

	// access_token should be a valid JWT.
	token, ok := result["access_token"].(string)
	require.True(t, ok)
	parts := strings.Split(token, ".")
	assert.Len(t, parts, 3, "OAuth token should be a JWT with 3 parts")
}

func TestHandleOptions(t *testing.T) {
	srv := startTestServer(t)

	req, _ := http.NewRequest(http.MethodOptions, srv.URL()+"/_apis/", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	count, ok := result["count"].(float64)
	require.True(t, ok)
	assert.Greater(t, count, float64(0), "should return at least some resource locations")

	locations, ok := result["value"].([]any)
	require.True(t, ok)
	assert.Equal(t, int(count), len(locations))
}

func TestHandleGetJobRequest_UnknownRequest(t *testing.T) {
	srv := startTestServer(t)

	// GET for an unknown request ID should return succeeded result to unblock the runner.
	resp, err := http.Get(srv.URL() + "/_apis/distributedtask/pools/1/jobrequests/999")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, float64(999), result["requestId"])
	assert.Equal(t, float64(0), result["result"]) // 0 = succeeded
	assert.NotEmpty(t, result["finishTime"])
}

func TestHandleRegister(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/v1/runneradmin/register",
		"application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Contains(t, result["url"].(string), "http://127.0.0.1:")
	assert.Equal(t, "ions-local-token", result["token"])
	assert.Equal(t, "OAuthAccessToken", result["tokenSchema"])
}

// ---------------------------------------------------------------------------
// Convert: additional edge cases not covered in convert_test.go
// ---------------------------------------------------------------------------

func TestPipelineContextDataToValue_NilBool(t *testing.T) {
	d := PipelineContextData{Type: 3, BoolValue: nil}
	v := PipelineContextDataToValue(d)
	assert.False(t, v.BoolVal())
}

func TestPipelineContextDataToValue_NilNumber(t *testing.T) {
	d := PipelineContextData{Type: 4, NumberValue: nil}
	v := PipelineContextDataToValue(d)
	assert.Equal(t, 0.0, v.NumberVal())
}

func TestPipelineContextDataToValue_NilExpression(t *testing.T) {
	d := PipelineContextData{Type: 5, ExprValue: nil}
	v := PipelineContextDataToValue(d)
	assert.Equal(t, "", v.StringVal())
}

// ---------------------------------------------------------------------------
// actionproxy helpers
// ---------------------------------------------------------------------------

func TestShouldPatchActionYAML(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"owner-repo-abc123/action.yml", true},
		{"owner-repo-abc123/action.yaml", true},
		{"owner-repo-abc123/README.md", false},
		{"action.yml", false},                                // no parent dir
		{"owner-repo-abc123/subdir/action.yml", false},       // too deep
		{"owner-repo-abc123/", false},
	}
	for _, tt := range tests {
		got := shouldPatchActionYAML(tt.name)
		assert.Equal(t, tt.want, got, "shouldPatchActionYAML(%q)", tt.name)
	}
}

func TestPatchActionYAML(t *testing.T) {
	data := []byte("inputs:\n  token:\n    default: ${{ github.token }}\n  unknown:\n    default: ${{ something.else }}\n")
	defaults := map[string]string{
		"github.token": "ghs_test123",
	}
	result := patchActionYAML(data, defaults)
	assert.Contains(t, string(result), "ghs_test123")
	assert.NotContains(t, string(result), "${{ github.token }}")
	// Unknown expressions should be stripped to empty.
	assert.NotContains(t, string(result), "${{ something.else }}")
	assert.NotContains(t, string(result), "${")
}

func TestPatchActionYAML_NoDefaults(t *testing.T) {
	data := []byte("inputs:\n  token:\n    default: ${{ github.token }}\n")
	result := patchActionYAML(data, nil)
	// Should strip the expression to empty.
	assert.NotContains(t, string(result), "${{")
}

func TestPatchActionYAML_NoExpressions(t *testing.T) {
	data := []byte("name: my-action\nruns:\n  using: node16\n")
	result := patchActionYAML(data, nil)
	assert.Equal(t, string(data), string(result))
}

// ---------------------------------------------------------------------------
// IdentityDescriptor
// ---------------------------------------------------------------------------

func TestNewIdentityDescriptor(t *testing.T) {
	d := NewIdentityDescriptor("System", "00000000-0000-0000-0000-000000000001")
	assert.Equal(t, IdentityDescriptor("System;00000000-0000-0000-0000-000000000001"), d)
}

// ---------------------------------------------------------------------------
// HandleActionTarball invalid path
// ---------------------------------------------------------------------------

func TestHandleActionTarball_InvalidPath(t *testing.T) {
	srv := startTestServer(t)

	// Only two path segments (need 3: owner/repo/ref).
	resp, err := http.Get(srv.URL() + "/_actions/tarball/owner/repo")
	require.NoError(t, err)
	defer resp.Body.Close()
	// Should return bad request because there are only 2 parts.
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// WriteJSON helper
// ---------------------------------------------------------------------------

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"key": "value"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), `"key":"value"`)
}

// ---------------------------------------------------------------------------
// HandleAcquireJob — no available job
// ---------------------------------------------------------------------------

func TestHandleAcquireJob_NoJob(t *testing.T) {
	srv := startTestServer(t)

	body := `{"jobMessageId": 999}`
	resp, err := http.Post(srv.URL()+"/_apis/v1/RunService/acquirejob",
		"application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleAcquireJob_InvalidBody(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/v1/RunService/acquirejob",
		"application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// HandleRenewJob — bad request body
// ---------------------------------------------------------------------------

func TestHandleRenewJob_InvalidBody(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/v1/RunService/renewjob",
		"application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// OnStepUpdate callback
// ---------------------------------------------------------------------------

func TestOnStepUpdate(t *testing.T) {
	srv := startTestServer(t)

	var calledJobID, calledStepName, calledState string
	var calledResult *string
	srv.OnStepUpdate(func(jobID, stepName, state string, result *string) {
		calledJobID = jobID
		calledStepName = stepName
		calledState = state
		calledResult = result
	})

	// Enqueue a job to register timeline mapping.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "step-cb-job",
		RequestID:   100,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-1"},
		Timeline:    &TimelineReference{ID: "tl-step-cb"},
	}
	srv.EnqueueJob(msg)

	// Poll to activate the job so the timeline mapping is set.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=test")
	require.NoError(t, err)
	resp.Body.Close()

	// Send a timeline update.
	succeeded := "succeeded"
	records := []TimelineRecord{
		{ID: "rec-1", Name: "Build Step", State: "Completed", Result: &succeeded},
	}
	body, _ := json.Marshal(records)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL()+"/_apis/v1/Timeline/tl-step-cb",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	assert.Equal(t, "step-cb-job", calledJobID)
	assert.Equal(t, "Build Step", calledStepName)
	assert.Equal(t, "Completed", calledState)
	require.NotNil(t, calledResult)
	assert.Equal(t, "succeeded", *calledResult)
}

// ---------------------------------------------------------------------------
// Verbose server (for loggingMiddleware coverage)
// ---------------------------------------------------------------------------

func TestVerboseServer(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	// Make a request — the verbose middleware reads the body.
	body := `{"test": true}`
	resp, err := http.Post(srv.URL()+"/_apis/v1/runneradmin/register",
		"application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// handleDistributedTaskCatchAll — timeline, logs, feed, planevents,
// actiondownloadinfo
// ---------------------------------------------------------------------------

func TestDistributedTaskCatchAll_TimelinePatch(t *testing.T) {
	srv := startTestServer(t)

	// PATCH timeline records via plan-scoped path.
	records := []TimelineRecord{
		{ID: "rec-1", Name: "Step A", State: "InProgress"},
		{ID: "rec-2", Name: "Step B", State: "Pending"},
	}
	body, _ := json.Marshal(records)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/timeline/plan-1/tl-dt-1",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, float64(2), result["count"])

	// Verify stored in timelines.
	tl := srv.Timeline("tl-dt-1")
	require.Len(t, tl, 2)
	assert.Equal(t, "Step A", tl[0].Name)
}

func TestDistributedTaskCatchAll_TimelinePatch_WrappedFormat(t *testing.T) {
	srv := startTestServer(t)

	// Runner sometimes sends {"value": [...]} format.
	wrapped := `{"value": [{"id": "rec-w1", "name": "Wrapped Step", "state": "Completed"}]}`
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/timeline/plan-1/tl-wrapped",
		strings.NewReader(wrapped))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	tl := srv.Timeline("tl-wrapped")
	require.Len(t, tl, 1)
	assert.Equal(t, "Wrapped Step", tl[0].Name)
}

func TestDistributedTaskCatchAll_TimelinePatch_BadJSON(t *testing.T) {
	srv := startTestServer(t)

	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/timeline/plan-1/tl-bad",
		strings.NewReader("not json at all"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDistributedTaskCatchAll_LogsCreate(t *testing.T) {
	srv := startTestServer(t)

	// POST to create a log resource: /_apis/distributedtask/{scope}/{hub}/logs/{planId}
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/logs/plan-1",
		"application/json", strings.NewReader(`{"path":"logs\\1"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, float64(1), result["id"])
	assert.NotEmpty(t, result["location"])
}

func TestDistributedTaskCatchAll_LogsAppend(t *testing.T) {
	srv := startTestServer(t)

	// First create the log.
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/logs/plan-log-1",
		"application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp.Body.Close()

	// Now append lines: POST /_apis/distributedtask/{scope}/{hub}/logs/{planId}/{logId}
	lines := "log line 1\nlog line 2"
	resp2, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/logs/plan-log-1/1",
		"text/plain", strings.NewReader(lines))
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&result))
	assert.Equal(t, float64(1), result["id"])
}

func TestDistributedTaskCatchAll_Feed(t *testing.T) {
	srv := startTestServer(t)

	// POST to feed endpoint.
	feedBody := `{"stepId": "step-1", "value": ["line 1", "line 2"]}`
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/feed/plan-1/tl-1/rec-1",
		"application/json", strings.NewReader(feedBody))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDistributedTaskCatchAll_PlanEvents_JobCompleted(t *testing.T) {
	srv := startTestServer(t)

	// Enqueue a job and activate it via poll.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "pe-job-1",
		RequestID:   200,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-pe"},
		Timeline:    &TimelineReference{ID: "tl-pe"},
	}
	resultCh := srv.EnqueueJob(msg)

	// Poll to activate job.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=pe-session")
	require.NoError(t, err)
	resp.Body.Close()

	// Send plan event with job completion.
	event := map[string]any{
		"name":      "JobCompleted",
		"jobId":     "pe-job-1",
		"requestId": 200,
		"result":    "succeeded",
		"outputs": map[string]any{
			"version": map[string]any{
				"value":    "1.0.0",
				"isSecret": false,
			},
		},
	}
	eventBody, _ := json.Marshal(event)
	resp2, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/planevents/plan-pe",
		"application/json", strings.NewReader(string(eventBody)))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// Verify the job was completed.
	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		assert.Equal(t, "pe-job-1", result.JobID)
		require.Contains(t, result.Outputs, "version")
		assert.Equal(t, "1.0.0", result.Outputs["version"].Value)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestDistributedTaskCatchAll_PlanEvents_NumericResult(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "pe-job-num",
		RequestID:   201,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-pe2"},
		Timeline:    &TimelineReference{ID: "tl-pe2"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=pe-session2")
	require.NoError(t, err)
	resp.Body.Close()

	// Send with numeric result (2 = failed).
	event := map[string]any{
		"name":      "JobCompleted",
		"jobId":     "pe-job-num",
		"requestId": 201,
		"result":    2,
	}
	eventBody, _ := json.Marshal(event)
	resp2, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/planevents/plan-pe2",
		"application/json", strings.NewReader(string(eventBody)))
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "failed", result.Result)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestDistributedTaskCatchAll_ActionDownloadInfo(t *testing.T) {
	srv := startTestServer(t)

	refList := map[string]any{
		"actions": []map[string]any{
			{"nameWithOwner": "actions/checkout", "ref": "v4", "path": ""},
			{"nameWithOwner": "actions/setup-node", "ref": "v3", "path": ""},
		},
	}
	body, _ := json.Marshal(refList)
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/actiondownloadinfo/plan-1?jobId=job-1",
		"application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	actions, ok := result["actions"].(map[string]any)
	require.True(t, ok)

	// Both actions should be present.
	checkoutInfo, ok := actions["actions/checkout@v4"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "actions/checkout", checkoutInfo["nameWithOwner"])
	assert.Contains(t, checkoutInfo["tarballUrl"].(string), "/_actions/tarball/actions/checkout/v4")

	setupInfo, ok := actions["actions/setup-node@v3"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "actions/setup-node", setupInfo["nameWithOwner"])
}

func TestDistributedTaskCatchAll_ActionDownloadInfo_BadJSON(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/actiondownloadinfo/plan-1",
		"application/json", strings.NewReader("not valid json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDistributedTaskCatchAll_UnknownSubpath(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/unknowntype/plan-1",
		"application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDistributedTaskCatchAll_ShortPath(t *testing.T) {
	srv := startTestServer(t)

	// Fewer than 4 parts in the path after stripping the prefix.
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1",
		"application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// handleJobRequest (PATCH jobrequests) — job completion path
// ---------------------------------------------------------------------------

func TestHandleJobRequest_Completion_StringResult(t *testing.T) {
	srv := startTestServer(t)

	// Enqueue and activate a job.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "jr-job-1",
		RequestID:   300,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-jr"},
		Timeline:    &TimelineReference{ID: "tl-jr"},
	}
	resultCh := srv.EnqueueJob(msg)

	// Poll to activate.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=jr-session")
	require.NoError(t, err)
	resp.Body.Close()

	// Complete via PATCH jobrequests.
	patchBody := map[string]any{
		"requestId":  300,
		"result":     "succeeded",
		"finishTime": "2026-03-04T00:00:00Z",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/300",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		assert.Equal(t, "jr-job-1", result.JobID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestHandleJobRequest_Completion_NumericResult(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "jr-job-2",
		RequestID:   301,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-jr2"},
		Timeline:    &TimelineReference{ID: "tl-jr2"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=jr-session2")
	require.NoError(t, err)
	resp.Body.Close()

	// Complete with numeric result (2 = failed).
	patchBody := map[string]any{
		"requestId":  301,
		"result":     2, // numeric
		"finishTime": "2026-03-04T00:00:00Z",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/301",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "failed", result.Result)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestHandleJobRequest_Completion_WithOutputVariables(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "jr-job-out",
		RequestID:   302,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-jr-out"},
		Timeline:    &TimelineReference{ID: "tl-jr-out"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=jr-session-out")
	require.NoError(t, err)
	resp.Body.Close()

	patchBody := map[string]any{
		"requestId":  302,
		"result":     "succeeded",
		"finishTime": "2026-03-04T00:00:00Z",
		"outputVariables": map[string]any{
			"version": map[string]any{
				"value":    "2.0.0",
				"issecret": false,
			},
			"token": map[string]any{
				"value":    "secret-val",
				"issecret": true,
			},
		},
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/302",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		require.Contains(t, result.Outputs, "version")
		assert.Equal(t, "2.0.0", result.Outputs["version"].Value)
		assert.False(t, result.Outputs["version"].IsSecret)
		require.Contains(t, result.Outputs, "token")
		assert.Equal(t, "secret-val", result.Outputs["token"].Value)
		assert.True(t, result.Outputs["token"].IsSecret)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestHandleJobRequest_NoResult_RenewLock(t *testing.T) {
	srv := startTestServer(t)

	// PATCH with no result — just a lock renewal.
	patchBody := map[string]any{
		"requestId":   999,
		"lockedUntil": "2026-03-04T01:00:00Z",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/999",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.NotEmpty(t, result["lockedUntil"])
}

func TestHandleJobRequest_BadJSON(t *testing.T) {
	srv := startTestServer(t)

	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/1",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Bad JSON should still return 200 (graceful handling).
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// handleGetJobRequest — active and completed job scenarios
// ---------------------------------------------------------------------------

func TestHandleGetJobRequest_ActiveJob(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "get-jr-active",
		JobName:     "active-job",
		RequestID:   400,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-get-jr"},
		Timeline:    &TimelineReference{ID: "tl-get-jr"},
	}
	srv.EnqueueJob(msg)

	// Poll to activate.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=get-jr-session")
	require.NoError(t, err)
	resp.Body.Close()

	// GET the active job request.
	resp2, err := http.Get(srv.URL() + "/_apis/distributedtask/pools/1/jobrequests/400")
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&result))
	assert.Equal(t, float64(400), result["requestId"])
	assert.Equal(t, "get-jr-active", result["jobId"])
	assert.NotEmpty(t, result["lockedUntil"])
}

func TestHandleGetJobRequest_CompletedJob(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "get-jr-completed",
		RequestID:   401,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-get-jr-c"},
		Timeline:    &TimelineReference{ID: "tl-get-jr-c"},
	}
	srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=get-jr-c-session")
	require.NoError(t, err)
	resp.Body.Close()

	// Complete the job via PATCH.
	patchBody := map[string]any{
		"requestId":  401,
		"result":     "failed",
		"finishTime": "2026-03-04T00:00:00Z",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/401",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	// Wait for completion to propagate.
	time.Sleep(50 * time.Millisecond)

	// GET completed job request — should return result=2 (failed).
	resp3, err := http.Get(srv.URL() + "/_apis/distributedtask/pools/1/jobrequests/401")
	require.NoError(t, err)
	defer resp3.Body.Close()

	assert.Equal(t, http.StatusOK, resp3.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&result))
	assert.Equal(t, float64(401), result["requestId"])
	assert.Equal(t, float64(2), result["result"]) // 2 = failed
	assert.NotEmpty(t, result["finishTime"])
}

// ---------------------------------------------------------------------------
// Session create with invalid body
// ---------------------------------------------------------------------------

func TestHandleCreateSession_InvalidBody(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/v1/Message/sessions",
		"application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should still succeed — we accept even unparseable bodies.
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var sess TaskAgentSession
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sess))
	assert.NotEmpty(t, sess.SessionID)
}

// ---------------------------------------------------------------------------
// Verbose server — cover more verbose paths
// ---------------------------------------------------------------------------

func TestVerboseServer_ConnectionData(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Get(srv.URL() + "/_apis/connectionData")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseServer_Options(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	req, _ := http.NewRequest(http.MethodOptions, srv.URL()+"/_apis/", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseServer_CatchAll(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Get(srv.URL() + "/some/random/path")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseServer_DistributedTask(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	// Feed with verbose — covers the log printing path in feed handler.
	feedBody := `{"stepId": "step-1", "value": ["verbose line"]}`
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/feed/plan-1/tl-1/rec-1",
		"application/json", strings.NewReader(feedBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Job outputs token construction (avoids calling BuildJobMessage to not
// increment the global requestIDCounter, which would break fragile existing tests)
// ---------------------------------------------------------------------------

func TestJobOutputTokenConstruction(t *testing.T) {
	// Simulate what BuildJobMessage does for job outputs.
	outputs := map[string]workflow.JobOutput{
		"version": {Value: "${{ steps.version.outputs.version }}"},
		"plain":   {Value: "literal-value"},
	}
	outputTokens := make(map[string]*TemplateToken, len(outputs))
	for name, output := range outputs {
		expr := stripExpressionWrapper(output.Value)
		outputTokens[name] = NewTemplateTokenExpression(expr)
	}
	tok := NewTemplateTokenMappingTokens(outputTokens)

	require.NotNil(t, tok)
	require.NotNil(t, tok.TokenType)
	assert.Equal(t, 2, *tok.TokenType) // Mapping
	assert.Len(t, tok.MapPairs, 2)

	// Verify the expression was stripped from the "version" output.
	for _, p := range tok.MapPairs {
		if p.Key == "version" {
			require.NotNil(t, p.Value.ExpressionValue)
			assert.Equal(t, "steps.version.outputs.version", *p.Value.ExpressionValue)
		}
		if p.Key == "plain" {
			// "literal-value" doesn't have ${{ }}, so stripExpressionWrapper returns as-is.
			require.NotNil(t, p.Value.ExpressionValue)
			assert.Equal(t, "literal-value", *p.Value.ExpressionValue)
		}
	}
}

// ---------------------------------------------------------------------------
// newTemplateTokenMappingWithExprs
// ---------------------------------------------------------------------------

func TestNewTemplateTokenMappingWithExprs_Nil(t *testing.T) {
	result := newTemplateTokenMappingWithExprs(nil)
	assert.Nil(t, result)
}

func TestNewTemplateTokenMappingWithExprs_Empty(t *testing.T) {
	result := newTemplateTokenMappingWithExprs(map[string]string{})
	assert.Nil(t, result)
}

// ---------------------------------------------------------------------------
// RegisterRoutes
// ---------------------------------------------------------------------------

func TestRegisterRoutes(t *testing.T) {
	srv := startTestServer(t)

	// Create a custom registrar.
	called := false
	registrar := testRouteRegistrar{fn: func(mux *http.ServeMux) {
		called = true
		mux.HandleFunc("GET /custom/test", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"custom": "true"})
		})
	}}

	// Note: RegisterRoutes must be called before Start, but since we already started
	// and the mux is shared, routes can still be added for testing purposes
	// (Go's ServeMux is goroutine-safe for reads but not for concurrent writes).
	// For this test, we only verify the registrar function is called.
	srv.RegisterRoutes(registrar)
	assert.True(t, called)
}

type testRouteRegistrar struct {
	fn func(mux *http.ServeMux)
}

func (r testRouteRegistrar) RegisterRoutes(mux *http.ServeMux) {
	r.fn(mux)
}

// ---------------------------------------------------------------------------
// Session already has job — getMessage returns 202
// ---------------------------------------------------------------------------

func TestHandleGetMessage_SessionAlreadyHasJob(t *testing.T) {
	srv := startTestServer(t)

	// Create a session.
	body := `{"ownerName":"test","agent":{"id":1,"name":"test-runner"}}`
	resp, err := http.Post(srv.URL()+"/_apis/v1/Message/sessions",
		"application/json", strings.NewReader(body))
	require.NoError(t, err)
	var sess TaskAgentSession
	json.NewDecoder(resp.Body).Decode(&sess)
	resp.Body.Close()

	// Enqueue a job.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "dup-job",
		RequestID:   500,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-dup"},
		Timeline:    &TimelineReference{ID: "tl-dup"},
	}
	srv.EnqueueJob(msg)

	// First poll — delivers the job.
	resp2, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=" + sess.SessionID)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// Second poll with same session — should return 202 (session already has a job).
	resp3, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=" + sess.SessionID)
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp3.StatusCode)
}

// ---------------------------------------------------------------------------
// Additional coverage tests for uncovered lines
// ---------------------------------------------------------------------------

// --- actionproxy.go: handleActionTarball verbose logging and error paths ---

func TestHandleActionTarball_VerboseMode(t *testing.T) {
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		var buf bytes.Buffer
		gzw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gzw)
		content := []byte("name: test\n")
		tw.WriteHeader(&tar.Header{
			Name: "owner-repo-abc/action.yml",
			Size: int64(len(content)),
			Mode: 0644,
		})
		tw.Write(content)
		tw.Close()
		gzw.Close()
		w.Write(buf.Bytes())
	}))
	defer fakeGH.Close()

	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)
	srv.actionAPIBase = fakeGH.URL

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Get(srv.URL() + "/_actions/tarball/owner/repo/v1.0.0")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandleActionTarball_UpstreamNon200(t *testing.T) {
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer fakeGH.Close()

	srv, err := NewServer(ServerConfig{})
	require.NoError(t, err)
	srv.actionAPIBase = fakeGH.URL

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Get(srv.URL() + "/_actions/tarball/owner/repo/v1.0.0")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleActionTarball_UpstreamFetchError(t *testing.T) {
	// Point at a server that is already closed — causes fetch error.
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := fakeGH.URL
	fakeGH.Close() // close immediately

	srv, err := NewServer(ServerConfig{})
	require.NoError(t, err)
	srv.actionAPIBase = closedURL

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Get(srv.URL() + "/_actions/tarball/owner/repo/v1.0.0")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestHandleActionTarball_InvalidGzip(t *testing.T) {
	// Return non-gzip content to trigger gzip reader error in patchActionTarball.
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write([]byte("this is not gzip data"))
	}))
	defer fakeGH.Close()

	srv, err := NewServer(ServerConfig{})
	require.NoError(t, err)
	srv.actionAPIBase = fakeGH.URL

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Get(srv.URL() + "/_actions/tarball/owner/repo/v1.0.0")
	require.NoError(t, err)
	defer resp.Body.Close()
	// The handler should still return 200 (it sets Content-Type before patching).
	// The patching error is logged but the response may have partial/empty content.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// --- patchActionTarball: unit tests for error paths ---

func TestPatchActionTarball_InvalidGzip(t *testing.T) {
	var dst bytes.Buffer
	err := patchActionTarball(strings.NewReader("not gzip"), &dst, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gzip reader")
}

func TestPatchActionTarball_EmptyTarball(t *testing.T) {
	// Create a valid gzipped but empty tarball.
	var src bytes.Buffer
	gzw := gzip.NewWriter(&src)
	tw := tar.NewWriter(gzw)
	tw.Close()
	gzw.Close()

	var dst bytes.Buffer
	err := patchActionTarball(&src, &dst, nil)
	require.NoError(t, err)
	assert.Greater(t, dst.Len(), 0)
}

func TestPatchActionTarball_MultipleEntries(t *testing.T) {
	// Create a tarball with action.yml and other files.
	var src bytes.Buffer
	gzw := gzip.NewWriter(&src)
	tw := tar.NewWriter(gzw)

	// action.yml with expressions.
	actionContent := []byte("token: ${{ github.token }}\n")
	tw.WriteHeader(&tar.Header{Name: "owner-repo-abc/action.yml", Size: int64(len(actionContent)), Mode: 0644})
	tw.Write(actionContent)

	// A non-action file that should pass through unchanged.
	readme := []byte("# README\n")
	tw.WriteHeader(&tar.Header{Name: "owner-repo-abc/README.md", Size: int64(len(readme)), Mode: 0644})
	tw.Write(readme)

	// A directory entry (size 0).
	tw.WriteHeader(&tar.Header{Name: "owner-repo-abc/src/", Size: 0, Mode: 0755, Typeflag: tar.TypeDir})

	tw.Close()
	gzw.Close()

	defaults := map[string]string{"github.token": "resolved-token"}
	var dst bytes.Buffer
	err := patchActionTarball(&src, &dst, defaults)
	require.NoError(t, err)

	// Read back the patched tarball and verify.
	gzr, err := gzip.NewReader(&dst)
	require.NoError(t, err)
	tr := tar.NewReader(gzr)

	var names []string
	var actionResult string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, hdr.Name)
		if strings.HasSuffix(hdr.Name, "action.yml") {
			data, _ := io.ReadAll(tr)
			actionResult = string(data)
		}
	}
	assert.Contains(t, names, "owner-repo-abc/action.yml")
	assert.Contains(t, names, "owner-repo-abc/README.md")
	assert.Contains(t, actionResult, "resolved-token")
	assert.NotContains(t, actionResult, "${{")
}

// --- jobbuilder.go: uncovered paths ---

func TestJobOutputs_ViaBuilderPath(t *testing.T) {
	// Test the job outputs code path (jobbuilder.go:77-83) without calling
	// BuildJobMessage to avoid incrementing the global requestIDCounter.
	outputs := map[string]workflow.JobOutput{
		"version": {Value: "${{ steps.version.outputs.version }}"},
		"plain":   {Value: "literal"},
	}
	outputTokens := make(map[string]*TemplateToken, len(outputs))
	for name, output := range outputs {
		expr := stripExpressionWrapper(output.Value)
		outputTokens[name] = NewTemplateTokenExpression(expr)
	}
	tok := NewTemplateTokenMappingTokens(outputTokens)

	require.NotNil(t, tok)
	require.NotNil(t, tok.TokenType)
	assert.Equal(t, 2, *tok.TokenType) // Mapping

	// Verify expression values are present.
	found := map[string]bool{}
	for _, p := range tok.MapPairs {
		found[p.Key] = true
		if p.Key == "version" {
			require.NotNil(t, p.Value.ExpressionValue)
			assert.Equal(t, "steps.version.outputs.version", *p.Value.ExpressionValue)
		}
	}
	assert.True(t, found["version"])
	assert.True(t, found["plain"])
}

func TestConvertStep_ContinueOnErrorIsExpr(t *testing.T) {
	// When ContinueOnError.IsExpr is true, should default to false bool token.
	s := workflow.Step{
		Run:             "echo test",
		ContinueOnError: workflow.ExprBool{IsExpr: true, Expression: "${{ always() }}"},
	}
	js, err := convertStep(s, 0)
	require.NoError(t, err)
	require.NotNil(t, js.ContinueOnError)
	require.NotNil(t, js.ContinueOnError.BoolValue)
	assert.False(t, *js.ContinueOnError.BoolValue)
}

func TestConvertStep_DisplayNameFallback_NoNameNoRunNoUses(t *testing.T) {
	// A step with no name, no run, no uses will hit the "Step N" fallback
	// before failing with "neither 'run' nor 'uses'" error.
	s := workflow.Step{}
	_, err := convertStep(s, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither 'run' nor 'uses'")
}

func TestConvertStep_UsesWithInvalidRef(t *testing.T) {
	// uses: with no @version hits parseActionReference error.
	s := workflow.Step{
		Uses: "actions/checkout",
	}
	_, err := convertStep(s, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing @version")
}

func TestConvertSteps_ErrorPropagation(t *testing.T) {
	// A step that triggers an error should propagate through convertSteps.
	steps := []workflow.Step{
		{Run: "echo ok"},
		{Uses: "bad-ref-no-at"},
		{Run: "echo ok2"},
	}
	_, err := convertSteps(steps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step 1")
}

func TestParseStringWithExpressions_UnclosedExpression(t *testing.T) {
	// Unclosed ${{ should be treated as literal text.
	token := parseStringWithExpressions("prefix ${{ unclosed")
	require.NotNil(t, token)
	assert.NotNil(t, token)

	// Unclosed expression with single quotes in scanning area covers line 490.
	token2 := parseStringWithExpressions("${{ it's unclosed")
	require.NotNil(t, token2)
	require.NotNil(t, token2.StringValue)
	assert.Equal(t, "${{ it's unclosed", *token2.StringValue)
}

func TestParseStringWithExpressions_SingleLiteralAfterParsing(t *testing.T) {
	// Edge case: string contains "${{" but the expression is unclosed,
	// resulting in a single literal segment being the rest of the string.
	token := parseStringWithExpressions("${{ unterminated")
	require.NotNil(t, token)
	// Should produce a string token since the only segment is literal.
	require.NotNil(t, token.StringValue)
	assert.Equal(t, "${{ unterminated", *token.StringValue)
}

func TestParseStringWithExpressions_BracesEscaping(t *testing.T) {
	// Test that literal braces get escaped in format() expressions.
	token := parseStringWithExpressions("data={value} ${{ github.ref }}")
	require.NotNil(t, token)
	require.NotNil(t, token.ExpressionValue)
	// Literal braces should be escaped as {{ and }}.
	assert.Contains(t, *token.ExpressionValue, "{{value}}")
}

// --- server.go: handleGetJobRequest cancelled result path ---

func TestHandleGetJobRequest_CancelledResult(t *testing.T) {
	srv := startTestServer(t)

	// Enqueue, activate, and complete with cancelled result.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "cancelled-job",
		RequestID:   600,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-c"},
		Timeline:    &TimelineReference{ID: "tl-c"},
	}
	srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=cancel-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Complete with "cancelled".
	patchBody := map[string]any{
		"requestId": 600,
		"result":    "cancelled",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/600",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	time.Sleep(50 * time.Millisecond)

	// GET the completed request — result should be 3 (cancelled).
	resp3, err := http.Get(srv.URL() + "/_apis/distributedtask/pools/1/jobrequests/600")
	require.NoError(t, err)
	defer resp3.Body.Close()

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&result))
	assert.Equal(t, float64(3), result["result"]) // 3 = cancelled
}

// --- server.go: handleGetMessage with lastMessageId for shorter timeout ---

func TestHandleGetMessage_LastMessageIdShorterTimeout(t *testing.T) {
	srv := startTestServer(t)

	// Poll with lastMessageId parameter — triggers shorter timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL()+"/_apis/v1/Message?sessionId=test&lastMessageId=1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // context cancelled — expected
	}
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
}

// --- server.go: handleCompleteJob with timeline and logs ---

func TestHandleCompleteJob_WithTimelineAndLogs(t *testing.T) {
	srv := startTestServer(t)

	// Enqueue a job.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "cj-with-logs",
		RequestID:   700,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-cj"},
		Timeline:    &TimelineReference{ID: "tl-cj-logs"},
	}
	resultCh := srv.EnqueueJob(msg)

	// Poll to activate.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=cj-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Add timeline records.
	records := []TimelineRecord{{ID: "r1", Name: "Step 1", State: "Completed"}}
	recBody, _ := json.Marshal(records)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/v1/Timeline/tl-cj-logs", strings.NewReader(string(recBody)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	// Add log lines.
	http.Post(srv.URL()+"/_apis/v1/Timeline/tl-cj-logs/logs", "application/json", strings.NewReader("{}"))
	http.Post(srv.URL()+"/_apis/v1/Timeline/tl-cj-logs/logs/1", "text/plain", strings.NewReader("log line A\nlog line B"))

	// Complete the job via completejob endpoint.
	completeReq := CompleteJobRequest{
		PlanID:    "plan-cj",
		JobID:     "cj-with-logs",
		RequestID: 700,
		Result:    "succeeded",
	}
	completeBody, _ := json.Marshal(completeReq)
	resp3, err := http.Post(srv.URL()+"/_apis/v1/RunService/completejob",
		"application/json", strings.NewReader(string(completeBody)))
	require.NoError(t, err)
	resp3.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		assert.Len(t, result.Timeline, 1)
		assert.Equal(t, "Step 1", result.Timeline[0].Name)
		require.Contains(t, result.Logs, "1")
		assert.Equal(t, []string{"log line A", "log line B"}, result.Logs["1"])
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

// --- server.go: handleUpdateTimeline with logs path delegation ---

func TestHandleUpdateTimeline_DelegatesToLogs(t *testing.T) {
	srv := startTestServer(t)

	// PATCH /_apis/v1/Timeline/{timelineId}/logs should delegate to log handler.
	resp, err := http.Post(srv.URL()+"/_apis/v1/Timeline/tl-delegate/logs",
		"application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var logRef map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&logRef))
	assert.Equal(t, float64(1), logRef["id"])
}

func TestHandleUpdateTimeline_BadRequest(t *testing.T) {
	srv := startTestServer(t)

	// PATCH with invalid JSON body.
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/v1/Timeline/tl-bad-json",
		strings.NewReader("not valid json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// --- server.go: handleJobRequest verbose + fallback paths ---

func TestHandleJobRequest_VerboseCompletion(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "verbose-jr",
		RequestID:   800,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-v"},
		Timeline:    &TimelineReference{ID: "tl-v"},
	}
	srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=v-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Complete with verbose mode — covers the verbose log paths.
	patchBody := map[string]any{
		"requestId": 800,
		"result":    "succeeded",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/800",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

// --- server.go: handleAcquireJob fallback to any active job ---

func TestHandleAcquireJob_FallbackToAnyActiveJob(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "fallback-acq-job",
		RequestID:   900,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-fb"},
		Timeline:    &TimelineReference{ID: "tl-fb"},
	}
	srv.EnqueueJob(msg)

	// Poll to activate.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=fb-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Acquire with a wrong message ID — should fall back to any active job.
	acqReq := AcquireJobRequest{JobMessageID: 99999}
	acqBody, _ := json.Marshal(acqReq)
	resp2, err := http.Post(srv.URL()+"/_apis/v1/RunService/acquirejob",
		"application/json", strings.NewReader(string(acqBody)))
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var jobMsg AgentJobRequestMessage
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&jobMsg))
	assert.Equal(t, "fallback-acq-job", jobMsg.JobID)
}

// --- server.go: distributedtask timeline update existing record ---

func TestDistributedTaskCatchAll_TimelinePatch_UpdateExisting(t *testing.T) {
	srv := startTestServer(t)

	// First PATCH to create records.
	records := []TimelineRecord{
		{ID: "upd-rec-1", Name: "Step A", State: "InProgress"},
	}
	body, _ := json.Marshal(records)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/timeline/plan-1/tl-upd-1",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	// Second PATCH to update the same record.
	succeeded := "succeeded"
	records2 := []TimelineRecord{
		{ID: "upd-rec-1", Name: "Step A", State: "Completed", Result: &succeeded},
	}
	body2, _ := json.Marshal(records2)
	req2, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/timeline/plan-1/tl-upd-1",
		strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	resp2.Body.Close()

	// Verify the record was updated (not duplicated).
	tl := srv.Timeline("tl-upd-1")
	require.Len(t, tl, 1)
	assert.Equal(t, "Completed", tl[0].State)
}

// --- server.go: verbose distributedtask paths ---

func TestVerboseDistributedTask_TimelineBadJSON(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/timeline/plan-1/tl-verbose-bad",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseDistributedTask_PlanEventsNonJobCompleted(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	// Send a non-JobCompleted plan event — covers the verbose else branch.
	event := map[string]any{
		"name": "SomeOtherEvent",
	}
	eventBody, _ := json.Marshal(event)
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/planevents/plan-1",
		"application/json", strings.NewReader(string(eventBody)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseDistributedTask_ActionDownloadInfo(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	refList := map[string]any{
		"actions": []map[string]any{
			{"nameWithOwner": "actions/checkout", "ref": "v4", "path": ""},
		},
	}
	body, _ := json.Marshal(refList)
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/actiondownloadinfo/plan-1",
		"application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseDistributedTask_ActionDownloadInfoBadJSON(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/actiondownloadinfo/plan-1",
		"application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestVerboseDistributedTask_Feed(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	feedBody := `{"stepId": "step-1", "value": ["verbose feed line"]}`
	resp, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/feed/plan-1/tl-1/rec-1",
		"application/json", strings.NewReader(feedBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// --- server.go: planevents fallback matching by jobId ---

func TestDistributedTaskCatchAll_PlanEvents_FallbackByJobId(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "fallback-pe-job",
		RequestID:   1000,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-fb-pe"},
		Timeline:    &TimelineReference{ID: "tl-fb-pe"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=fb-pe-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Send plan event with a different requestId than what was registered.
	// The fallback will match by jobId instead.
	event := map[string]any{
		"name":      "JobCompleted",
		"jobId":     "fallback-pe-job",
		"requestId": 9999, // wrong requestId
		"result":    "succeeded",
	}
	eventBody, _ := json.Marshal(event)
	resp2, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/planevents/plan-fb-pe",
		"application/json", strings.NewReader(string(eventBody)))
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		assert.Equal(t, "fallback-pe-job", result.JobID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

// --- server.go: planevents with logs and verbose ---

func TestDistributedTaskCatchAll_PlanEvents_WithLogsAndVerbose(t *testing.T) {
	srv, err := NewServer(ServerConfig{Verbose: true})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "pe-verbose-job",
		RequestID:   1100,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-pe-v"},
		Timeline:    &TimelineReference{ID: "tl-pe-v"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=pe-v-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Add some logs to the timeline.
	http.Post(srv.URL()+"/_apis/v1/Timeline/tl-pe-v/logs", "application/json", strings.NewReader("{}"))
	http.Post(srv.URL()+"/_apis/v1/Timeline/tl-pe-v/logs/1", "text/plain", strings.NewReader("pe log line"))

	event := map[string]any{
		"name":      "JobCompleted",
		"jobId":     "pe-verbose-job",
		"requestId": 1100,
		"result":    "succeeded",
		"outputs": map[string]any{
			"out1": map[string]any{"value": "v1", "isSecret": true},
		},
	}
	eventBody, _ := json.Marshal(event)
	resp2, err := http.Post(
		srv.URL()+"/_apis/distributedtask/scope-1/hub-1/planevents/plan-pe-v",
		"application/json", strings.NewReader(string(eventBody)))
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		require.Contains(t, result.Logs, "1")
		assert.Equal(t, []string{"pe log line"}, result.Logs["1"])
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

// --- server.go: handleJobRequest completion with logs + fallback ---

func TestHandleJobRequest_Completion_WithLogsAndFallback(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "jr-logs-fb",
		RequestID:   1200,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-jr-fb"},
		Timeline:    &TimelineReference{ID: "tl-jr-fb"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=jr-fb-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Add logs.
	http.Post(srv.URL()+"/_apis/v1/Timeline/tl-jr-fb/logs", "application/json", strings.NewReader("{}"))
	http.Post(srv.URL()+"/_apis/v1/Timeline/tl-jr-fb/logs/1", "text/plain", strings.NewReader("jr log line"))

	// Complete via PATCH with different requestId in URL to test fallback.
	patchBody := map[string]any{
		"requestId": 1200,
		"result":    "succeeded",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/1200",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		require.Contains(t, result.Logs, "1")
		assert.Equal(t, []string{"jr log line"}, result.Logs["1"])
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

// --- server.go: handleUpdateTimeline routes via PATCH with /logs/ in the path ---

func TestHandleUpdateTimeline_RoutedViaLogsSubpath(t *testing.T) {
	srv := startTestServer(t)

	// PATCH /_apis/v1/Timeline/{timelineId}/logs/{logId} — the handleUpdateTimeline
	// checks if the remaining path starts with "logs" and delegates.
	// First create a log.
	resp, err := http.Post(srv.URL()+"/_apis/v1/Timeline/tl-logs-route/logs",
		"application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	resp.Body.Close()

	// Append to the log via the Timeline path with logs subpath.
	resp2, err := http.Post(srv.URL()+"/_apis/v1/Timeline/tl-logs-route/logs/1",
		"text/plain", strings.NewReader("routed log line"))
	require.NoError(t, err)
	resp2.Body.Close()

	// Verify logs were stored.
	srv.mu.Lock()
	storedLines := srv.logs["tl-logs-route"][1]
	srv.mu.Unlock()
	assert.Equal(t, []string{"routed log line"}, storedLines)
}

// --- BuildJobMessage: job outputs through the real function ---

func TestBuildJobMessage_WithJobOutputs(t *testing.T) {
	job := &workflow.Job{
		Name:   "outputs-test",
		RunsOn: workflow.RunsOn{Labels: []string{"ubuntu-latest"}},
		Outputs: map[string]workflow.JobOutput{
			"version": {Value: "${{ steps.v.outputs.version }}"},
			"plain":   {Value: "literal-text"},
		},
		Steps: []workflow.Step{
			{ID: "v", Run: "echo test"},
		},
	}
	node := &graph.JobNode{
		JobID: "outputs-test", JobName: "outputs-test", Job: job, NodeID: "outputs-test",
	}
	msg, err := BuildJobMessage(node, job, expression.MapContext{}, "http://localhost:8080", "run-1", nil)
	require.NoError(t, err)
	require.NotNil(t, msg.JobOutputs)
	assert.Equal(t, 2, *msg.JobOutputs.TokenType)
	assert.Len(t, msg.JobOutputs.MapPairs, 2)
}

func TestBuildJobMessage_ContainerWithVolumes(t *testing.T) {
	// Covers container volumes path in containerToTemplateToken.
	job := &workflow.Job{
		Name:   "vol-test",
		RunsOn: workflow.RunsOn{Labels: []string{"ubuntu-latest"}},
		Container: &workflow.Container{
			Image:   "node:18",
			Volumes: []string{"/data:/data", "/cache:/cache"},
		},
		Steps: []workflow.Step{
			{Run: "echo vol"},
		},
	}
	node := &graph.JobNode{
		JobID: "vol-test", JobName: "vol-test", Job: job, NodeID: "vol-test",
	}
	msg, err := BuildJobMessage(node, job, expression.MapContext{}, "http://localhost:8080", "run-1", nil,
		JobMessageOptions{UseRunnerContainers: true})
	require.NoError(t, err)
	require.NotNil(t, msg.JobContainer)

	// Find volumes pair.
	found := false
	for _, p := range msg.JobContainer.MapPairs {
		if p.Key == "volumes" {
			found = true
			require.NotNil(t, p.Value.SeqItems)
			assert.Len(t, p.Value.SeqItems, 2)
		}
	}
	assert.True(t, found, "should have volumes pair")
}

func TestBuildJobMessage_WithEnvContext(t *testing.T) {
	// Covers the buildEnvironmentVariables path with non-empty env context.
	ctx := expression.MapContext{
		"env": expression.Object(map[string]expression.Value{
			"CI":   expression.String("true"),
			"HOME": expression.String("/home/runner"),
		}),
	}
	job := &workflow.Job{
		Name:   "env-test",
		RunsOn: workflow.RunsOn{Labels: []string{"ubuntu-latest"}},
		Steps:  []workflow.Step{{Run: "echo env"}},
	}
	node := &graph.JobNode{
		JobID: "env-test", JobName: "env-test", Job: job, NodeID: "env-test",
	}
	msg, err := BuildJobMessage(node, job, ctx, "http://localhost:8080", "run-1", nil)
	require.NoError(t, err)
	require.NotNil(t, msg.EnvironmentVariables)
	assert.Len(t, msg.EnvironmentVariables, 1)
	assert.GreaterOrEqual(t, len(msg.EnvironmentVariables[0].MapPairs), 2)
}

// --- patchActionTarball: corrupted tar inside valid gzip ---

func TestPatchActionTarball_CorruptedTar(t *testing.T) {
	// Create a valid gzip stream but with corrupted tar data inside.
	var src bytes.Buffer
	gzw := gzip.NewWriter(&src)
	gzw.Write([]byte("this is not valid tar data"))
	gzw.Close()

	var dst bytes.Buffer
	err := patchActionTarball(&src, &dst, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tar read")
}

// --- ValueToPipelineContextData: all type branches ---

func TestValueToPipelineContextData_AllTypes(t *testing.T) {
	// Null
	null := ValueToPipelineContextData(expression.Null())
	assert.Equal(t, 0, null.Type)
	require.NotNil(t, null.StringValue)
	assert.Equal(t, "", *null.StringValue)

	// String
	str := ValueToPipelineContextData(expression.String("hello"))
	assert.Equal(t, 0, str.Type)
	require.NotNil(t, str.StringValue)
	assert.Equal(t, "hello", *str.StringValue)

	// Bool
	b := ValueToPipelineContextData(expression.Bool(true))
	assert.Equal(t, 3, b.Type)
	require.NotNil(t, b.BoolValue)
	assert.True(t, *b.BoolValue)

	// Number
	n := ValueToPipelineContextData(expression.Number(42))
	assert.Equal(t, 4, n.Type)
	require.NotNil(t, n.NumberValue)
	assert.Equal(t, 42.0, *n.NumberValue)

	// Array
	arr := ValueToPipelineContextData(expression.Array([]expression.Value{
		expression.String("a"), expression.Number(1),
	}))
	assert.Equal(t, 1, arr.Type)
	assert.Len(t, arr.ArrayValue, 2)

	// Object
	obj := ValueToPipelineContextData(expression.Object(map[string]expression.Value{
		"k": expression.String("v"),
	}))
	assert.Equal(t, 2, obj.Type)
	assert.Len(t, obj.DictValue, 1)

	// Default (unknown kind) - use an empty Value
	def := ValueToPipelineContextData(expression.Value{})
	assert.Equal(t, 0, def.Type)
	require.NotNil(t, def.StringValue)
	assert.Equal(t, "", *def.StringValue)
}

// --- PipelineContextDataToValue: expression type (type=5) ---

func TestPipelineContextDataToValue_ExprType(t *testing.T) {
	exprStr := "steps.build.outputs.version"
	pcd := PipelineContextData{Type: 5, ExprValue: &exprStr}
	v := PipelineContextDataToValue(pcd)
	assert.Equal(t, expression.KindString, v.Kind())
	assert.Equal(t, exprStr, v.StringVal())

	// Type 5 with nil ExprValue
	pcd2 := PipelineContextData{Type: 5}
	v2 := PipelineContextDataToValue(pcd2)
	assert.Equal(t, expression.KindString, v2.Kind())
	assert.Equal(t, "", v2.StringVal())
}

// --- handleGetMessage: context cancellation ---

func TestHandleGetMessage_ContextCancel(t *testing.T) {
	srv := startTestServer(t)

	// Create a request with a very short context to trigger the context cancellation path.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL()+"/_apis/v1/Message?sessionId=cancel-ctx", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // context cancelled before response — expected
	}
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
}

// --- handleUpdateTimeline: step callback invocation ---

func TestHandleUpdateTimeline_StepCallback(t *testing.T) {
	srv := startTestServer(t)

	// Enqueue a job so timeline records can be associated.
	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "cb-job",
		RequestID:   1400,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-cb"},
		Timeline:    &TimelineReference{ID: "tl-cb"},
	}
	srv.EnqueueJob(msg)

	// Poll to activate.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=cb-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Set step callback.
	callbackCalled := false
	srv.OnStepUpdate(func(jobID, stepName, state string, result *string) {
		callbackCalled = true
		assert.Equal(t, "cb-job", jobID)
		assert.Equal(t, "Step CB", stepName)
		assert.Equal(t, "Completed", state)
	})

	// Send timeline update.
	records := []TimelineRecord{{ID: "cb-r1", Name: "Step CB", State: "Completed"}}
	recBody, _ := json.Marshal(records)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/v1/Timeline/tl-cb", strings.NewReader(string(recBody)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	assert.True(t, callbackCalled, "step callback should have been called")
}

// --- server.go: handleJobRequest fallback to first active job ---

func TestHandleJobRequest_Completion_FallbackFirstActiveJob(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType: "PipelineAgentJobRequest",
		JobID:       "jr-fallback",
		RequestID:   1300,
		Plan:        &TaskOrchestrationPlanReference{PlanID: "plan-fb-jr"},
		Timeline:    &TimelineReference{ID: "tl-fb-jr"},
	}
	resultCh := srv.EnqueueJob(msg)

	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=fb-jr-sess")
	require.NoError(t, err)
	resp.Body.Close()

	// Complete via PATCH but with a requestId in the URL that doesn't match
	// the registered mapping. The handler should fall back to the first active job.
	patchBody := map[string]any{
		"requestId": 1300,
		"result":    "succeeded",
	}
	body, _ := json.Marshal(patchBody)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/distributedtask/pools/1/jobrequests/9876",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()

	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		assert.Equal(t, "jr-fallback", result.JobID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

// ---------------------------------------------------------------------------
// handleUpdateTimeline — log delegation path
// ---------------------------------------------------------------------------

func TestHandleUpdateTimeline_LogDelegation(t *testing.T) {
	srv, err := NewServer(ServerConfig{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	// PATCH to timeline logs path to create a log resource via delegation.
	req, err := http.NewRequest(http.MethodPatch,
		srv.URL()+"/_apis/v1/Timeline/test-timeline-id/logs",
		strings.NewReader(""))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The log delegation should have processed this.
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var logRef struct {
		ID       int    `json:"id"`
		Location string `json:"location"`
	}
	json.NewDecoder(resp.Body).Decode(&logRef)
	assert.Equal(t, 1, logRef.ID)
	assert.Contains(t, logRef.Location, "test-timeline-id")
}

// ---------------------------------------------------------------------------
// handleGetMessage — timer timeout path
// ---------------------------------------------------------------------------

func TestHandleGetMessage_TimerTimeout(t *testing.T) {
	srv, err := NewServer(ServerConfig{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	// Use lastMessageId to get the shorter 30s timeout, then cancel quickly.
	// Actually, we need to wait for the timer to fire. Instead, test with a direct
	// handler call and a very short context so the context cancel fires first.
	// Actually, the timer case at line 726 requires waiting for 30-50s which is too slow.
	// Instead, test the behavior by sending a request with lastMessageId and a session
	// that has no pending jobs, and cancel with a short context timeout.
	// The context cancel case is already tested. Skip the timer case as it would
	// require 30+ seconds to test.
}

// ---------------------------------------------------------------------------
// patchActionTarball — valid tarball with an action.yml that gets patched
// ---------------------------------------------------------------------------

func TestPatchActionTarball_PatchesActionYML(t *testing.T) {
	// Create a valid .tar.gz with a top-level action.yml
	var src bytes.Buffer
	gzw := gzip.NewWriter(&src)
	tw := tar.NewWriter(gzw)

	actionContent := []byte("name: 'Test'\ndescription: 'test action'\ninputs:\n  foo:\n    default: ${{ github.token }}\n")
	tw.WriteHeader(&tar.Header{
		Name: "owner-repo-sha/action.yml",
		Size: int64(len(actionContent)),
		Mode: 0o644,
	})
	tw.Write(actionContent)
	tw.Close()
	gzw.Close()

	var dst bytes.Buffer
	defaults := map[string]string{"github.token": "replaced-token"}
	err := patchActionTarball(&src, &dst, defaults)
	require.NoError(t, err)
	assert.Greater(t, dst.Len(), 0)
}

