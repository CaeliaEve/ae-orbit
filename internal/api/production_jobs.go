package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) productionJobRoutes() {
	s.mux.HandleFunc("POST /api/v1/networks/{network}/jobs", s.user(s.createProductionJob))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/jobs", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.ProductionJobs(r.Context(), m.ID, r.PathValue("network"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/jobs/by-key/{key}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		// An explicit owner selector is administrator-only, even for one's own ID.
		if r.URL.Query().Has("owner_id") && !m.Admin {
			fail(w, store.ErrForbidden)
			return
		}
		value, err := s.db.ProductionJobByKey(r.Context(), m.ID, r.PathValue("network"), r.URL.Query().Get("owner_id"), r.PathValue("key"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/jobs/history", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		limit, err := integerQuery(r, "limit")
		if err != nil {
			fail(w, err)
			return
		}
		value, err := s.db.ProductionJobHistory(r.Context(), m.ID, r.PathValue("network"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/jobs/changes", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var after int64
		if text := r.URL.Query().Get("after"); text != "" {
			if len(text) > 1 && text[0] == '0' {
				fail(w, store.ErrInvalid)
				return
			}
			// Unlike integerQuery, job cursors accept only nonnegative decimal digits.
			for _, c := range text {
				if c < '0' || c > '9' {
					fail(w, store.ErrInvalid)
					return
				}
			}
			var err error
			after, err = strconv.ParseInt(text, 10, 64)
			if err != nil {
				fail(w, store.ErrInvalid)
				return
			}
		}
		value, err := s.db.ProductionJobChanges(r.Context(), m.ID, r.PathValue("network"), protocol.Quantity(after))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/jobs/{job}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.ProductionJob(r.Context(), m.ID, r.PathValue("network"), r.PathValue("job"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	// GET by-key and POST controls have disjoint methods, avoiding cross-pattern ambiguity.
	s.mux.HandleFunc("POST /api/v1/networks/{network}/jobs/{job}/pause", s.productionJobControl("pause"))
	s.mux.HandleFunc("POST /api/v1/networks/{network}/jobs/{job}/resume", s.productionJobControl("resume"))
	s.mux.HandleFunc("POST /api/v1/networks/{network}/jobs/{job}/stop", s.productionJobControl("stop"))
}

func (s *Server) productionJobControl(action string) http.HandlerFunc {
	return s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input protocol.ProductionJobControlInput
		if err := readJSON(w, r, &input, 2048); err != nil {
			fail(w, err)
			return
		}
		value, err := s.db.ControlProductionJob(r.Context(), m.ID, r.PathValue("network"), r.PathValue("job"), action, input)
		if err != nil {
			failJob(w, err)
			return
		}
		respond(w, 200, value)
	})
}

func failJob(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrCapacity) {
		w.Header().Set("Retry-After", "1")
	}
	var conflict *store.ProductionJobEntryConflictError
	if errors.As(err, &conflict) {
		respond(w, 409, map[string]any{"error": map[string]any{"code": "entry_already_accepted", "message": "A source entry was already accepted.", "job_ids": conflict.JobIDs}})
		return
	}
	fail(w, err)
}

func (s *Server) createProductionJob(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	var input protocol.ProductionJobInput
	if err := readJSON(w, r, &input, 256*1024); err != nil {
		fail(w, err)
		return
	}
	value, err := s.db.CreateProductionJob(r.Context(), m.ID, r.PathValue("network"), input)
	if err != nil {
		failJob(w, err)
		return
	}
	status := 201
	if value.Replayed {
		status = 200
	}
	respond(w, status, value)
}
