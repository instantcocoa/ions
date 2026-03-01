package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// RequestLogEntry records an HTTP request for debugging.
type RequestLogEntry struct {
	Time       time.Time
	Method     string
	Path       string
	StatusCode int
	Body       string
}

// jobEnvelope wraps a job message with its completion channel.
type jobEnvelope struct {
	msg    *AgentJobRequestMessage
	result chan *JobCompletionResult
}

// session tracks a connected runner session.
type session struct {
	id        string
	agent     TaskAgent
	createdAt time.Time
	hasJob    bool // true once a job message has been delivered to this session
}

// Server is the broker HTTP server that the runner talks to.
type Server struct {
	listener   net.Listener
	httpServer *http.Server
	mux        *http.ServeMux
	baseURL    string
	instanceID string // stable across all connectionData calls
	verbose    bool

	// Session management.
	sessions map[string]*session

	// Job queue: orchestrator pushes, runner polls.
	pendingJobs chan *jobEnvelope

	// Active jobs being executed, indexed by jobId.
	activeJobs map[string]*jobEnvelope

	// requestId → jobId mapping for matching completion PATCHes.
	requestToJob map[int64]string

	// messageId → jobId mapping for matching acquireJob calls.
	messageToJob map[int64]string

	// Completed request results — the runner calls GET jobrequests/{requestId}
	// for old requests during EnsureDispatchFinished; we need to return the result
	// so it recognizes the old job as finished and proceeds with the new one.
	completedRequests map[int64]string // requestId → result ("succeeded", "failed", etc.)

	// Timeline data from runner step updates.
	timelines map[string][]TimelineRecord

	// Log data keyed by timelineId then logId.
	logs      map[string]map[int][]string
	logNextID map[string]int

	// Request log for debugging.
	requestLog []RequestLogEntry

	// Expression defaults for action.yml patching.
	exprDefaults map[string]string

	// Message ID counter.
	messageIDCounter atomic.Int64

	mu sync.Mutex
}

// ServerConfig configures the broker server.
type ServerConfig struct {
	Verbose bool
	// ExprDefaults maps expression names (e.g. "github.token") to literal values
	// for replacing ${{ expr }} in action.yml defaults. The runner's legacy
	// ActionManifestManager can't handle BasicExpressionToken in defaults, so
	// the action tarball proxy replaces them with these resolved values.
	ExprDefaults map[string]string
}

// RouteRegistrar can register HTTP routes on the broker's mux.
type RouteRegistrar interface {
	RegisterRoutes(mux *http.ServeMux)
}

// NewServer creates a new broker server listening on a random localhost port.
func NewServer(cfg ServerConfig) (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot listen: %w", err)
	}

	s := &Server{
		listener:    listener,
		baseURL:     fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port),
		instanceID:  uuid.New().String(),
		verbose:     cfg.Verbose,
		exprDefaults: cfg.ExprDefaults,
		sessions:     make(map[string]*session),
		pendingJobs:  make(chan *jobEnvelope, 100),
		activeJobs:   make(map[string]*jobEnvelope),
		requestToJob:      make(map[int64]string),
		messageToJob:      make(map[int64]string),
		completedRequests: make(map[int64]string),
		timelines:    make(map[string][]TimelineRecord),
		logs:        make(map[string]map[int][]string),
		logNextID:   make(map[string]int),
	}

	mux := http.NewServeMux()
	s.mux = mux

	// Connection data and resource areas — the runner uses these to discover services.
	mux.HandleFunc("GET /_apis/connectionData", s.handleConnectionData)
	mux.HandleFunc("GET /_apis/ResourceAreas", s.handleResourceAreas)

	// OPTIONS _apis/ — the runner calls this to discover ApiResourceLocation objects
	// which it uses to construct URLs via route template expansion.
	mux.HandleFunc("OPTIONS /_apis/", s.handleOptions)
	mux.HandleFunc("OPTIONS /_apis", s.handleOptions)

	// OAuth token endpoint — runner calls this to get an access token.
	mux.HandleFunc("POST /_apis/oauth2/token", s.handleOAuthToken)

	// Runner registration.
	mux.HandleFunc("POST /_apis/v1/runneradmin/register", s.handleRegister)

	// Pools — the runner discovers pools via the distributed task service.
	mux.HandleFunc("GET /_apis/distributedtask/pools", s.handleGetPools)
	mux.HandleFunc("GET /_apis/v1/runneradmin/pools", s.handleGetPools)

	// Agents.
	mux.HandleFunc("POST /_apis/distributedtask/pools/{poolId}/agents", s.handleAddAgent)
	mux.HandleFunc("POST /_apis/v1/runneradmin/pools/{poolId}/agents", s.handleAddAgent)
	mux.HandleFunc("GET /_apis/distributedtask/pools/{poolId}/agents", s.handleGetAgents)

	// Session management — the runner creates sessions under the pool path.
	mux.HandleFunc("POST /_apis/distributedtask/pools/{poolId}/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /_apis/distributedtask/pools/{poolId}/sessions/{sessionId}", s.handleDeleteSession)
	mux.HandleFunc("POST /_apis/v1/Message/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /_apis/v1/Message/sessions/{sessionId}", s.handleDeleteSession)

	// Message polling — the runner polls for jobs under the pool path.
	mux.HandleFunc("GET /_apis/distributedtask/pools/{poolId}/messages", s.handleGetMessage)
	mux.HandleFunc("DELETE /_apis/distributedtask/pools/{poolId}/messages/{messageId}", s.handleDeleteMessage)
	mux.HandleFunc("GET /_apis/v1/Message", s.handleGetMessage)
	mux.HandleFunc("DELETE /_apis/v1/Message", s.handleDeleteMessage)

	// Job requests — get, renew lock, complete.
	mux.HandleFunc("GET /_apis/distributedtask/pools/{poolId}/jobrequests/{requestId}", s.handleGetJobRequest)
	mux.HandleFunc("PATCH /_apis/distributedtask/pools/{poolId}/jobrequests/{requestId}", s.handleJobRequest)

	// Run service (job lifecycle) — alternate paths.
	mux.HandleFunc("POST /_apis/v1/RunService/acquirejob", s.handleAcquireJob)
	mux.HandleFunc("POST /_apis/v1/RunService/renewjob", s.handleRenewJob)
	mux.HandleFunc("POST /_apis/v1/RunService/completejob", s.handleCompleteJob)

	// Timeline (step reporting) — both old-style and route-template-expanded paths.
	mux.HandleFunc("PATCH /_apis/v1/Timeline/", s.handleUpdateTimeline)
	mux.HandleFunc("POST /_apis/v1/Timeline/", s.handleTimelineLogs)

	// Plan-scoped endpoints — URLs constructed from route template expansion.
	// Timeline records: PATCH _apis/distributedtask/{scopeId}/{hubName}/timeline/{planId}/{timelineId}
	mux.HandleFunc("PATCH /_apis/distributedtask/", s.handleDistributedTaskCatchAll)
	// Logs: POST _apis/distributedtask/{scopeId}/{hubName}/logs/{planId}/{logId}
	// Plan events, timeline record feeds, etc.
	mux.HandleFunc("POST /_apis/distributedtask/", s.handleDistributedTaskCatchAll)
	mux.HandleFunc("PUT /_apis/distributedtask/", s.handleDistributedTaskCatchAll)

	// Action tarball proxy — patches action.yml to strip expression tokens.
	mux.HandleFunc("GET /_actions/tarball/", s.handleActionTarball)

	// Catch-all for unhandled endpoints.
	mux.HandleFunc("/", s.handleCatchAll)

	s.httpServer = &http.Server{
		Handler: s.loggingMiddleware(mux),
	}

	return s, nil
}

