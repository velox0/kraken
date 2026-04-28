package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/velox0/kraken/internal/assets"
	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/logbuf"
	"github.com/velox0/kraken/internal/queue"
)

type Handler struct {
	store         *db.Store
	queue         *queue.RedisQueue
	fixScriptsDir string
	uiDir         string
	logBuf        *logbuf.Buffer
}

func NewHandler(store *db.Store, q *queue.RedisQueue, fixScriptsDir, uiDir string, lb *logbuf.Buffer) *Handler {
	return &Handler{
		store:         store,
		queue:         q,
		fixScriptsDir: fixScriptsDir,
		uiDir:         uiDir,
		logBuf:        lb,
	}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/v1", func(v1 chi.Router) {
		v1.Post("/login", h.login)

		v1.Group(func(r chi.Router) {
			r.Use(h.Authenticate)
			r.Post("/logout", h.logout)
			r.Get("/auth/me", h.authMe)
			r.Put("/auth/password", h.changePassword)
			r.Post("/api_keys", h.createAPIKey)
			r.Get("/logs/stream", h.streamLogs)

			// Admin user management
			r.Route("/admin/users", func(admin chi.Router) {
				admin.Use(RequireScope("users:manage"))
				admin.Get("/", h.adminListUsers)
				admin.Post("/", h.adminCreateUser)
				admin.Get("/{userID}", h.adminGetUser)
				admin.Put("/{userID}", h.adminUpdateUser)
				admin.Delete("/{userID}", h.adminDeleteUser)
				admin.Post("/{userID}/unfreeze", h.adminUnfreezeUser)
			})

			r.With(RequireScope("projects:read")).Get("/projects", h.listProjects)
			r.With(RequireScope("projects:write")).Post("/projects", h.createProject)
			r.With(RequireScope("projects:delete")).Delete("/projects/{projectID}", h.deleteProject)
			r.With(RequireScope("projects.autofix:write")).Patch("/projects/{projectID}/autofix", h.patchProjectAutofix)
			r.With(RequireScope("projects:read")).Get("/projects/{projectID}/settings", h.getProjectSettings)
			r.With(RequireScope("projects:write")).Put("/projects/{projectID}/settings", h.updateProjectSettings)
			r.With(RequireScope("checks:read")).Get("/projects/{projectID}/checks", h.listProjectChecks)
			r.With(RequireScope("checks:write")).Post("/projects/{projectID}/checks", h.createProjectCheck)
			r.With(RequireScope("check_runs:read")).Get("/projects/{projectID}/checks/{checkID}/runs", h.listCheckRunsByCheck)
			r.With(RequireScope("checks:run")).Post("/projects/{projectID}/run-now", h.runProjectNow)
			r.With(RequireScope("logs:read")).Get("/projects/{projectID}/logs", h.listProjectLogs)
			r.With(RequireScope("incidents:read")).Get("/projects/{projectID}/incidents", h.listProjectIncidents)
			r.With(RequireScope("check_runs:read")).Get("/projects/{projectID}/check-runs", h.listProjectCheckRuns)
			r.With(RequireScope("routes:read")).Get("/projects/{projectID}/routes/health", h.listRouteHealth)
			r.With(RequireScope("uptime:read")).Get("/projects/{projectID}/uptime", h.getProjectUptime)
			r.With(RequireScope("fixes:read")).Get("/projects/{projectID}/fixes", h.listProjectFixes)
			r.With(RequireScope("fixes:write")).Post("/projects/{projectID}/fixes", h.createProjectFix)
			r.With(RequireScope("fixes:write")).Post("/projects/{projectID}/fixes/upload", h.uploadProjectFix)
			r.With(RequireScope("fixes:write")).Put("/projects/{projectID}/fixes/{fixID}", h.updateProjectFix)
			r.With(RequireScope("fixes:delete")).Delete("/projects/{projectID}/fixes/{fixID}", h.deleteProjectFix)
			r.With(RequireScope("fixes:run")).Post("/projects/{projectID}/fixes/{fixID}/run", h.runProjectFix)
			r.With(RequireScope("fixes:read")).Get("/projects/{projectID}/fix-runs", h.listFixRuns)
			r.With(RequireScope("fixes:read")).Get("/projects/{projectID}/fix-runs/{runID}", h.getFixRun)
			r.With(RequireScope("fixes:read")).Get("/projects/{projectID}/env-vars", h.listFixEnvVars)
			r.With(RequireScope("fixes:write")).Post("/projects/{projectID}/env-vars", h.setFixEnvVar)
			r.With(RequireScope("fixes:delete")).Delete("/projects/{projectID}/env-vars/{envVarID}", h.deleteFixEnvVar)
			r.With(RequireScope("smtp_profiles:read")).Get("/smtp_profiles", h.listSMTPProfiles)
			r.With(RequireScope("smtp_profiles:write")).Post("/smtp_profiles", h.createSMTPProfile)
		})
	})

	h.mountWebUI(r)
	return r
}

func (h *Handler) mountWebUI(r chi.Router) {
	uiFS := h.resolveUIFS()
	if uiFS == nil {
		return
	}
	fileServer := http.FileServer(http.FS(uiFS))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, req, uiFS, "index.html")
	})
	r.Get("/app.js", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, req, uiFS, "app.js")
	})
	r.Get("/styles.css", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFileFS(w, req, uiFS, "styles.css")
	})
	r.Handle("/web/*", http.StripPrefix("/web/", fileServer))
}

func (h *Handler) resolveUIFS() fs.FS {
	candidates := make([]string, 0, 2)
	if strings.TrimSpace(h.uiDir) != "" {
		candidates = append(candidates, h.uiDir)
	}
	candidates = append(candidates, "internal/assets/web")

	for _, dir := range candidates {
		indexPath := filepath.Join(dir, "index.html")
		if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
			return os.DirFS(dir)
		}
	}

	sub, err := fs.Sub(assets.WebFS, "web")
	if err != nil {
		return nil
	}
	return sub
}

var filenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filenameSanitizer.ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func normalizeEmails(raw []string) []string {
	if raw == nil {
		return []string{}
	}
	res := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(strings.ToLower(item))
		if trimmed == "" {
			continue
		}
		res = append(res, trimmed)
	}
	return res
}

func uptimeWindowConfig(window string) (time.Duration, time.Duration, error) {
	switch window {
	case "1h":
		return 1 * time.Hour, 1 * time.Minute, nil
	case "12h":
		return 12 * time.Hour, 5 * time.Minute, nil
	case "1d":
		return 24 * time.Hour, 15 * time.Minute, nil
	case "7d":
		return 7 * 24 * time.Hour, 1 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, 6 * time.Hour, nil
	default:
		return 0, 0, errors.New("window must be one of: 1h, 12h, 1d, 7d, 30d")
	}
}

func alignToBucket(ts time.Time, bucket time.Duration) time.Time {
	if bucket <= 0 {
		return ts
	}
	return ts.Truncate(bucket)
}

func parseIDParam(r *http.Request, key string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func parseLimit(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	if parsed > 500 {
		return 500
	}
	return parsed
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
