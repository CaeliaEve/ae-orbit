// Package api exposes the service without embedding a frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

type Options struct {
	Origin         string
	SecureCookies  bool
	MaxConcurrent  int
	MaxStreams     int
	RequestTimeout time.Duration
	StreamInterval time.Duration
}

func DefaultOptions() Options {
	return Options{Origin: "http://127.0.0.1:8080", MaxConcurrent: 64, MaxStreams: 32, RequestTimeout: 15 * time.Second, StreamInterval: time.Second}
}

type Server struct {
	db             *store.Store
	options        Options
	mux            *http.ServeMux
	requests       chan struct{}
	streams        chan struct{}
	deviceMu       sync.Mutex
	deviceInFlight map[string]int
	total          atomic.Uint64
	started        time.Time
}

func New(db *store.Store, options Options) *Server {
	s := &Server{db: db, options: options, mux: http.NewServeMux(), requests: make(chan struct{}, options.MaxConcurrent), streams: make(chan struct{}, options.MaxStreams), deviceInFlight: map[string]int{}, started: time.Now()}
	s.routes()
	return s
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.total.Add(1)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	stream := r.Method == "GET" && len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "networks" && parts[4] == "events"
	if stream {
		select {
		case s.streams <- struct{}{}:
			defer func() { <-s.streams }()
		default:
			w.Header().Set("Retry-After", "1")
			fail(w, store.ErrCapacity)
			return
		}
	} else {
		select {
		case s.requests <- struct{}{}:
			defer func() { <-s.requests }()
		default:
			w.Header().Set("Retry-After", "1")
			fail(w, store.ErrCapacity)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.options.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(s.options.RequestTimeout + time.Second))
	}
	s.mux.ServeHTTP(w, r)
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

type httpError struct {
	status int
	code   string
}

func (e httpError) Error() string { return e.code }
func fail(w http.ResponseWriter, err error) {
	status, code, message := 500, "internal_error", "The request could not be completed."
	var h httpError
	var pg *pgconn.PgError
	switch {
	case errors.As(err, &h):
		status, code, message = h.status, h.code, h.code
	case errors.Is(err, store.ErrUnauthorized):
		status, code, message = 401, "unauthorized", "Valid credentials are required."
	case errors.Is(err, store.ErrForbidden):
		status, code, message = 403, "forbidden", "This operation is not permitted."
	case errors.Is(err, store.ErrNotFound):
		status, code, message = 404, "not_found", "The resource was not found."
	case errors.Is(err, store.ErrInvalid):
		status, code, message = 400, "invalid_request", "Check the request against the API contract."
	case errors.Is(err, store.ErrSequence):
		status, code, message = 409, "sequence_conflict", "Retry the original pending batch or establish a new boot session."
	case errors.Is(err, store.ErrStaleSession):
		status, code, message = 409, "stale_session", "This device session has been retired."
	case errors.Is(err, store.ErrViewChanged):
		status, code, message = 409, "view_changed", "Refresh the query from its first page."
	case errors.Is(err, store.ErrConflict):
		status, code, message = 409, "conflict", "The request conflicts with the current state."
	case errors.Is(err, store.ErrCapability):
		status, code, message = 409, "capability_unavailable", "The connected device cannot perform this operation."
	case errors.Is(err, store.ErrCapacity):
		status, code, message = 429, "capacity_exceeded", "Reduce the batch or retry after the configured interval."
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = 503, "timeout", "The request exceeded its execution budget."
	case errors.As(err, &pg):
		switch pg.Code {
		case "23505":
			status, code, message = 409, "conflict", "The resource already exists."
		case "23503", "22P02", "22021":
			status, code, message = 400, "invalid_reference", "Check the supplied resource references."
		case "57014", "55P03":
			status, code, message = 503, "busy", "The database is busy; retry later."
		}
	}
	if status >= 500 {
		slog.Error("request failed", "code", code, "error", err)
	}
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func readJSON(w http.ResponseWriter, r *http.Request, target any, limit int) error {
	if r.ContentLength > int64(limit) {
		return httpError{413, "body_too_large"}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" {
		return httpError{415, "json_required"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(limit))
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(target)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			if trailing == nil {
				err = store.ErrInvalid
			} else {
				err = trailing
			}
		}
	}
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		return httpError{413, "body_too_large"}
	}
	if err != nil {
		return store.ErrInvalid
	}
	return nil
}
