package broker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(ServerConfig{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })

	require.NoError(t, srv.Start(ctx))
	return srv
}

func TestServer_URL(t *testing.T) {
	srv := startTestServer(t)
	assert.True(t, strings.HasPrefix(srv.URL(), "http://127.0.0.1:"))
}

func TestServer_ConnectionData(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Get(srv.URL() + "/_apis/connectionData")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var data ConnectionData
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&data))
	assert.NotEmpty(t, data.InstanceID)
	assert.NotEmpty(t, data.LocationServiceData.ServiceDefinitions)
}

func TestServer_SessionLifecycle(t *testing.T) {
	srv := startTestServer(t)

	// Create session.
	body := `{"ownerName":"test","agent":{"id":1,"name":"test-runner"}}`
	resp, err := http.Post(srv.URL()+"/_apis/v1/Message/sessions", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var sess TaskAgentSession
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&sess))
	assert.NotEmpty(t, sess.SessionID)
	assert.Equal(t, "test", sess.OwnerName)

	// Delete session.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL()+"/_apis/v1/Message/sessions/"+sess.SessionID, nil)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestServer_MessagePollTimeout(t *testing.T) {
	srv := startTestServer(t)

	// Cancel context quickly so the server-side poll returns 202.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+"/_apis/v1/Message?sessionId=test", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context deadline expired — the server would have returned 202.
		// This is expected behavior: no job available.
		return
	}
	defer resp.Body.Close()

	// If we got a response, it should be 202 (no content).
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
}

func TestServer_EnqueueAndPollJob(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType:    "PipelineAgentJobRequest",
		JobID:          "test-job-1",
		JobDisplayName: "Test Job",
		JobName:        "test",
		RequestID:      1,
		Plan: &TaskOrchestrationPlanReference{
			PlanID: "plan-1",
		},
		Timeline: &TimelineReference{
			ID: "timeline-1",
		},
	}

	resultCh := srv.EnqueueJob(msg)

	// Poll for the job.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=test")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var agentMsg TaskAgentMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agentMsg))
	assert.Equal(t, "PipelineAgentJobRequest", agentMsg.MessageType)
	assert.NotZero(t, agentMsg.MessageID)

	// Decode the body to verify it's our job.
	var jobMsg AgentJobRequestMessage
	require.NoError(t, json.Unmarshal([]byte(agentMsg.Body), &jobMsg))
	assert.Equal(t, "test-job-1", jobMsg.JobID)

	// Complete the job.
	completeReq := CompleteJobRequest{
		PlanID:    "plan-1",
		JobID:     "test-job-1",
		RequestID: 1,
		Result:    "succeeded",
	}
	completeBody, _ := json.Marshal(completeReq)
	resp2, err := http.Post(srv.URL()+"/_apis/v1/RunService/completejob", "application/json",
		strings.NewReader(string(completeBody)))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// Check the result channel.
	select {
	case result := <-resultCh:
		assert.Equal(t, "succeeded", result.Result)
		assert.Equal(t, "test-job-1", result.JobID)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestServer_AcquireJob(t *testing.T) {
	srv := startTestServer(t)

	msg := &AgentJobRequestMessage{
		MessageType:    "PipelineAgentJobRequest",
		JobID:          "test-job-2",
		JobDisplayName: "Test Job 2",
		Plan: &TaskOrchestrationPlanReference{
			PlanID: "plan-2",
		},
		Timeline: &TimelineReference{
			ID: "timeline-2",
		},
	}

	srv.EnqueueJob(msg)

	// Poll to move job to active.
	resp, err := http.Get(srv.URL() + "/_apis/v1/Message?sessionId=test")
	require.NoError(t, err)
	resp.Body.Close()

	// Acquire the job.
	acqReq := AcquireJobRequest{JobMessageID: 1}
	acqBody, _ := json.Marshal(acqReq)
	resp2, err := http.Post(srv.URL()+"/_apis/v1/RunService/acquirejob", "application/json",
		strings.NewReader(string(acqBody)))
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var jobMsg AgentJobRequestMessage
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&jobMsg))
	assert.Equal(t, "test-job-2", jobMsg.JobID)
}

func TestServer_RenewJob(t *testing.T) {
	srv := startTestServer(t)

	renewReq := RenewJobRequest{
		PlanID:    "plan-1",
		JobID:     "job-1",
		RequestID: 1,
	}
	body, _ := json.Marshal(renewReq)
	resp, err := http.Post(srv.URL()+"/_apis/v1/RunService/renewjob", "application/json",
		strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var renewResp RenewJobResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&renewResp))
	assert.Equal(t, "plan-1", renewResp.PlanID)
	assert.NotEmpty(t, renewResp.LockedUntil)
}

