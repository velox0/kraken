package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/velox0/kraken/internal/logbuf"
)

func (h *Handler) listProjectLogs(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseIDParam(r, "projectID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	logs, err := h.store.ListLogsByProject(r.Context(), projectID, parseLimit(r, 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// streamLogs streams the ring buffer snapshot then all new log entries as SSE.
func (h *Handler) streamLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if h.logBuf != nil {
		// Replay history as "snapshot" events — frontend renders them but does
		// not trigger the blink or increment error count.
		for _, e := range h.logBuf.Snapshot() {
			writeSSEEvent(w, "snapshot", e)
		}
		// Signal end of snapshot so the client knows live streaming has begun.
		_, _ = fmt.Fprintf(w, "event: ready\ndata: {}\n\n")
		flusher.Flush()

		// Subscribe to new entries — sent as "log" events (triggers blink).
		ch := h.logBuf.Subscribe()
		defer h.logBuf.Unsubscribe(ch)

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				writeSSEEvent(w, "log", e)
				flusher.Flush()
			}
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, event string, e logbuf.Entry) {
	b, _ := json.Marshal(e)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
