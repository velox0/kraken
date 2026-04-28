package api

import (
	"net/http"
)

func (h *Handler) listProjectIncidents(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	incidents, err := h.store.ListIncidentsByProject(r.Context(), projectID, parseLimit(r, 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, incidents)
}
