package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/velox0/kraken/internal/db"
)

type uptimeResponse struct {
	Window      string           `json:"window"`
	BucketSec   int              `json:"bucket_sec"`
	Start       time.Time        `json:"start"`
	End         time.Time        `json:"end"`
	UptimeRatio float64          `json:"uptime_ratio"`
	Points      []db.UptimePoint `json:"points"`
}

func (h *Handler) getProjectUptime(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	windowName := strings.TrimSpace(r.URL.Query().Get("window"))
	if windowName == "" {
		windowName = "1h"
	}

	windowDur, bucketDur, err := uptimeWindowConfig(windowName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	now := time.Now().UTC()
	end := alignToBucket(now, bucketDur)
	if !end.After(now) {
		end = end.Add(bucketDur)
	}
	start := end.Add(-windowDur)

	points, err := h.store.GetUptimeSeries(r.Context(), projectID, start, end, bucketDur)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	totalUp := 0
	totalKnown := 0
	for _, p := range points {
		totalUp += p.UpSeconds
		totalKnown += p.UpSeconds + p.DownSeconds
	}
	ratio := 0.0
	if totalKnown > 0 {
		ratio = float64(totalUp) / float64(totalKnown)
	}

	writeJSON(w, http.StatusOK, uptimeResponse{
		Window:      windowName,
		BucketSec:   int(bucketDur.Seconds()),
		Start:       start,
		End:         end,
		UptimeRatio: ratio,
		Points:      points,
	})
}