// RegisterRoutes adds external route registrars to the broker's mux.
// This must be called before Start. Go's ServeMux uses longest-prefix matching,
// so more specific routes registered here take precedence over the catch-all.
func (s *Server) RegisterRoutes(registrars ...RouteRegistrar) {
	for _, r := range registrars {
		r.RegisterRoutes(s.mux)
	}
}

// Start begins serving requests.
func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.httpServer.Shutdown(context.Background())
	}()

	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			log.Printf("broker server error: %v", err)
		}
	}()

	return nil
}

// Stop shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// URL returns the base URL of the broker server.
func (s *Server) URL() string {
	return s.baseURL
}

// EnqueueJob submits a job for the runner to pick up. Returns a channel
// that will receive the completion result.
func (s *Server) EnqueueJob(msg *AgentJobRequestMessage) <-chan *JobCompletionResult {
	env := &jobEnvelope{
		msg:    msg,
		result: make(chan *JobCompletionResult, 1),
	}
	s.pendingJobs <- env
	return env.result
}

// RequestLog returns a copy of the request log.
func (s *Server) RequestLog() []RequestLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]RequestLogEntry, len(s.requestLog))
	copy(cp, s.requestLog)
	return cp
}

// Timeline returns the timeline records for a given timeline ID.
func (s *Server) Timeline(timelineID string) []TimelineRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]TimelineRecord, len(s.timelines[timelineID]))
	copy(cp, s.timelines[timelineID])
	return cp
}

// loggingMiddleware logs all requests for debugging.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		if s.verbose && r.Body != nil {
			bodyBytes, err := io.ReadAll(r.Body)
			if err == nil {
				body = string(bodyBytes)
				r.Body = io.NopCloser(strings.NewReader(body))
			}
		}

		rw := &responseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rw, r)

		entry := RequestLogEntry{
			Time:       time.Now(),
			Method:     r.Method,
			Path:       r.URL.Path,
			StatusCode: rw.statusCode,
			Body:       body,
		}

		s.mu.Lock()
		s.requestLog = append(s.requestLog, entry)
		s.mu.Unlock()

		if s.verbose {
			log.Printf("[broker] %s %s → %d", r.Method, r.URL.Path, rw.statusCode)
		}
	})
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// --- Endpoint handlers ---

func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": generateJWT("00000000-0000-0000-0000-000000000000"),
		"token_type":   "bearer",
		"expires_in":   3600,
	})
}

