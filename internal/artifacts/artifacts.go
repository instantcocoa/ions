package artifacts

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Server serves artifact upload/download APIs for both v3 (pipelines) and v4 (twirp/results).
type Server struct {
	store   *Store
	baseURL string
}

// NewServer creates a new artifact server.
func NewServer(artifactDir, baseURL string) (*Server, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}
	return &Server{
		store:   NewStore(artifactDir),
		baseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

// RegisterRoutes registers artifact API routes on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Results API v4 (Twirp) — used by actions/upload-artifact@v4, actions/download-artifact@v4.
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", s.handleV4CreateArtifact)
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact", s.handleV4FinalizeArtifact)
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.ArtifactService/ListArtifacts", s.handleV4ListArtifacts)
	mux.HandleFunc("POST /twirp/github.actions.results.api.v1.ArtifactService/GetSignedArtifactURL", s.handleV4GetSignedURL)
	mux.HandleFunc("PUT /_apis/results/upload/", s.handleV4Upload)
	mux.HandleFunc("GET /_apis/results/download/", s.handleV4Download)

	// Pipelines API v3 — used by actions/upload-artifact@v3, actions/download-artifact@v3.
	mux.HandleFunc("POST /_apis/pipelines/workflows/{runId}/artifacts", s.handleV3CreateArtifact)
	mux.HandleFunc("PATCH /_apis/pipelines/workflows/{runId}/artifacts", s.handleV3FinalizeArtifact)
	mux.HandleFunc("GET /_apis/pipelines/workflows/{runId}/artifacts", s.handleV3ListArtifacts)
	mux.HandleFunc("PUT /_apis/resources/Containers/", s.handleV3Upload)
	mux.HandleFunc("GET /_apis/resources/Containers/", s.handleV3Download)
}

// --- Results API v4 handlers ---

func (s *Server) handleV4CreateArtifact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowRunBackendID    string `json:"workflow_run_backend_id"`
		WorkflowJobRunBackendID string `json:"workflow_job_run_backend_id"`
		Name                    string `json:"name"`
		Version                 int    `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	a, err := s.store.Create(req.Name, req.WorkflowRunBackendID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[artifacts] v4 CreateArtifact: name=%s id=%s", req.Name, a.ID)

	writeJSON(w, map[string]any{
		"ok":                true,
		"signed_upload_url": fmt.Sprintf("%s/_apis/results/upload/%s?sig=unused", s.baseURL, a.ID),
	})
}

func (s *Server) handleV4Upload(w http.ResponseWriter, r *http.Request) {
	id := extractPathSegment(r.URL.Path, "/_apis/results/upload/")
	if id == "" {
		http.Error(w, "missing artifact id", http.StatusBadRequest)
		return
	}

	a := s.store.Get(id)
	if a == nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	comp := r.URL.Query().Get("comp")

	switch comp {
	case "block":
		// Azure Blob Storage: Stage Block — store block data in temp file.
		blockID := r.URL.Query().Get("blockid")
		blockPath := s.store.BlockPath(id, blockID)
		f, err := os.Create(blockPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		if _, err := io.Copy(f, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("x-ms-request-id", "ions")
		w.Header().Set("x-ms-version", "2020-10-02")
		w.WriteHeader(http.StatusCreated)

	case "blocklist":
		// Azure Blob Storage: Commit Block List — assemble blocks into final blob.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		blockIDs := parseBlockListXML(string(body))

		blobPath := s.store.BlobPath(id)
		f, err := os.Create(blobPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		for _, bid := range blockIDs {
			blockPath := s.store.BlockPath(id, bid)
			blockData, err := os.ReadFile(blockPath)
			if err != nil {
				log.Printf("[artifacts] missing block %s for artifact %s: %v", bid, id, err)
				continue
			}
			f.Write(blockData)
			os.Remove(blockPath)
		}

		log.Printf("[artifacts] v4 Upload committed: id=%s blocks=%d", id, len(blockIDs))
		w.Header().Set("x-ms-request-id", "ions")
		w.Header().Set("x-ms-version", "2020-10-02")
		w.Header().Set("ETag", "\"ions-"+id+"\"")
		w.WriteHeader(http.StatusCreated)

	default:
		// Simple Put Blob — entire content in one request.
		blobPath := s.store.BlobPath(id)
		f, err := os.Create(blobPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		n, err := io.Copy(f, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("[artifacts] v4 Upload: id=%s bytes=%d", id, n)
		w.Header().Set("x-ms-request-id", "ions")
		w.Header().Set("x-ms-version", "2020-10-02")
		w.Header().Set("ETag", "\"ions-"+id+"\"")
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *Server) handleV4FinalizeArtifact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowRunBackendID    string `json:"workflow_run_backend_id"`
		WorkflowJobRunBackendID string `json:"workflow_job_run_backend_id"`
		Name                    string `json:"name"`
		Size                    string `json:"size"` // string, not int
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	size, _ := strconv.ParseInt(req.Size, 10, 64)

	// Find the artifact by name and workflow run.
	a := s.store.FindByName(req.WorkflowRunBackendID, req.Name)
	if a == nil {
		// Not finalized yet — search all artifacts including non-finalized.
		a = s.findUnfinalized(req.WorkflowRunBackendID, req.Name)
	}
	if a == nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	if err := s.store.Finalize(a.ID, size); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[artifacts] v4 FinalizeArtifact: name=%s id=%s size=%d", req.Name, a.ID, size)

	writeJSON(w, map[string]any{
		"ok":          true,
		"artifact_id": a.ID,
	})
}

func (s *Server) handleV4ListArtifacts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowRunBackendID    string          `json:"workflow_run_backend_id"`
		WorkflowJobRunBackendID string          `json:"workflow_job_run_backend_id"`
		NameFilter              json.RawMessage `json:"name_filter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// name_filter can be a plain string (protobuf JSON) or {"value": "..."} (wrapper).
	var nameFilter string
	if len(req.NameFilter) > 0 {
		var s string
		if json.Unmarshal(req.NameFilter, &s) == nil {
			nameFilter = s
		} else {
			var wrapper struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(req.NameFilter, &wrapper) == nil {
				nameFilter = wrapper.Value
			}
		}
	}

	artifacts := s.store.List(req.WorkflowRunBackendID, nameFilter)

	type artifactEntry struct {
		WorkflowRunBackendID    string `json:"workflow_run_backend_id"`
		WorkflowJobRunBackendID string `json:"workflow_job_run_backend_id"`
		DatabaseID              string `json:"database_id"`
		Name                    string `json:"name"`
		Size                    string `json:"size"`
	}

	entries := make([]artifactEntry, 0, len(artifacts))
	for _, a := range artifacts {
		entries = append(entries, artifactEntry{
			WorkflowRunBackendID:    a.WorkflowRunID,
			WorkflowJobRunBackendID: "",
			DatabaseID:              a.ID,
			Name:                    a.Name,
			Size:                    strconv.FormatInt(a.Size, 10),
		})
	}

	writeJSON(w, map[string]any{"artifacts": entries})
}

