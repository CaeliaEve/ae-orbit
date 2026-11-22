package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) enrichCPUDetails(network string, view *store.CPUDetailView) {
	for pi := range view.Parts {
		for ii := range view.Parts[pi].Items {
			item := &view.Parts[pi].Items[ii]
			match, _, state, ok := s.catalog.LookupCPUOutput(item.Stack)
			if !ok {
				continue
			}
			public := match.Public(state)
			public.BaseFallback = item.HasTag != nil && *item.HasTag || match.NBT != ""
			if public.BaseFallback {
				public.LocalizedName = item.Label
				public.Tooltips = nil
			}
			if state == "available" {
				public.ImageURL = "/api/v1/networks/" + url.PathEscape(network) + "/catalog/cpu-details/" + url.PathEscape(match.Key) + "/image?v=" + url.QueryEscape(public.ImageKey)
			}
			item.Catalog = &public
		}
	}
}
func (s *Server) cpuDetailRoutes() {
	s.mux.HandleFunc("POST /api/v1/networks/{network}/cpu-details", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.CPUDetailInput
		if err := readJSON(w, r, &in, 1024); err != nil {
			fail(w, err)
			return
		}
		view, err := s.db.RequestCPUDetails(r.Context(), m.ID, r.PathValue("network"), in)
		if err != nil {
			fail(w, err)
			return
		}
		s.enrichCPUDetails(r.PathValue("network"), &view)
		respond(w, 200, view)
	}))
	s.mux.HandleFunc("DELETE /api/v1/networks/{network}/cpu-details/{viewer}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if err := s.db.CloseCPUDetails(r.Context(), m.ID, r.PathValue("network"), r.PathValue("viewer")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/catalog/cpu-details/{asset}/image", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if err := s.db.RequireAccess(r.Context(), m.ID, r.PathValue("network"), false); err != nil {
			fail(w, err)
			return
		}
		rows, err := s.db.Pool().Query(r.Context(), `SELECT items FROM cpu_detail_snapshots p JOIN networks n ON n.id=p.network_id WHERE p.network_id=$1 AND p.session_id=n.current_session AND p.observed_at>now()-interval '2 minutes'`, r.PathValue("network"))
		if err != nil {
			fail(w, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				fail(w, err)
				return
			}
			items, err := decodeCPUDetailItems(raw)
			if err != nil {
				fail(w, err)
				return
			}
			for _, item := range items {
				match, _, state, ok := s.catalog.LookupCPUOutput(item.Stack)
				if ok && state == "available" && match.Key == r.PathValue("asset") && match.ImagePath == r.URL.Query().Get("v") {
					s.serveCatalogImage(w, r, match)
					return
				}
			}
		}
		if err = rows.Err(); err != nil {
			fail(w, err)
			return
		}
		fail(w, store.ErrNotFound)
	}))
}

func decodeCPUDetailItems(raw []byte) ([]protocol.CPUDetailItem, error) {
	var items []protocol.CPUDetailItem
	err := json.Unmarshal(raw, &items)
	return items, err
}
