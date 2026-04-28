package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/queue"
)

func (h *Handler) listProjectFixes(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fixes, err := h.store.ListProjectFixes(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, fixes)
}

func (h *Handler) createProjectFix(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req db.CreateFixParams
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ScriptPath) == "" || strings.TrimSpace(req.SupportedErrorPattern) == "" {
		writeError(w, http.StatusBadRequest, errors.New("name, script_path and supported_error_pattern are required"))
		return
	}

	fix, err := h.store.CreateFix(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.store.AttachFixToProject(r.Context(), projectID, fix.ID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, fix)
}

func (h *Handler) uploadProjectFix(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid multipart form"))
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	fixType := strings.TrimSpace(r.FormValue("type"))
	pattern := strings.TrimSpace(r.FormValue("supported_error_pattern"))
	timeoutSec, err := strconv.Atoi(strings.TrimSpace(r.FormValue("timeout_sec")))
	if err != nil || timeoutSec <= 0 {
		timeoutSec = 60
	}
	if name == "" || pattern == "" {
		writeError(w, http.StatusBadRequest, errors.New("name and supported_error_pattern are required"))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("file is required"))
		return
	}
	defer file.Close()

	if header.Size <= 0 || header.Size > (2<<20) {
		writeError(w, http.StatusBadRequest, errors.New("file must be between 1 byte and 2MB"))
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".sh" && ext != ".bat" && ext != ".cmd" {
		writeError(w, http.StatusBadRequest, errors.New("only .sh, .bat and .cmd files are allowed"))
		return
	}

	safeName := sanitizeFilename(strings.TrimSuffix(header.Filename, ext))
	if safeName == "" {
		safeName = "fix"
	}
	storedFileName := "uploaded-" + strconv.FormatInt(time.Now().UTC().Unix(), 10) + "-" + safeName + ext

	if err := os.MkdirAll(h.fixScriptsDir, 0o750); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	absPath := filepath.Join(h.fixScriptsDir, storedFileName)
	out, err := os.OpenFile(absPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o750)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, io.LimitReader(file, 2<<20)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	fix, err := h.store.CreateFix(r.Context(), db.CreateFixParams{
		Name:                  name,
		Type:                  fixType,
		ScriptPath:            storedFileName,
		SupportedErrorPattern: pattern,
		TimeoutSec:            timeoutSec,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.store.AttachFixToProject(r.Context(), projectID, fix.ID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"fix":         fix,
		"uploaded_as": storedFileName,
	})
}

func (h *Handler) updateProjectFix(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fixID, err := parseIDParam(r, "fixID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	existing, err := h.store.GetProjectFix(r.Context(), projectID, fixID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, errors.New("fix not attached to project"))
		return
	}

	var req db.UpdateFixParams
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ScriptPath) == "" || strings.TrimSpace(req.SupportedErrorPattern) == "" {
		writeError(w, http.StatusBadRequest, errors.New("name, script_path and supported_error_pattern are required"))
		return
	}

	updated, err := h.store.UpdateFix(r.Context(), fixID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) deleteProjectFix(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fixID, err := parseIDParam(r, "fixID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	existing, err := h.store.GetProjectFix(r.Context(), projectID, fixID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, errors.New("fix not attached to project"))
		return
	}

	if err := h.store.DetachFixFromProject(r.Context(), projectID, fixID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.store.DeleteFix(r.Context(), fixID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"fix_id":  fixID,
	})
}

type runFixRequest struct {
	RequestedBy string `json:"requested_by"`
}

func (h *Handler) runProjectFix(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fixID, err := parseIDParam(r, "fixID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	fix, err := h.store.GetProjectFix(r.Context(), projectID, fixID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if fix == nil {
		writeError(w, http.StatusNotFound, errors.New("fix not attached to project"))
		return
	}

	var req runFixRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if strings.TrimSpace(req.RequestedBy) == "" {
		req.RequestedBy = "api"
	}

	if err := h.queue.EnqueueFix(r.Context(), queue.FixJob{
		ProjectID:   projectID,
		FixID:       fixID,
		RequestedBy: req.RequestedBy,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"project_id":   projectID,
		"fix_id":       fixID,
		"queued":       true,
		"requested_by": req.RequestedBy,
	})
}

func (h *Handler) listFixRuns(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runs, err := h.store.ListFixRunsByProject(r.Context(), projectID, parseLimit(r, 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) getFixRun(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runID, err := parseIDParam(r, "runID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	run, err := h.store.GetFixRun(r.Context(), projectID, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if run == nil {
		writeError(w, http.StatusNotFound, errors.New("fix run not found"))
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) listFixEnvVars(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	vars, err := h.store.ListFixEnvVars(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, vars)
}

type setEnvVarRequest struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

func (h *Handler) setFixEnvVar(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req setEnvVarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	v, err := h.store.UpsertFixEnvVar(r.Context(), projectID, req.Name, req.Value, req.IsSecret)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) deleteFixEnvVar(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	envVarID, err := parseIDParam(r, "envVarID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.store.DeleteFixEnvVar(r.Context(), projectID, envVarID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": envVarID})
}
