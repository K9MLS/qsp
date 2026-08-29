package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/logging"
)

type contextKey int

const requestIDKey contextKey = iota

// RequestIDFromContext returns the correlation ID assigned to a request, or the
// empty string if there is none.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// middleware wraps a handler.
type middleware func(http.Handler) http.Handler

// chain applies middleware so that the first argument is the outermost wrapper.
func chain(h http.Handler, mw ...middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// newRequestID returns a random correlation identifier.
//
// It is not a security token and carries no authority; it exists so an operator
// can tie a log line to a request.
func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// A correlation ID is not worth failing a request over. Falling back to
		// a timestamp keeps requests traceable even if the entropy source
		// misbehaves.
		return "t" + hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return hex.EncodeToString(b)
}

// withRequestID assigns a correlation ID and echoes it in the response.
func withRequestID() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := newRequestID()
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		})
	}
}

// statusRecorder captures the status code and response size for logging.
//
// It implements http.Flusher because Server-Sent Events require flushing, and a
// wrapper that silently dropped that capability would break streaming.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.written += int64(n)
	return n, err
}

// Flush implements http.Flusher, delegating when the underlying writer supports it.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withLogging records one line per completed request.
func withLogging(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case status >= 500:
				level = slog.LevelError
			case status >= 400:
				level = slog.LevelWarn
			case polled(r.URL.Path):
				// A page that polls produces one line every few seconds, per
				// open browser. One member watching the join page overnight is
				// roughly 28,000 lines; a club's worth during a net would
				// rotate away the evidence an operator actually needs, which on
				// a fourteen-day soak is the whole record.
				//
				// A successful poll carries no information. A failing one still
				// logs at warning or error, which is the case worth seeing.
				level = slog.LevelDebug
			}

			log.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("bytes", rec.written),
				slog.Duration("duration", time.Since(start)),
				logging.RequestID(RequestIDFromContext(r.Context())),
			)
		})
	}
}

// polled reports whether a path is fetched repeatedly by a page left open.
//
// These are logged at debug when they succeed. Everything else, and any of
// these that fails, is logged normally.
func polled(path string) bool {
	switch path {
	case "/api/join", "/api/peers", "/api/events", "/api/session", "/healthz", "/readyz":
		return true
	default:
		return false
	}
}

// withRecovery converts a panicking handler into a 500 rather than letting it
// terminate the process.
//
// A panic is a defect, and the log line says so; the point is that one broken
// console endpoint must not take down a bridge carrying live traffic.
func withRecovery(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler is the documented way to abort a
					// response; it is not a defect and must propagate.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					log.LogAttrs(r.Context(), slog.LevelError, "handler panicked",
						slog.Any("panic", rec),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						logging.RequestID(RequestIDFromContext(r.Context())),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// withSecurityHeaders applies conservative response headers.
//
// The content security policy forbids inline script and remote origins. The
// console is built to satisfy it: assets are served from this origin and no
// handler emits inline JavaScript.
//
// **One exception, and only one: the map's tile origin.** It is retained for an
// operator who builds their own map against /api/peers, which is what ADR-0025
// now points them at — QSP no longer draws one itself, and a policy that
// forbade the tiles of a map somebody else built would be an odd thing to leave
// behind.
//
// The origin is derived from the configured tile URL rather than opened to
// every host, and clearing the tile URL restores the original policy exactly.
func withSecurityHeaders(tileURL string) middleware {
	csp := "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self' data:" + tileOrigin(tileURL) + "; " +
		"font-src 'self'; " +
		"connect-src 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"object-src 'none'"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the client address for logging and audit.
//
// Forwarding headers are honoured only when the operator has declared that QSP
// runs behind a reverse proxy. Trusting them unconditionally would let any
// client forge its own address in the audit trail.
func clientIP(r *http.Request, behindProxy bool) string {
	if behindProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if first, _, found := strings.Cut(fwd, ","); found {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(fwd)
		}
		if real := r.Header.Get("X-Real-Ip"); real != "" {
			return strings.TrimSpace(real)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tileOrigin returns the scheme and host of a tile URL, prefixed with a space,
// or the empty string when there is nothing to allow.
//
// **Scheme and host only.** A policy naming a path would not match the tiles,
// which vary by zoom and coordinate, and one naming a wildcard would allow
// every host the operator did not choose.
func tileOrigin(tileURL string) string {
	tileURL = strings.TrimSpace(tileURL)
	if tileURL == "" {
		return ""
	}
	// The template holds {z}, {x} and {y}, which are not valid URL characters
	// everywhere they appear. Only the origin is wanted, so the placeholders
	// are replaced with something parseable first.
	replacer := strings.NewReplacer("{z}", "0", "{x}", "0", "{y}", "0")
	u, err := url.Parse(replacer.Replace(tileURL))
	if err != nil || u.Host == "" {
		// An unparseable tile URL allows nothing extra. The map will not draw
		// tiles, which is the same outcome as a tile server that is down, and
		// better than widening the policy on a value nobody could read.
		return ""
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	return " " + u.Scheme + "://" + u.Host
}