func (s *Server) handleConnectionData(w http.ResponseWriter, r *http.Request) {
	// The runner uses connectionData to discover how to reach services.
	// It needs AccessMappings to resolve LocationMappings on each ServiceDefinition,
	// and it needs ServiceDefinitions with the service owner GUIDs it looks for.
	//
	// The runner's VssServerDataProvider.ConnectAsync processes this response:
	// 1. Extracts LocationServiceData.ServiceOwner (falls back to TFSOnPremises if empty)
	// 2. Writes server mapping to disk cache via LocationServerMapCache
	// 3. Creates LocationCacheManager and calls LoadServicesData
	// 4. LoadServicesData processes AccessMappings then calls
	//    DetermineClientAndDefaultZones(DefaultAccessMappingMoniker)
	//
	// IMPORTANT: The runner uses Newtonsoft.Json with CamelCasePropertyNamesContractResolver
	// so all JSON keys must be camelCase (matching our Go json tags).

	instanceID := s.instanceID

	data := ConnectionData{
		InstanceID:   instanceID,
		DeploymentID: instanceID,
		DeploymentType: "hosted",
		WebApplicationRelativeDirectory: "",
		AuthenticatedUser: &IdentityRef{
			DisplayName: "ions-runner",
			ID:          "00000000-0000-0000-0000-000000000001",
			UniqueName:  "ions-runner",
			Descriptor:  ptrDescriptor(NewIdentityDescriptor("System", "00000000-0000-0000-0000-000000000001")),
		},
		AuthorizedUser: &IdentityRef{
			DisplayName: "ions-runner",
			ID:          "00000000-0000-0000-0000-000000000001",
			UniqueName:  "ions-runner",
			Descriptor:  ptrDescriptor(NewIdentityDescriptor("System", "00000000-0000-0000-0000-000000000001")),
		},
		LocationServiceData: LocationServiceData{
			ServiceOwner:                "00000000-0000-0000-0000-000000000000",
			DefaultAccessMappingMoniker: "PublicAccessMapping",
			ClientCacheFresh:            false,
			ClientCacheTimeToLive:       3600,
			LastChangeID:                1,
			LastChangeID64:              1,
			AccessMappings: []AccessMapping{
				{
					DisplayName:    "Public Access Mapping",
					Moniker:        "PublicAccessMapping",
					AccessPoint:    s.baseURL,
					ServiceOwner:   "00000000-0000-0000-0000-000000000000",
					VirtualDirectory: "",
				},
				{
					DisplayName:    "Server Access Mapping",
					Moniker:        "ServerAccessMapping",
					AccessPoint:    s.baseURL,
					ServiceOwner:   "00000000-0000-0000-0000-000000000000",
					VirtualDirectory: "",
				},
				{
					DisplayName:    "Client Access Mapping",
					Moniker:        "ClientAccessMapping",
					AccessPoint:    s.baseURL,
					ServiceOwner:   "00000000-0000-0000-0000-000000000000",
					VirtualDirectory: "",
				},
			},
			ServiceDefinitions: s.serviceDefinitions(),
		},
	}

	if s.verbose {
		jsonBytes, _ := json.MarshalIndent(data, "", "  ")
		log.Printf("[broker] connectionData response:\n%s", string(jsonBytes))
	}

	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleResourceAreas(w http.ResponseWriter, r *http.Request) {
	// The runner also queries /_apis/ResourceAreas to discover service locations.
	// Return the key resource areas it needs.
	areas := []ResourceArea{
		{
			ID:          "a85b8835-c1a1-4aac-ae97-1c3d0ba72dbd",
			Name:        "distributedtask",
			LocationURL: s.baseURL,
		},
		{
			ID:          "5264459e-e5e0-4571-b150-dc22b365e680",
			Name:        "pipelines",
			LocationURL: s.baseURL,
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(areas),
		"value": areas,
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// Accept any registration — we control both sides.
	writeJSON(w, http.StatusOK, map[string]any{
		"url":         s.baseURL,
		"token":       "ions-local-token",
		"tokenSchema": "OAuthAccessToken",
	})
}

func (s *Server) handleGetPools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"count": 1,
		"value": []AgentPool{
			{ID: 1, Name: "Default"},
		},
	})
}

