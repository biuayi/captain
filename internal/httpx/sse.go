package httpx

import (
	"fmt"
	"net/http"
	"time"
)

// ServeSSE streams JSON messages from ch as Server-Sent Events until the
// client disconnects. A heartbeat keeps proxies from idling the connection.
func ServeSSE(w http.ResponseWriter, r *http.Request, ch <-chan []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		Fail(w, http.StatusInternalServerError, "sse_unsupported", "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	beat := time.NewTicker(25 * time.Second)
	defer beat.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