func (s *Server) handleV4GetSignedURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowRunBackendID string `json:"workflow_run_backend_id"`
		Name                 string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	a := s.store.FindByName(req.WorkflowRunBackendID, req.Name)
	if a == nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	writeJSON(w, map[string]any{
		"signed_url": fmt.Sprintf("%s/_apis/results/download/%s?sig=unused", s.baseURL, a.ID),
	})
}

func (s *Server) handleV4Download(w http.ResponseWriter, r *http.Request) {
	id := extractPathSegment(r.URL.Path, "/_apis/results/download/")
	if id == "" {
		http.Error(w, "missing artifact id", http.StatusBadRequest)
		return
	}

	blobPath := s.store.BlobPath(id)
	http.ServeFile(w, r, blobPath)
}

// --- Pipelines API v3 handlers ---

func (s *Server) handleV3CreateArtifact(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")
	artifactName := r.URL.Query().Get("artifactName")

	var req struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := artifactName
	if name == "" {
		name = req.Name
	}

	a, err := s.store.Create(name, runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[artifacts] v3 CreateArtifact: name=%s id=%s runId=%s", name, a.ID, runID)

	writeJSON(w, map[string]any{
		"containerId":              mustAtoi(a.ID),
		"name":                     name,
		"fileContainerResourceUrl": fmt.Sprintf("%s/_apis/resources/Containers/%s", s.baseURL, a.ID),
		"size":                     0,
		"type":                     "actions_storage",
	})
}

func (s *Server) handleV3Upload(w http.ResponseWriter, r *http.Request) {
	// Path: /_apis/resources/Containers/{containerId}?itemPath=...
	path := strings.TrimPrefix(r.URL.Path, "/_apis/resources/Containers/")
	id := strings.SplitN(path, "/", 2)[0]
	if id == "" {
		http.Error(w, "missing container id", http.StatusBadRequest)
		return
	}

	a := s.store.Get(id)
	if a == nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	itemPath := r.URL.Query().Get("itemPath")

	blobPath := s.store.BlobPath(id)

	// Handle Content-Range for chunked uploads.
	contentRange := r.Header.Get("Content-Range")
	if contentRange != "" {
		start, _, err := parseContentRange(contentRange)
		if err != nil {
			http.Error(w, "invalid Content-Range: "+err.Error(), http.StatusBadRequest)
			return
		}

		f, err := os.OpenFile(blobPath, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		if _, err := f.Seek(start, io.SeekStart); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if _, err := io.Copy(f, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		f, err := os.Create(blobPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		if _, err := io.Copy(f, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	cid := mustAtoi(id)
	log.Printf("[artifacts] v3 Upload: id=%s itemPath=%s", id, itemPath)

	writeJSON(w, map[string]any{
		"containerId": cid,
		"itemPath":    itemPath,
		"isFile":      true,
	})
}

func (s *Server) handleV3FinalizeArtifact(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")
	artifactName := r.URL.Query().Get("artifactName")

	// Find the artifact by name (may not be finalized yet).
	a := s.findUnfinalized(runID, artifactName)
	if a == nil {
		a = s.store.FindByName(runID, artifactName)
	}
	if a == nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	// Get actual size from blob on disk.
	var size int64
	if info, err := os.Stat(s.store.BlobPath(a.ID)); err == nil {
		size = info.Size()
	}

	if err := s.store.Finalize(a.ID, size); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[artifacts] v3 FinalizeArtifact: name=%s id=%s size=%d", artifactName, a.ID, size)

	writeJSON(w, map[string]any{
		"containerId": mustAtoi(a.ID),
		"name":        artifactName,
		"size":        size,
		"type":        "actions_storage",
	})
}

func (s *Server) handleV3ListArtifacts(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")
	artifactName := r.URL.Query().Get("artifactName")

	artifacts := s.store.List(runID, artifactName)

	type v3Entry struct {
		ContainerID              int    `json:"containerId"`
		Name                     string `json:"name"`
		FileContainerResourceURL string `json:"fileContainerResourceUrl"`
		Size                     int64  `json:"size"`
		Type                     string `json:"type"`
	}

	entries := make([]v3Entry, 0, len(artifacts))
	for _, a := range artifacts {
		entries = append(entries, v3Entry{
			ContainerID:              mustAtoi(a.ID),
			Name:                     a.Name,
			FileContainerResourceURL: fmt.Sprintf("%s/_apis/resources/Containers/%s", s.baseURL, a.ID),
			Size:                     a.Size,
			Type:                     "actions_storage",
		})
	}

	writeJSON(w, map[string]any{
		"count": len(entries),
		"value": entries,
	})
}

func (s *Server) handleV3Download(w http.ResponseWriter, r *http.Request) {
	// Path: /_apis/resources/Containers/{containerId}?itemPath=...
	path := strings.TrimPrefix(r.URL.Path, "/_apis/resources/Containers/")
	id := strings.SplitN(path, "/", 2)[0]
	if id == "" {
		http.Error(w, "missing container id", http.StatusBadRequest)
		return
	}

	blobPath := s.store.BlobPath(id)
	http.ServeFile(w, r, blobPath)
}

// --- Helpers ---

func (s *Server) findUnfinalized(workflowRunID, name string) *Artifact {
	s.store.mu.RLock()
	defer s.store.mu.RUnlock()

	for _, a := range s.store.artifacts {
		if !a.Finalized && a.WorkflowRunID == workflowRunID && a.Name == name {
			return a
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// extractPathSegment extracts the segment after prefix, stripping query params.
func extractPathSegment(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// parseContentRange parses "bytes start-end/total" and returns start and end.
func parseContentRange(header string) (start, end int64, err error) {
	// Format: "bytes 0-1023/4096" or "bytes 0-1023/*"
	header = strings.TrimPrefix(header, "bytes ")
	parts := strings.SplitN(header, "/", 2)
	if len(parts) < 1 {
		return 0, 0, fmt.Errorf("invalid format")
	}
	rangeParts := strings.SplitN(parts[0], "-", 2)
	if len(rangeParts) != 2 {
		return 0, 0, fmt.Errorf("invalid range")
	}
	start, err = strconv.ParseInt(rangeParts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start: %w", err)
	}
	end, err = strconv.ParseInt(rangeParts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end: %w", err)
	}
	return start, end, nil
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// parseBlockListXML extracts block IDs from Azure Blob Storage BlockList XML.
// Format: <BlockList><Latest>blockid1</Latest><Latest>blockid2</Latest></BlockList>
func parseBlockListXML(body string) []string {
	var ids []string
	for {
		start := strings.Index(body, "<Latest>")
		if start < 0 {
			// Also check for <Committed> and <Uncommitted> tags.
			start = strings.Index(body, "<Committed>")
			if start < 0 {
				start = strings.Index(body, "<Uncommitted>")
				if start < 0 {
					break
				}
				body = body[start+len("<Uncommitted>"):]
				end := strings.Index(body, "</Uncommitted>")
				if end < 0 {
					break
				}
				ids = append(ids, strings.TrimSpace(body[:end]))
				body = body[end:]
				continue
			}
			body = body[start+len("<Committed>"):]
			end := strings.Index(body, "</Committed>")
			if end < 0 {
				break
			}
			ids = append(ids, strings.TrimSpace(body[:end]))
			body = body[end:]
			continue
		}
		body = body[start+len("<Latest>"):]
		end := strings.Index(body, "</Latest>")
		if end < 0 {
			break
		}
		ids = append(ids, strings.TrimSpace(body[:end]))
		body = body[end:]
	}
	return ids
}
