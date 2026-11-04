package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func integerQuery(r *http.Request, key string) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, store.ErrInvalid
	}
	return n, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /oc/{path...}", s.ocFile)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	s.mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.Pool().Ping(r.Context()); err != nil {
			respond(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	s.mux.HandleFunc("POST /api/v1/session", s.login)
	s.mux.HandleFunc("DELETE /api/v1/session", s.user(s.logout))
	s.mux.HandleFunc("GET /api/v1/me", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) { respond(w, 200, m) }))
	s.mux.HandleFunc("GET /api/v1/networks", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.ListNetworks(r.Context(), m.ID)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("POST /api/v1/networks", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct {
			Name        string `json:"name"`
			ServerGroup string `json:"server_group"`
			Source      string `json:"source"`
		}
		if err := readJSON(w, r, &input, 2048); err != nil {
			fail(w, err)
			return
		}
		n, token, err := s.db.CreateNetwork(r.Context(), m.ID, input.Name, input.ServerGroup, input.Source)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 201, map[string]any{"network": n, "device_token": token})
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.Network(r.Context(), m.ID, r.PathValue("network"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("POST /api/v1/networks/{network}/device-token", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct{}
		if err := readJSON(w, r, &input, 1024); err != nil {
			fail(w, err)
			return
		}
		token, err := s.db.RotateDeviceToken(r.Context(), m.ID, r.PathValue("network"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]string{"device_token": token})
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/items", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		limit, err := integerQuery(r, "limit")
		if err != nil {
			fail(w, err)
			return
		}
		q := protocol.ItemQuery{Search: r.URL.Query().Get("q"), Sort: r.URL.Query().Get("sort"), Cursor: r.URL.Query().Get("cursor"), Limit: limit}
		if value := r.URL.Query().Get("craftable"); value != "" {
			yes, err := strconv.ParseBool(value)
			if err != nil {
				fail(w, store.ErrInvalid)
				return
			}
			q.Craftable = &yes
		}
		value, err := s.db.Items(r.Context(), m.ID, r.PathValue("network"), q)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/inventory-stats", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.InventoryStats(r.Context(), m.ID, r.PathValue("network"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/cpus", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.CPUs(r.Context(), m.ID, r.PathValue("network"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("POST /api/v1/networks/{network}/orders", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input protocol.OrderInput
		if err := readJSON(w, r, &input, 2048); err != nil {
			fail(w, err)
			return
		}
		value, err := s.db.CreateOrder(r.Context(), m.ID, r.PathValue("network"), input)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 201, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/orders", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		limit, err := integerQuery(r, "limit")
		if err != nil {
			fail(w, err)
			return
		}
		value, err := s.db.Orders(r.Context(), m.ID, r.PathValue("network"), r.URL.Query().Get("state"), limit)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/orders/{order}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.Order(r.Context(), m.ID, r.PathValue("network"), r.PathValue("order"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/orders/{order}/events", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.OrderHistory(r.Context(), m.ID, r.PathValue("network"), r.PathValue("order"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("POST /api/v1/networks/{network}/orders/{order}/cancel", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.CancelOrder(r.Context(), m.ID, r.PathValue("network"), r.PathValue("order"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("POST /api/v1/networks/{network}/orders/{order}/resolve", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		}
		if err := readJSON(w, r, &input, 2048); err != nil {
			fail(w, err)
			return
		}
		value, err := s.db.ResolveOrder(r.Context(), m.ID, r.PathValue("network"), r.PathValue("order"), input.State, input.Reason)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("GET /api/v1/networks/{network}/events", s.user(s.events))
	s.mux.HandleFunc("GET /api/v1/members", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		value, err := s.db.Members(r.Context(), m.ID)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, value)
	}))
	s.mux.HandleFunc("POST /api/v1/members", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct {
			Name   string           `json:"name"`
			Grants []protocol.Grant `json:"grants"`
		}
		if err := readJSON(w, r, &input, 32768); err != nil {
			fail(w, err)
			return
		}
		value, token, err := s.db.CreateMember(r.Context(), m.ID, input.Name, input.Grants)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 201, map[string]any{"member": value, "token": token})
	}))
	s.mux.HandleFunc("DELETE /api/v1/members/{member}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if err := s.db.RevokeMember(r.Context(), m.ID, r.PathValue("member")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	s.mux.HandleFunc("PUT /api/v1/members/{member}/networks/{network}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct {
			CanCraft bool `json:"can_craft"`
		}
		if err := readJSON(w, r, &input, 1024); err != nil {
			fail(w, err)
			return
		}
		if err := s.db.SetGrant(r.Context(), m.ID, r.PathValue("member"), protocol.Grant{NetworkID: r.PathValue("network"), CanCraft: input.CanCraft}); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	s.mux.HandleFunc("DELETE /api/v1/members/{member}/networks/{network}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if err := s.db.DeleteGrant(r.Context(), m.ID, r.PathValue("member"), r.PathValue("network")); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	s.mux.HandleFunc("GET /api/v1/admin/metrics", s.user(s.metrics))
	s.mux.HandleFunc("POST /bridge/v1/boot-id", s.device(func(w http.ResponseWriter, r *http.Request, _ store.Device) {
		var input struct{}
		if err := readJSON(w, r, &input, 1024); err != nil {
			fail(w, err)
			return
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]string{"boot_id": hex.EncodeToString(nonce[:])})
	}))
	s.mux.HandleFunc("POST /bridge/v1/hello", s.device(func(w http.ResponseWriter, r *http.Request, d store.Device) {
		var value protocol.Hello
		if err := readJSON(w, r, &value, s.db.Options().Limits.BatchBytes); err != nil {
			fail(w, err)
			return
		}
		result, err := s.db.Hello(r.Context(), d, value)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	s.mux.HandleFunc("POST /bridge/v1/inventory", s.device(func(w http.ResponseWriter, r *http.Request, d store.Device) {
		var value protocol.InventoryBatch
		if err := readJSON(w, r, &value, s.db.Options().Limits.BatchBytes); err != nil {
			fail(w, err)
			return
		}
		result, err := s.db.Ingest(r.Context(), d, value)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	s.mux.HandleFunc("POST /bridge/v1/poll", s.device(func(w http.ResponseWriter, r *http.Request, d store.Device) {
		var value protocol.Poll
		if err := readJSON(w, r, &value, s.db.Options().Limits.BatchBytes); err != nil {
			fail(w, err)
			return
		}
		result, err := s.db.Poll(r.Context(), d, value)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	if err := s.db.RequireAdmin(r.Context(), m.ID); err != nil {
		fail(w, err)
		return
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	pool := s.db.Pool().Stat()
	respond(w, 200, map[string]any{"uptime_seconds": int64(time.Since(s.started).Seconds()), "requests": strconv.FormatUint(s.total.Load(), 10), "active_requests": len(s.requests), "active_streams": len(s.streams), "goroutines": runtime.NumGoroutine(), "heap_bytes": strconv.FormatUint(memory.HeapAlloc, 10), "gc_cycles": memory.NumGC, "database_connections": pool.TotalConns(), "database_acquired": pool.AcquiredConns(), "database_acquire_wait_ms": pool.EmptyAcquireWaitTime().Milliseconds()})
}
