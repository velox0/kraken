package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/velox0/kraken/internal/db"
)

func (h *Handler) listProjectChecks(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	checks, err := h.store.ListChecksByProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, checks)
}

func (h *Handler) createProjectCheck(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req db.CreateCheckParams
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.ProjectID = projectID
	if strings.TrimSpace(req.Target) == "" || strings.TrimSpace(req.Type) == "" {
		writeError(w, http.StatusBadRequest, errors.New("type and target are required"))
		return
	}
	check, err := h.store.CreateCheck(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, check)
}

func (h *Handler) listCheckRunsByCheck(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	checkID, err := parseIDParam(r, "checkID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runs, err := h.store.ListCheckRunsByCheck(r.Context(), projectID, checkID, parseLimit(r, 120))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) listProjectCheckRuns(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runs, err := h.store.ListCheckRunsByProject(r.Context(), projectID, parseLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) listRouteHealth(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	routes, err := h.store.ListRouteHealthByProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}