func (s *Server) handleAddAgent(w http.ResponseWriter, r *http.Request) {
	var agent TaskAgent
	if err := json.NewDecoder(r.Body).Decode(&agent); err != nil {
		agent = TaskAgent{ID: 1, Name: "ions-runner"}
	}
	if agent.ID == 0 {
		agent.ID = 1
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleGetAgents(w http.ResponseWriter, r *http.Request) {
	// Return the single agent that we configured.
	writeJSON(w, http.StatusOK, map[string]any{
		"count": 1,
		"value": []TaskAgent{
			{ID: 1, Name: "ions-runner"},
		},
	})
}

func (s *Server) handleGetJobRequest(w http.ResponseWriter, r *http.Request) {
	// GET /_apis/distributedtask/pools/{poolId}/jobrequests/{requestId}
	// The runner calls this in two scenarios:
	// 1. To fetch the full job request after receiving a message
	// 2. In EnsureDispatchFinished, to check if a PREVIOUS job is done before dispatching a new one
	// For (2), the runner expects a TaskAgentJobRequest with result set, NOT an AgentJobRequestMessage.
	requestIDStr := r.PathValue("requestId")
	requestID, _ := strconv.ParseInt(requestIDStr, 10, 64)

	s.mu.Lock()
	// Check active jobs first.
	var env *jobEnvelope
	if jid, ok := s.requestToJob[requestID]; ok {
		env = s.activeJobs[jid]
	}
	// Check completed jobs.
	completedResult, wasCompleted := s.completedRequests[requestID]
	s.mu.Unlock()

	if env != nil {
		// Active job — return the full message as a TaskAgentJobRequest-compatible response.
		writeJSON(w, http.StatusOK, map[string]any{
			"requestId":  env.msg.RequestID,
			"lockedUntil": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"jobId":      env.msg.JobID,
			"jobName":    env.msg.JobName,
		})
		return
	}

	if wasCompleted {
		// Completed job — return with result so the runner knows it's done.
		// TaskResult enum: succeeded=0, succeededWithIssues=1, failed=2, canceled=3
		resultValue := 0 // succeeded
		switch completedResult {
		case "failed":
			resultValue = 2
		case "cancelled":
			resultValue = 3
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"requestId":  requestID,
			"result":     resultValue,
			"finishTime": time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	// Unknown request — return as completed to unblock the runner.
	writeJSON(w, http.StatusOK, map[string]any{
		"requestId":  requestID,
		"result":     0, // succeeded
		"finishTime": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleJobRequest(w http.ResponseWriter, r *http.Request) {
	// PATCH /_apis/distributedtask/pools/{poolId}/jobrequests/{requestId}
	// The runner uses this to renew the lock on a job or update job status.
	// On completion, the body contains "result", "finishTime", and "outputVariables".
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}

	// Check if this is a job completion (has a "result" field).
	// With Plan version >= 8, completion comes via planevents instead,
	// but we keep this path for backward compatibility.
	result := ""
	if r, ok := body["result"].(string); ok && r != "" {
		result = r
	} else if r, ok := body["result"].(float64); ok {
		result = taskResultToString(int(r))
	}
	if s.verbose && result != "" {
		bodyJSON, _ := json.MarshalIndent(body, "", "  ")
		log.Printf("[broker] Job completion PATCH body: %s", string(bodyJSON))
	}
	if result != "" {
		// Look up the active job by requestId from the URL path.
		requestIDStr := r.PathValue("requestId")
		requestID, _ := strconv.ParseInt(requestIDStr, 10, 64)

		s.mu.Lock()
		var env *jobEnvelope
		var jobID string

		// Try to find by requestId mapping first.
		if jid, ok := s.requestToJob[requestID]; ok {
			env = s.activeJobs[jid]
			jobID = jid
		}
		// Fallback: grab first active job (for backward compat with single-job tests).
		if env == nil {
			for id, e := range s.activeJobs {
				env = e
				jobID = id
				break
			}
		}

		if env != nil {
			delete(s.activeJobs, jobID)
			delete(s.requestToJob, env.msg.RequestID)
			s.completedRequests[requestID] = result
		}
		var timeline []TimelineRecord
		var logs map[int][]string
		if env != nil && env.msg.Timeline != nil {
			timeline = s.timelines[env.msg.Timeline.ID]
			logs = s.logs[env.msg.Timeline.ID]
		}
		s.mu.Unlock()

		if env != nil {
			completionResult := &JobCompletionResult{
				JobID:    jobID,
				Result:   result,
				Timeline: timeline,
				Outputs:  make(map[string]VariableValue),
				Logs:     make(map[string][]string),
			}
			for id, lines := range logs {
				completionResult.Logs[strconv.Itoa(id)] = lines
			}
			// Capture outputVariables from the completion body.
			if outputVars, ok := body["outputVariables"].(map[string]any); ok {
				for k, v := range outputVars {
					if vm, ok := v.(map[string]any); ok {
						vv := VariableValue{}
						if val, ok := vm["value"].(string); ok {
							vv.Value = val
						}
						if sec, ok := vm["issecret"].(bool); ok {
							vv.IsSecret = sec
						}
						completionResult.Outputs[k] = vv
					}
				}
			}
			if s.verbose {
				log.Printf("[broker] Job %s completed: result=%s outputs=%v", jobID, result, completionResult.Outputs)
			}
			env.result <- completionResult
		}
	}

	// Respond with updated lock time.
	writeJSON(w, http.StatusOK, map[string]any{
		"requestId":   body["requestId"],
		"lockedUntil": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req TaskAgentSession
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Accept even if we can't parse it.
		req = TaskAgentSession{}
	}

	sessionID := uuid.New().String()
	sess := &session{
		id:        sessionID,
		agent:     req.Agent,
		createdAt: time.Now(),
	}

	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	resp := TaskAgentSession{
		SessionID: sessionID,
		OwnerName: req.OwnerName,
		Agent:     req.Agent,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")

	// If this session already received a job, don't deliver another.
	// Each runner process handles exactly one job.
	s.mu.Lock()
	sess := s.sessions[sessionID]
	alreadyHasJob := sess != nil && sess.hasJob
	s.mu.Unlock()

	if alreadyHasJob {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Long-poll: wait up to 50 seconds for a job.
	timeout := 50 * time.Second
	if t := r.URL.Query().Get("lastMessageId"); t != "" {
		// Shorter timeout on subsequent polls.
		timeout = 30 * time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case env := <-s.pendingJobs:
		// Got a job — send it as a message.
		bodyBytes, err := json.Marshal(env.msg)
		if err != nil {
			http.Error(w, "marshal error", http.StatusInternalServerError)
			return
		}

		msgID := s.messageIDCounter.Add(1)

		// Store in active jobs and mark this session as having a job.
		s.mu.Lock()
		s.activeJobs[env.msg.JobID] = env
		s.requestToJob[env.msg.RequestID] = env.msg.JobID
		s.messageToJob[msgID] = env.msg.JobID
		if sess != nil {
			sess.hasJob = true
		}
		s.mu.Unlock()

		msg := TaskAgentMessage{
			MessageID:   msgID,
			MessageType: "PipelineAgentJobRequest",
			Body:        string(bodyBytes),
		}
		writeJSON(w, http.StatusOK, msg)

	case <-timer.C:
		// No job available — return empty response.
		w.WriteHeader(http.StatusAccepted)

	case <-r.Context().Done():
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	// Acknowledge message receipt — nothing to do.
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAcquireJob(w http.ResponseWriter, r *http.Request) {
	var req AcquireJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Find the job by message ID.
	s.mu.Lock()
	var env *jobEnvelope
	if jobID, ok := s.messageToJob[req.JobMessageID]; ok {
		env = s.activeJobs[jobID]
	}
	if env == nil {
		// Fallback: return any active job (for single-job compatibility).
		for _, e := range s.activeJobs {
			env = e
			break
		}
	}
	s.mu.Unlock()

	if env == nil {
		http.Error(w, "no job available", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, env.msg)
}

func (s *Server) handleRenewJob(w http.ResponseWriter, r *http.Request) {
	var req RenewJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	resp := RenewJobResponse{
		PlanID:      req.PlanID,
		JobID:       req.JobID,
		RequestID:   req.RequestID,
		LockedUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCompleteJob(w http.ResponseWriter, r *http.Request) {
	var req CompleteJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	env, ok := s.activeJobs[req.JobID]
	if ok {
		delete(s.activeJobs, req.JobID)
	}
	timeline := s.timelines[env.msg.Timeline.ID]
	logs := s.logs[env.msg.Timeline.ID]
	s.mu.Unlock()

	if ok {
		result := &JobCompletionResult{
			JobID:    req.JobID,
			Result:   req.Result,
			Outputs:  req.Outputs,
			Timeline: timeline,
			Logs:     make(map[string][]string),
		}
		for id, lines := range logs {
			result.Logs[strconv.Itoa(id)] = lines
		}
		env.result <- result
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleUpdateTimeline(w http.ResponseWriter, r *http.Request) {
	// Path: /_apis/v1/Timeline/{timelineId}
	path := strings.TrimPrefix(r.URL.Path, "/_apis/v1/Timeline/")
	parts := strings.SplitN(path, "/", 2)
	timelineID := parts[0]

	if len(parts) > 1 && strings.HasPrefix(parts[1], "logs") {
		// This is a log endpoint routed here — delegate.
		s.handleTimelineLogsInner(w, r, timelineID, parts[1])
		return
	}

	var records []TimelineRecord
	if err := json.NewDecoder(r.Body).Decode(&records); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	existing := s.timelines[timelineID]
	// Merge: update existing records by ID or append new ones.
	for _, rec := range records {
		found := false
		for i, e := range existing {
			if e.ID == rec.ID {
				existing[i] = rec
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, rec)
		}
	}
	s.timelines[timelineID] = existing
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, records)
}

func (s *Server) handleTimelineLogs(w http.ResponseWriter, r *http.Request) {
	// Path: /_apis/v1/Timeline/{timelineId}/logs or /_apis/v1/Timeline/{timelineId}/logs/{logId}
	path := strings.TrimPrefix(r.URL.Path, "/_apis/v1/Timeline/")
	parts := strings.SplitN(path, "/", 2)
	timelineID := parts[0]
	remaining := ""
	if len(parts) > 1 {
		remaining = parts[1]
	}
	s.handleTimelineLogsInner(w, r, timelineID, remaining)
}

func (s *Server) handleTimelineLogsInner(w http.ResponseWriter, r *http.Request, timelineID, path string) {
	s.mu.Lock()
	if s.logs[timelineID] == nil {
		s.logs[timelineID] = make(map[int][]string)
	}

	if path == "logs" || path == "logs/" {
		// Create a new log resource.
		nextID := s.logNextID[timelineID] + 1
		s.logNextID[timelineID] = nextID
		s.mu.Unlock()

		writeJSON(w, http.StatusOK, LogReference{
			ID:       nextID,
			Location: fmt.Sprintf("%s/_apis/v1/Timeline/%s/logs/%d", s.baseURL, timelineID, nextID),
		})
		return
	}

	// Append log lines: path = "logs/{logId}"
	logIDStr := strings.TrimPrefix(path, "logs/")
	logID, _ := strconv.Atoi(logIDStr)

	body, _ := io.ReadAll(r.Body)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	s.logs[timelineID][logID] = append(s.logs[timelineID][logID], lines...)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, LogReference{ID: logID})
}

func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	// The runner sends OPTIONS _apis/ to discover all API resource locations.
	// It caches these and uses the RouteTemplate to construct URLs for every API call.
	// RouteTemplate placeholders like {poolId} are substituted with actual values.
	locations := []ApiResourceLocation{
		{
			ID:              "a8c47e17-4d56-4a56-92bb-de7ea7dc65be",
			Area:            "distributedtask",
			ResourceName:    "pools",
			RouteTemplate:   "_apis/distributedtask/pools/{poolId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "e298ef32-5878-4cab-993c-043836571f42",
			Area:            "distributedtask",
			ResourceName:    "agents",
			RouteTemplate:   "_apis/distributedtask/pools/{poolId}/agents/{agentId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "134e239e-2df3-4794-a6f6-24f1f19ec8dc",
			Area:            "distributedtask",
			ResourceName:    "sessions",
			RouteTemplate:   "_apis/distributedtask/pools/{poolId}/sessions/{sessionId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "c3a054f6-7a8a-49c0-944e-3a8e5d7adfd7",
			Area:            "distributedtask",
			ResourceName:    "messages",
			RouteTemplate:   "_apis/distributedtask/pools/{poolId}/messages/{messageId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "fc825784-c92a-4299-9221-998a02d1b54f",
			Area:            "distributedtask",
			ResourceName:    "jobrequests",
			RouteTemplate:   "_apis/distributedtask/pools/{poolId}/jobrequests/{requestId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "8893bc5b-35b2-4be7-83cb-99e683551db4",
			Area:            "distributedtask",
			ResourceName:    "records",
			RouteTemplate:   "_apis/distributedtask/{scopeIdentifier}/{hubName}/timeline/{planId}/{timelineId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "46f5667d-263a-4684-91b1-dff7fdcf64e2",
			Area:            "distributedtask",
			ResourceName:    "logs",
			RouteTemplate:   "_apis/distributedtask/{scopeIdentifier}/{hubName}/logs/{planId}/{logId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "557624af-b29e-4c20-8ab0-0399d2204f3f",
			Area:            "distributedtask",
			ResourceName:    "planevents",
			RouteTemplate:   "_apis/distributedtask/{scopeIdentifier}/{hubName}/planevents/{planId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "858983e4-19bd-4c5e-864c-507b59b58b12",
			Area:            "distributedtask",
			ResourceName:    "feed",
			RouteTemplate:   "_apis/distributedtask/{scopeIdentifier}/{hubName}/feed/{planId}/{timelineId}/{recordId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "8cc1b02b-ae49-4516-b5ad-4f9b29967c30",
			Area:            "distributedtask",
			ResourceName:    "agentupdates",
			RouteTemplate:   "_apis/distributedtask/pools/{poolId}/agentupdates/{agentUpdateId}",
			ResourceVersion: 1,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
		{
			ID:              "8ffcd551-079c-493a-9c02-54346299d144",
			Area:            "distributedtask",
			ResourceName:    "packages",
			RouteTemplate:   "_apis/distributedtask/packages/{packageType}",
			ResourceVersion: 2,
			MinVersion:      "1.0",
			MaxVersion:      "7.0",
			ReleasedVersion: "6.0",
		},
	}

	if s.verbose {
		log.Printf("[broker] OPTIONS _apis/ → returning %d resource locations", len(locations))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(locations),
		"value": locations,
	})
}

func (s *Server) handleDistributedTaskCatchAll(w http.ResponseWriter, r *http.Request) {
	// Handle plan-scoped endpoints that use route-template-expanded URLs.
	// URL pattern: /_apis/distributedtask/{scopeId}/{hubName}/{type}/{planId}/...
	path := strings.TrimPrefix(r.URL.Path, "/_apis/distributedtask/")
	parts := strings.Split(path, "/")

	if s.verbose {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		log.Printf("[broker] distributedtask catch-all: %s /_apis/distributedtask/%s body_len=%d", r.Method, path, len(body))
	}

	// Minimum: scopeId/hubName/type/planId = 4 parts
	if len(parts) < 4 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}

	resourceType := parts[2] // "timeline", "logs", "feed", "planevents"

	switch resourceType {
	case "timeline":
		// PATCH /_apis/distributedtask/{scopeId}/{hubName}/timeline/{planId}/{timelineId}
		if r.Method == "PATCH" && len(parts) >= 5 {
			timelineID := parts[len(parts)-1]
			// The runner sends the body as a JSON array of timeline record objects.
			// We use json.RawMessage to avoid deserialization issues with unknown fields.
			body, _ := io.ReadAll(r.Body)
			var records []json.RawMessage
			if err := json.Unmarshal(body, &records); err != nil {
				// Try wrapping format {"value": [...]}
				var wrapped struct {
					Value []json.RawMessage `json:"value"`
				}
				if err2 := json.Unmarshal(body, &wrapped); err2 != nil {
					if s.verbose {
						log.Printf("[broker] timeline PATCH parse error: %v body: %s", err, string(body[:min(len(body), 500)]))
					}
					writeJSON(w, http.StatusOK, []any{})
					return
				}
				records = wrapped.Value
			}
			// Parse what we can into TimelineRecords.
			var parsedRecords []TimelineRecord
			for _, raw := range records {
				var rec TimelineRecord
				if err := json.Unmarshal(raw, &rec); err == nil {
					parsedRecords = append(parsedRecords, rec)
				}
			}
			s.mu.Lock()
			existing := s.timelines[timelineID]
			for _, rec := range parsedRecords {
				found := false
				for i, e := range existing {
					if e.ID == rec.ID {
						existing[i] = rec
						found = true
						break
					}
				}
				if !found {
					existing = append(existing, rec)
				}
			}
			s.timelines[timelineID] = existing
			s.mu.Unlock()
			// The runner expects VssJsonCollectionWrapper format.
			writeJSON(w, http.StatusOK, map[string]any{
				"count": len(parsedRecords),
				"value": parsedRecords,
			})
			return
		}

	case "logs":
		// POST /_apis/distributedtask/{scopeId}/{hubName}/logs/{planId}         — create log
		// POST /_apis/distributedtask/{scopeId}/{hubName}/logs/{planId}/{logId}  — append lines
		if r.Method == "POST" {
			planID := ""
			if len(parts) >= 4 {
				planID = parts[3]
			}
			timelineID := planID // Use planId as timeline key for log storage.

			s.mu.Lock()
			if s.logs[timelineID] == nil {
				s.logs[timelineID] = make(map[int][]string)
			}

			if len(parts) <= 4 {
				// Create log resource — runner sends a TaskLog object, expects TaskLog back.
				// TaskLog constructor requires path, and runner needs id + location.
				nextID := s.logNextID[timelineID] + 1
				s.logNextID[timelineID] = nextID
				s.mu.Unlock()
				writeJSON(w, http.StatusOK, map[string]any{
					"id":       nextID,
					"path":     fmt.Sprintf("logs\\%d", nextID),
					"location": fmt.Sprintf("%s/_apis/distributedtask/logs/%s/%d", s.baseURL, planID, nextID),
				})
				return
			}

			// Append log lines — runner sends raw text, expects TaskLog back.
			logIDStr := parts[len(parts)-1]
			logID, _ := strconv.Atoi(logIDStr)
			body, _ := io.ReadAll(r.Body)
			lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
			s.logs[timelineID][logID] = append(s.logs[timelineID][logID], lines...)
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"id":       logID,
				"path":     fmt.Sprintf("logs\\%d", logID),
				"location": fmt.Sprintf("%s/_apis/distributedtask/logs/%s/%d", s.baseURL, planID, logID),
			})
			return
		}

	case "feed":
		// POST /_apis/distributedtask/{scopeId}/{hubName}/feed/{planId}/{timelineId}/{recordId}
		// Timeline record feed — console output lines from the runner.
		if s.verbose {
			feedBody, _ := io.ReadAll(r.Body)
			var feedData struct {
				StepID string   `json:"stepId"`
				Value  []string `json:"value"`
			}
			if err := json.Unmarshal(feedBody, &feedData); err == nil && len(feedData.Value) > 0 {
				for _, line := range feedData.Value {
					log.Printf("[broker] [feed] %s", line)
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return

	case "planevents":
		// POST /_apis/distributedtask/{scopeId}/{hubName}/planevents/{planId}
		// With Plan version >= 8, the runner sends JobCompletedEvent here instead of
		// using PATCH jobrequests. This event contains the job result and outputs.
		body, _ := io.ReadAll(r.Body)
		var event struct {
			Name      string          `json:"name"`
			JobID     string          `json:"jobId"`
			RequestID int64           `json:"requestId"`
			Result    json.RawMessage `json:"result"` // TaskResult: string "succeeded" or int 0
			Outputs   map[string]struct {
				Value    string `json:"value"`
				IsSecret bool   `json:"isSecret"`
			} `json:"outputs"`
		}
		if err := json.Unmarshal(body, &event); err == nil && event.Name == "JobCompleted" {
			result := parseTaskResult(event.Result)
			if s.verbose {
				log.Printf("[broker] JobCompletedEvent: jobId=%s requestId=%d result=%s outputs=%v",
					event.JobID, event.RequestID, result, event.Outputs)
			}

			s.mu.Lock()
			var env *jobEnvelope
			var jobID string

			// Find active job by requestId.
			if jid, ok := s.requestToJob[event.RequestID]; ok {
				env = s.activeJobs[jid]
				jobID = jid
			}
			// Fallback: match by jobId from event.
			if env == nil {
				for id, e := range s.activeJobs {
					if e.msg.JobID == event.JobID {
						env = e
						jobID = id
						break
					}
				}
			}

			if env != nil {
				delete(s.activeJobs, jobID)
				delete(s.requestToJob, env.msg.RequestID)
				s.completedRequests[event.RequestID] = result
			}

			var timeline []TimelineRecord
			var logs map[int][]string
			if env != nil && env.msg.Timeline != nil {
				timeline = s.timelines[env.msg.Timeline.ID]
				logs = s.logs[env.msg.Timeline.ID]
			}
			s.mu.Unlock()

			if env != nil {
				completionResult := &JobCompletionResult{
					JobID:    jobID,
					Result:   result,
					Timeline: timeline,
					Outputs:  make(map[string]VariableValue),
					Logs:     make(map[string][]string),
				}
				for id, lines := range logs {
					completionResult.Logs[strconv.Itoa(id)] = lines
				}
				for k, v := range event.Outputs {
					completionResult.Outputs[k] = VariableValue{
						Value:    v.Value,
						IsSecret: v.IsSecret,
					}
				}
				if s.verbose {
					log.Printf("[broker] Job %s completed via plan event: result=%s outputs=%v",
						jobID, result, completionResult.Outputs)
				}
				env.result <- completionResult
			}
		} else if s.verbose {
			log.Printf("[broker] planevents body: %s", string(body))
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return

	case "actiondownloadinfo":
		// POST /_apis/distributedtask/{scopeId}/{hubName}/actiondownloadinfo/{planId}?jobId={jobId}
		// The runner sends an ActionReferenceList and expects ActionDownloadInfoCollection back
		// with tarball URLs for each action.
		body, _ := io.ReadAll(r.Body)
		var refList struct {
			Actions []struct {
				NameWithOwner string `json:"nameWithOwner"`
				Ref           string `json:"ref"`
				Path          string `json:"path"`
			} `json:"actions"`
		}
		if err := json.Unmarshal(body, &refList); err != nil {
			if s.verbose {
				log.Printf("[broker] actiondownloadinfo parse error: %v body: %s", err, string(body[:min(len(body), 500)]))
			}
			writeJSON(w, http.StatusOK, map[string]any{"actions": map[string]any{}})
			return
		}

		// The runner expects actions as a dictionary keyed by "nameWithOwner@ref"
		// (e.g., "actions/setup-node@v4"): IDictionary<string, ActionDownloadInfo>.
		// Route tarball downloads through our broker so we can patch action.yml
		// to strip expression tokens the legacy manifest parser can't handle.
		downloadInfos := make(map[string]map[string]any)
		for _, action := range refList.Actions {
			tarballURL := fmt.Sprintf("%s/_actions/tarball/%s/%s", s.baseURL, action.NameWithOwner, action.Ref)
			key := action.NameWithOwner + "@" + action.Ref
			downloadInfos[key] = map[string]any{
				"nameWithOwner":         action.NameWithOwner,
				"resolvedNameWithOwner": action.NameWithOwner,
				"ref":                   action.Ref,
				"resolvedSha":           action.Ref,
				"tarballUrl":            tarballURL,
				"zipballUrl":            tarballURL, // runner uses tarball; keep same URL
			}
			if s.verbose {
				log.Printf("[broker] actiondownloadinfo: %s@%s → %s (proxied)", action.NameWithOwner, action.Ref, tarballURL)
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{"actions": downloadInfos})
		return

	}

	// Unknown distributed task sub-path — accept gracefully.
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleCatchAll(w http.ResponseWriter, r *http.Request) {
	if s.verbose {
		body, _ := io.ReadAll(r.Body)
		log.Printf("[broker] UNHANDLED: %s %s body=%s", r.Method, r.URL.Path, string(body))
	}
	// Return 200 to avoid failing the runner on endpoints we haven't implemented.
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// serviceDefinitions returns all service definitions that the runner needs to discover
// endpoints. The runner's VssServerDataProvider.ConnectAsync converts these to
// ApiResourceLocation objects via ApiResourceLocation.FromServiceDefinition:
//   Identifier → Id, ServiceType → Area, DisplayName → ResourceName, RelativePath → RouteTemplate
// The runner then uses the RouteTemplate to construct URLs for all API calls.
func (s *Server) serviceDefinitions() []ServiceDefinition {
	dtOwner := "a85b8835-c1a1-4aac-ae97-1c3d0ba72dbd" // distributedtask area GUID
	fwOwner := "00000000-0000-0000-0000-000000000000"   // framework owner

	makeMappings := func(path string) []LocationMapping {
		return []LocationMapping{
			{AccessMappingMoniker: "PublicAccessMapping", Location: s.baseURL + path},
			{AccessMappingMoniker: "ServerAccessMapping", Location: s.baseURL + path},
		}
	}

	// dtService creates a distributedtask service definition with standard version fields.
	// MinVersion/MaxVersion/ReleasedVersion are required by NegotiateRequestVersion.
	dtService := func(id, name, relPath string, resVersion int) ServiceDefinition {
		return ServiceDefinition{
			ServiceType:      "distributedtask",
			Identifier:       id,
			DisplayName:      name,
			RelativePath:     relPath,
			ServiceOwner:     dtOwner,
			ResourceVersion:  resVersion,
			MinVersion:       "1.0",
			MaxVersion:       "7.0",
			ReleasedVersion:  "6.0",
			LocationMappings: makeMappings(""),
		}
	}

	return []ServiceDefinition{
		// Framework services
		{
			ServiceType:      "LocationService2",
			Identifier:       "951917ac-a960-4999-8464-e3f0aa25b381",
			DisplayName:      "Location Service (Rel 2)",
			RelativePath:     "/_apis/connectionData",
			ServiceOwner:     fwOwner,
			ResourceVersion:  1,
			MinVersion:       "1.0",
			MaxVersion:       "7.0",
			ReleasedVersion:  "6.0",
			LocationMappings: makeMappings("/_apis/connectionData"),
		},
		// Distributed task services — GUIDs from TaskResourceIds.cs.
		// The runner looks these up by Identifier (GUID) and uses RelativePath as route template.
		// FromServiceDefinition maps: ServiceType→Area, DisplayName→ResourceName, RelativePath→RouteTemplate
		dtService("a8c47e17-4d56-4a56-92bb-de7ea7dc65be", "pools",
			"_apis/distributedtask/pools/{poolId}", 1),
		dtService("e298ef32-5878-4cab-993c-043836571f42", "agents",
			"_apis/distributedtask/pools/{poolId}/agents/{agentId}", 1),
		dtService("134e239e-2df3-4794-a6f6-24f1f19ec8dc", "sessions",
			"_apis/distributedtask/pools/{poolId}/sessions/{sessionId}", 1),
		dtService("c3a054f6-7a8a-49c0-944e-3a8e5d7adfd7", "messages",
			"_apis/distributedtask/pools/{poolId}/messages/{messageId}", 1),
		dtService("fc825784-c92a-4299-9221-998a02d1b54f", "jobrequests",
			"_apis/distributedtask/pools/{poolId}/jobrequests/{requestId}", 1),
		dtService("8893bc5b-35b2-4be7-83cb-99e683551db4", "records",
			"_apis/distributedtask/{scopeIdentifier}/{hubName}/timeline/{planId}/{timelineId}", 1),
		dtService("46f5667d-263a-4684-91b1-dff7fdcf64e2", "logs",
			"_apis/distributedtask/{scopeIdentifier}/{hubName}/logs/{planId}/{logId}", 1),
		dtService("557624af-b29e-4c20-8ab0-0399d2204f3f", "planevents",
			"_apis/distributedtask/{scopeIdentifier}/{hubName}/planevents/{planId}", 1),
		dtService("858983e4-19bd-4c5e-864c-507b59b58b12", "feed",
			"_apis/distributedtask/{scopeIdentifier}/{hubName}/feed/{planId}/{timelineId}/{recordId}", 1),
		dtService("8cc1b02b-ae49-4516-b5ad-4f9b29967c30", "agentupdates",
			"_apis/distributedtask/pools/{poolId}/agentupdates/{agentUpdateId}", 1),
		dtService("8ffcd551-079c-493a-9c02-54346299d144", "packages",
			"_apis/distributedtask/packages/{packageType}", 2),
		dtService("27d7f831-88c1-4719-8ca1-6a061dad90eb", "actiondownloadinfo",
			"_apis/distributedtask/{scopeIdentifier}/{hubName}/actiondownloadinfo/{planId}", 1),
	}
}

func ptrDescriptor(d IdentityDescriptor) *IdentityDescriptor { return &d }

// parseTaskResult parses a TaskResult value that may be a string ("succeeded")
// or an integer (0). The C# runner sends strings via Newtonsoft enum serialization.
func parseTaskResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first (most common from the runner).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try numeric.
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return taskResultToString(int(n))
	}
	return string(raw)
}

// taskResultToString converts a TaskResult enum value to a string.
// TaskResult: 0=Succeeded, 1=SucceededWithIssues, 2=Failed, 3=Canceled, 4=Skipped, 5=Abandoned.
func taskResultToString(v int) string {
	switch v {
	case 0:
		return "succeeded"
	case 1:
		return "succeededWithIssues"
	case 2:
		return "failed"
	case 3:
		return "cancelled"
	case 4:
		return "skipped"
	case 5:
		return "abandoned"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
