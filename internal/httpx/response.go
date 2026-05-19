package httpx

import (
	"encoding/json"
	"io"
	"net/http"
)

// Error is the uniform error envelope (DESIGN §1/§3.6).
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Fail writes the uniform error envelope. The request id is sourced from the
// response header set by the RequestID middleware (zero handler-side change).
func Fail(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, Error{Code: code, Message: msg, RequestID: w.Header().Get(RequestIDHeader)})
}

func DecodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}
