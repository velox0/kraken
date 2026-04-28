package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/queue"
)

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	var req db.CreateProjectParams
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Domain) == "" || req.CheckIntervalSec <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("name, domain and check_interval_sec are required"))
		return
	}
	project, err := h.store.CreateProject(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.store.DeleteProject(r.Context(), projectID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":    true,
		"project_id": projectID,
	})
}

type patchAutofixRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *Handler) patchProjectAutofix(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req patchAutofixRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.store.SetProjectAutofix(r.Context(), projectID, req.Enabled); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_id": projectID, "autofix_enabled": req.Enabled})
}

type projectSettingsResponse struct {
	Project      db.Project              `json:"project"`
	Checks       []db.Check              `json:"checks"`
	SMTPProfiles []db.SMTPProfileSummary `json:"smtp_profiles"`
}

type updateProjectSettingsRequest struct {
	Name                     string                  `json:"name"`
	Domain                   string                  `json:"domain"`
	CheckIntervalSec         int                     `json:"check_interval_sec"`
	FailureThreshold         int                     `json:"failure_threshold"`
	AutofixEnabled           bool                    `json:"autofix_enabled"`
	MaxAutofixRetries        int                     `json:"max_autofix_retries"`
	SMTPProfileID            *int64                  `json:"smtp_profile_id"`
	AlertEmails              []string                `json:"alert_emails"`
	EmailSubjectOpened       string                  `json:"email_subject_opened"`
	EmailBodyOpened          string                  `json:"email_body_opened"`
	EmailSubjectResolved     string                  `json:"email_subject_resolved"`
	EmailBodyResolved        string                  `json:"email_body_resolved"`
	EmailSubjectRepeated     string                  `json:"email_subject_repeated"`
	EmailBodyRepeated        string                  `json:"email_body_repeated"`
	EmailSubjectAutofixLimit string                  `json:"email_subject_autofix_limit"`
	EmailBodyAutofixLimit    string                  `json:"email_body_autofix_limit"`
	Checks                   []db.ReplaceCheckParams `json:"checks"`
}

func (h *Handler) getProjectSettings(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	project, err := h.store.GetProjectByID(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, errors.New("project not found"))
		return
	}

	checks, err := h.store.ListChecksByProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	profiles, err := h.store.ListSMTPProfiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, projectSettingsResponse{
		Project:      *project,
		Checks:       checks,
		SMTPProfiles: profiles,
	})
}

func (h *Handler) updateProjectSettings(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var req updateProjectSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Domain = strings.TrimSpace(req.Domain)
	if req.Name == "" || req.Domain == "" {
		writeError(w, http.StatusBadRequest, errors.New("name and domain are required"))
		return
	}
	if req.CheckIntervalSec <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("check_interval_sec must be greater than 0"))
		return
	}
	if req.FailureThreshold <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("failure_threshold must be greater than 0"))
		return
	}

	if req.SMTPProfileID != nil {
		if *req.SMTPProfileID <= 0 {
			req.SMTPProfileID = nil
		} else {
			profile, err := h.store.GetSMTPProfile(r.Context(), *req.SMTPProfileID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if profile == nil {
				writeError(w, http.StatusBadRequest, errors.New("smtp_profile_id not found"))
				return
			}
		}
	}

	checkInputs := req.Checks
	if checkInputs == nil {
		existingChecks, err := h.store.ListChecksByProject(r.Context(), projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		checkInputs = make([]db.ReplaceCheckParams, 0, len(existingChecks))
		for _, c := range existingChecks {
			id := c.ID
			checkInputs = append(checkInputs, db.ReplaceCheckParams{
				ID:         &id,
				Type:       c.Type,
				Target:     c.Target,
				TimeoutMs:  c.TimeoutMs,
				Assertions: c.Assertions,
			})
		}
	}

	updatedProject, err := h.store.UpdateProject(r.Context(), projectID, db.UpdateProjectParams{
		Name:                     req.Name,
		Domain:                   req.Domain,
		CheckIntervalSec:         req.CheckIntervalSec,
		FailureThreshold:         req.FailureThreshold,
		AutofixEnabled:           req.AutofixEnabled,
		MaxAutofixRetries:        req.MaxAutofixRetries,
		SMTPProfileID:            req.SMTPProfileID,
		AlertEmails:              normalizeEmails(req.AlertEmails),
		EmailSubjectOpened:       req.EmailSubjectOpened,
		EmailBodyOpened:          req.EmailBodyOpened,
		EmailSubjectResolved:     req.EmailSubjectResolved,
		EmailBodyResolved:        req.EmailBodyResolved,
		EmailSubjectRepeated:     req.EmailSubjectRepeated,
		EmailBodyRepeated:        req.EmailBodyRepeated,
		EmailSubjectAutofixLimit: req.EmailSubjectAutofixLimit,
		EmailBodyAutofixLimit:    req.EmailBodyAutofixLimit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updatedChecks, err := h.store.ReplaceProjectChecks(r.Context(), projectID, checkInputs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	profiles, err := h.store.ListSMTPProfiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, projectSettingsResponse{
		Project:      updatedProject,
		Checks:       updatedChecks,
		SMTPProfiles: profiles,
	})
}

func (h *Handler) runProjectNow(w http.ResponseWriter, r *http.Request) {
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
	for _, check := range checks {
		if err := h.queue.EnqueueCheck(r.Context(), queue.CheckJob{CheckID: check.ID, Reason: "manual"}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": len(checks), "project_id": projectID})
}
