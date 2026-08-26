package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Logger is a middleware that logs request details.
//
// Deprecated: use RequestLogger instead. This one logs no request_id, so its
// lines cannot be correlated across the gateway hop, and it is the
// echo.WrapMiddleware-wrapped stdlib shape whose responseWriter has to
// hand-implement Flush/Unwrap to keep the SSE endpoints alive. Running both
// emits two access lines per request. It stays exported only until every
// service has migrated its wiring.
func Logger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap ResponseWriter to capture status code
			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(ww, r)

			logger.Info("HTTP Request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.status),
				zap.Duration("latency", time.Since(start)),
				zap.String("user_agent", r.UserAgent()),
				zap.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
// This is required for compatibility with httputil.ReverseProxy's streaming flush behavior.
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter, allowing middleware chains
// to discover interfaces (like http.Flusher) on the original writer.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}
