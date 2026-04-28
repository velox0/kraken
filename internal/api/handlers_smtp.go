package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/velox0/kraken/internal/db"
)

type createSMTPProfileRequest struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	FromEmail string `json:"from_email"`
}

func (h *Handler) listSMTPProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := h.store.ListSMTPProfiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (h *Handler) createSMTPProfile(w http.ResponseWriter, r *http.Request) {
	var req createSMTPProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Host == "" || req.Port <= 0 || req.Username == "" || req.Password == "" || req.FromEmail == "" {
		writeError(w, http.StatusBadRequest, errors.New("host, port, username, password, from_email are required"))
		return
	}
	profile, err := h.store.CreateSMTPProfile(r.Context(), req.Host, req.Port, req.Username, req.Password, req.FromEmail)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, db.SMTPProfileSummary{
		ID:        profile.ID,
		Host:      profile.Host,
		Port:      profile.Port,
		Username:  profile.Username,
		FromEmail: profile.FromEmail,
	})
}