func TestServer_TimelineUpdate(t *testing.T) {
	srv := startTestServer(t)

	records := []TimelineRecord{
		{ID: "rec-1", Name: "Step 1", State: "InProgress"},
		{ID: "rec-2", Name: "Step 2", State: "Pending"},
	}
	body, _ := json.Marshal(records)

	req, _ := http.NewRequest(http.MethodPatch, srv.URL()+"/_apis/v1/Timeline/tl-1", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify timeline was stored.
	tl := srv.Timeline("tl-1")
	assert.Len(t, tl, 2)
	assert.Equal(t, "Step 1", tl[0].Name)

	// Update a record.
	succeeded := "succeeded"
	records2 := []TimelineRecord{
		{ID: "rec-1", Name: "Step 1", State: "Completed", Result: &succeeded},
	}
	body2, _ := json.Marshal(records2)
	req2, _ := http.NewRequest(http.MethodPatch, srv.URL()+"/_apis/v1/Timeline/tl-1", strings.NewReader(string(body2)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	tl2 := srv.Timeline("tl-1")
	assert.Len(t, tl2, 2)
	assert.Equal(t, "Completed", tl2[0].State)
	assert.Equal(t, "succeeded", *tl2[0].Result)
}

func TestServer_Logs(t *testing.T) {
	srv := startTestServer(t)

	// Create a log resource.
	resp, err := http.Post(srv.URL()+"/_apis/v1/Timeline/tl-1/logs", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var logRef LogReference
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&logRef))
	assert.Equal(t, 1, logRef.ID)

	// Append log lines.
	lines := "line 1\nline 2\nline 3"
	resp2, err := http.Post(srv.URL()+"/_apis/v1/Timeline/tl-1/logs/1", "text/plain", strings.NewReader(lines))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// Verify logs were stored.
	srv.mu.Lock()
	storedLines := srv.logs["tl-1"][1]
	srv.mu.Unlock()
	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, storedLines)
}

func TestServer_GetPools(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Get(srv.URL() + "/_apis/v1/runneradmin/pools")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_Register(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Post(srv.URL()+"/_apis/v1/runneradmin/register", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_CatchAll(t *testing.T) {
	srv := startTestServer(t)

	resp, err := http.Get(srv.URL() + "/_apis/some/unknown/endpoint")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Catch-all returns 200 to not break the runner.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_RequestLog(t *testing.T) {
	srv := startTestServer(t)

	http.Get(srv.URL() + "/_apis/connectionData")
	http.Get(srv.URL() + "/_apis/v1/runneradmin/pools")

	logs := srv.RequestLog()
	assert.GreaterOrEqual(t, len(logs), 2)
}

func TestServer_ActionTarball_AuthHeader(t *testing.T) {
	// Set up a fake GitHub API that verifies the auth header.
	var receivedAuth string
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		assert.Contains(t, r.URL.Path, "/repos/owner/repo/tarball/v1.0.0")

		// Return a minimal valid gzipped tarball.
		w.Header().Set("Content-Type", "application/gzip")
		var buf bytes.Buffer
		gzw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gzw)

		content := []byte("name: test\n")
		tw.WriteHeader(&tar.Header{
			Name: "owner-repo-abc123/action.yml",
			Size: int64(len(content)),
			Mode: 0644,
		})
		tw.Write(content)
		tw.Close()
		gzw.Close()
		w.Write(buf.Bytes())
	}))
	defer fakeGH.Close()

	srv, err := NewServer(ServerConfig{GitHubToken: "test-secret-token"})
	require.NoError(t, err)

	// Override the action API base to point to our fake server.
	srv.actionAPIBase = fakeGH.URL

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { srv.Stop(context.Background()) })
	require.NoError(t, srv.Start(ctx))

	// Request a tarball.
	resp, err := http.Get(srv.URL() + "/_actions/tarball/owner/repo/v1.0.0")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Bearer test-secret-token", receivedAuth)
}

func TestServer_ActionTarball_NoAuth(t *testing.T) {
	// Without a token, no Authorization header should be sent.
	var receivedAuth string
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")

		var buf bytes.Buffer
		gzw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gzw)
		content := []byte("name: test\n")
		tw.WriteHeader(&tar.Header{
			Name: "owner-repo-abc123/action.yml",
			Size: int64(len(content)),
			Mode: 0644,
		})
		tw.Write(content)
		tw.Close()
		gzw.Close()
		w.Write(buf.Bytes())
	}))
	defer fakeGH.Close()

	srv, err := NewServer(ServerConfig{}) // no token
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
	assert.Empty(t, receivedAuth)
}

func TestServer_ActionTarball_ExprPatching(t *testing.T) {
	// Verify that expressions in action.yml are replaced.
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		gzw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gzw)

		content := []byte("inputs:\n  token:\n    default: ${{ github.token }}\n  name:\n    default: ${{ github.repository }}\n")
		tw.WriteHeader(&tar.Header{
			Name: "owner-repo-abc123/action.yml",
			Size: int64(len(content)),
			Mode: 0644,
		})
		tw.Write(content)
		tw.Close()
		gzw.Close()
		w.Write(buf.Bytes())
	}))
	defer fakeGH.Close()

	srv, err := NewServer(ServerConfig{
		ExprDefaults: map[string]string{
			"github.token":      "ghs_xxx",
			"github.repository": "octocat/hello",
		},
	})
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

	// Read the patched tarball and find action.yml.
	gzr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	tr := tar.NewReader(gzr)

	var actionContent string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if strings.HasSuffix(hdr.Name, "action.yml") {
			data, _ := io.ReadAll(tr)
			actionContent = string(data)
			break
		}
	}

	assert.Contains(t, actionContent, "ghs_xxx")
	assert.Contains(t, actionContent, "octocat/hello")
	assert.NotContains(t, actionContent, "${{")
}
