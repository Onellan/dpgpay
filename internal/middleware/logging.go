package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type requestIDKey string

const reqIDKey requestIDKey = "request_id"

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := randomRequestID()
		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(withRequestID(r.Context(), requestID))
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		payload := map[string]any{
			"ts":          time.Now().UTC().Format(time.RFC3339Nano),
			"request_id":  requestID,
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      sw.status,
			"duration_ms": time.Since(start).Milliseconds(),
			"remote_addr": r.RemoteAddr,
			"user_agent":  r.UserAgent(),
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			log.Printf("{\"ts\":%q,\"event\":\"request_log_encode_failed\",\"error\":%q}", time.Now().UTC().Format(time.RFC3339Nano), err.Error())
			return
		}
		log.Print(string(encoded))
	})
}

func withRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, reqIDKey, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(reqIDKey).(string)
	return v
}

func randomRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}
