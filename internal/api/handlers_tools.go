package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/velox0/kraken/internal/autofix"
)

// listFixTools returns the current tools whitelist and each tool's resolution status.
func (h *Handler) listFixTools(w http.ResponseWriter, r *http.Request) {
	if h.autofixEngine == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("autofix engine not configured"))
		return
	}

	tools := h.autofixEngine.AllowedTools()

	type toolInfo struct {
		Name     string `json:"name"`
		Resolved bool   `json:"resolved"`
	}

	// Do a quick lookup per tool to see if it's actually available.
	result := make([]toolInfo, 0, len(tools))
	syncResult := h.autofixEngine.SyncToolsDir()
	statusByName := make(map[string]autofix.ToolStatus, len(syncResult.Linked))
	for _, ts := range syncResult.Linked {
		statusByName[ts.Name] = ts
	}
	for _, t := range tools {
		info := toolInfo{Name: t}
		if ts, ok := statusByName[t]; ok {
			info.Resolved = ts.Resolved
		}
		result = append(result, info)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tools_dir": syncResult.ToolsDir,
		"tools":     result,
	})
}

type updateFixToolsRequest struct {
	Tools []string `json:"tools"`
}

// updateFixTools replaces the tools whitelist and re-syncs the tools directory.
func (h *Handler) updateFixTools(w http.ResponseWriter, r *http.Request) {
	if h.autofixEngine == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("autofix engine not configured"))
		return
	}

	var req updateFixToolsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Validate: at least the runners (bash/sh or cmd.exe) must remain.
	cleaned := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		t = strings.TrimSpace(t)
		if t != "" {
			cleaned = append(cleaned, t)
		}
	}
	if len(cleaned) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("tools list cannot be empty"))
		return
	}

	result := h.autofixEngine.SetAllowedTools(cleaned)
	writeJSON(w, http.StatusOK, result)
}
