package api

import (
	"net/http"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) integration(scope string, fn func(http.ResponseWriter, *http.Request, store.IntegrationAuth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.db.AuthIntegration(r.Context(), bearer(r))
		if err != nil {
			fail(w, err)
			return
		}
		if err = s.db.RequireIntegration(r.Context(), a, scope, r.PathValue("network"), r.PathValue("group")); err != nil {
			fail(w, err)
			return
		}
		key := "integration:" + a.Key.ID
		s.deviceMu.Lock()
		count := s.deviceInFlight[key]
		if count < 4 {
			s.deviceInFlight[key] = count + 1
		}
		s.deviceMu.Unlock()
		if count >= 4 {
			w.Header().Set("Retry-After", "1")
			fail(w, store.ErrCapacity)
			return
		}
		defer func() {
			s.deviceMu.Lock()
			s.deviceInFlight[key]--
			if s.deviceInFlight[key] == 0 {
				delete(s.deviceInFlight, key)
			}
			s.deviceMu.Unlock()
		}()
		if err = s.db.TouchIntegration(r.Context(), a); err != nil {
			fail(w, err)
			return
		}
		fn(w, r, a)
	}
}
func (s *Server) integrationRoutes() {
	s.mux.HandleFunc("GET /api/v1/integrations", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		keys, e := s.db.IntegrationKeys(r.Context(), m.ID)
		if e != nil {
			fail(w, e)
			return
		}
		networks, e := s.db.ListNetworks(r.Context(), m.ID)
		if e != nil {
			fail(w, e)
			return
		}
		groups, e := s.db.MonitoringGroups(r.Context(), m.ID)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]any{"keys": keys, "networks": networks, "server_groups": groups})
	}))
	s.mux.HandleFunc("POST /api/v1/integrations", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.IntegrationKeyInput
		if e := readJSON(w, r, &in, 32768); e != nil {
			fail(w, e)
			return
		}
		k, token, e := s.db.CreateIntegrationKey(r.Context(), m.ID, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 201, map[string]any{"key": k, "token": token})
	}))
	s.mux.HandleFunc("PUT /api/v1/integrations/{key}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.IntegrationKeyInput
		if e := readJSON(w, r, &in, 32768); e != nil {
			fail(w, e)
			return
		}
		k, e := s.db.UpdateIntegrationKey(r.Context(), m.ID, r.PathValue("key"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, k)
	}))
	s.mux.HandleFunc("POST /api/v1/integrations/{key}/token", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct {
			Revision int64 `json:"revision"`
		}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		k, token, e := s.db.RotateIntegrationKey(r.Context(), m.ID, r.PathValue("key"), in.Revision)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]any{"key": k, "token": token})
	}))
	s.mux.HandleFunc("GET /api/integration/v1/check", s.integration("", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		networks, e := s.db.IntegrationNetworks(r.Context(), a)
		if e != nil {
			fail(w, e)
			return
		}
		groups, e := s.db.IntegrationGroups(r.Context(), a)
		if e != nil {
			fail(w, e)
			return
		}
		ids := []string{}
		for _, n := range networks {
			ids = append(ids, n.ID)
		}
		respond(w, 200, map[string]any{"status": "ok", "name": a.Key.Name, "scopes": a.Key.Scopes, "network_ids": ids, "server_groups": groups, "expires_at": a.Key.ExpiresAt, "read_only": true, "source": "cached_observations"})
	}))
	s.mux.HandleFunc("GET /api/integration/v1/networks", s.integration("", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		v, e := s.db.IntegrationNetworks(r.Context(), a)
		if e != nil {
			fail(w, e)
			return
		}
		rows := []map[string]any{}
		for _, n := range v {
			rows = append(rows, map[string]any{"id": n.ID, "name": n.Name, "server_group": n.ServerGroup, "online": n.Online, "observed_at": n.LastInventory})
		}
		respond(w, 200, rows)
	}))
	s.mux.HandleFunc("GET /api/integration/v1/networks/{network}/items", s.integration("inventory.read", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		limit, e := integerQuery(r, "limit")
		if e != nil {
			fail(w, e)
			return
		}
		q := protocol.ItemQuery{Kind: r.URL.Query().Get("kind"), Search: r.URL.Query().Get("q"), Cursor: r.URL.Query().Get("cursor"), Limit: limit, Sort: "name", Consistency: "window", View: "observed"}
		v, e := s.db.Items(r.Context(), a.MemberID, r.PathValue("network"), q)
		if e != nil {
			fail(w, e)
			return
		}
		n, e := s.db.Network(r.Context(), a.MemberID, r.PathValue("network"))
		if e != nil {
			fail(w, e)
			return
		}
		s.enrichCatalog(n.ID, v.Items)
		rows := []map[string]any{}
		now := time.Now()
		for _, i := range v.Items {
			known := i.QuantityKnown == nil || *i.QuantityKnown
			current := known && i.CurrentSession && n.Online && i.ObservedAt.After(now.Add(-s.db.Options().RecheckAfter))
			var quantity any
			if current {
				quantity = i.Quantity
			}
			unit := "件"
			if i.Kind == "fluid" {
				unit = "mB"
			} else if i.Kind == "essentia" {
				unit = "点"
			}
			status := "current"
			if !known {
				status = "waiting"
			} else if !n.Online {
				status = "offline"
			} else if !current {
				status = "stale"
			}
			var last any
			var observed any
			if known {
				last = i.Quantity
				observed = i.ObservedAt
			}
			rows = append(rows, map[string]any{"id": i.ID, "name": i.Name, "label": i.Label, "kind": i.Kind, "damage": i.Damage, "has_tag": i.NBT != "", "quantity": quantity, "last_quantity": last, "unit": unit, "known": current, "status": status, "observed_at": observed, "image_key": catalogImageKey(i.Catalog)})
		}
		respond(w, 200, map[string]any{"items": rows, "next_cursor": v.NextCursor, "revision": v.Revision, "source": "cached_observations", "stale_after_seconds": int(s.db.Options().RecheckAfter.Seconds())})
	}))
	s.mux.HandleFunc("GET /api/integration/v1/networks/{network}/cpus", s.integration("cpu.read", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		v, e := s.db.CPUs(r.Context(), a.MemberID, r.PathValue("network"))
		if e != nil {
			fail(w, e)
			return
		}
		n, e := s.db.Network(r.Context(), a.MemberID, r.PathValue("network"))
		if e != nil {
			fail(w, e)
			return
		}
		s.enrichCPUOutputs(n.ID, v.CPUs)
		known := n.Online && v.SessionID == n.SessionID && v.ObservedAt != nil && v.ObservedAt.After(time.Now().Add(-s.db.Options().OfflineAfter))
		respond(w, 200, map[string]any{"cpus": v.CPUs, "known": known, "observed_at": v.ObservedAt, "truncated": v.Truncated, "source": "cached_observations"})
	}))
	s.mux.HandleFunc("GET /api/integration/v1/groups/{group}/energy", s.integration("energy.read", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		v, e := s.db.MonitorObjects(r.Context(), a.MemberID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		rows := []map[string]any{}
		for _, o := range v {
			if o.PluginID != "gtnh.lsc" {
				continue
			}
			var stored, capacity any
			if o.Status == "online" {
				stored = o.Stored
				capacity = o.Capacity
			}
			rows = append(rows, map[string]any{"id": o.ID, "name": o.Name, "status": o.Status, "known": o.Status == "online", "stored_eu": stored, "capacity_eu": capacity, "last_stored_eu": o.Stored, "last_capacity_eu": o.Capacity, "percent": o.Percent, "observed_at": o.ObservedAt, "flow": o.Flow, "flow_error": o.FlowError})
		}
		respond(w, 200, map[string]any{"objects": rows, "source": "cached_observations"})
	}))
	s.mux.HandleFunc("GET /api/integration/v1/groups/{group}/subnets", s.integration("subnet.read", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		v, e := s.db.Subnets(r.Context(), a.MemberID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		rows := []map[string]any{}
		for _, n := range v {
			rows = append(rows, map[string]any{"id": n.ID, "name": n.Name, "online": n.Online, "enabled": n.Enabled, "item_count": n.Count})
		}
		respond(w, 200, rows)
	}))
	s.mux.HandleFunc("GET /api/integration/v1/groups/{group}/subnets/{subnet}/items", s.integration("subnet.read", func(w http.ResponseWriter, r *http.Request, a store.IntegrationAuth) {
		v, e := s.db.SubnetView(r.Context(), a.MemberID, r.PathValue("group"), r.PathValue("subnet"))
		if e != nil {
			fail(w, e)
			return
		}
		rows := []map[string]any{}
		for _, i := range v.Items {
			var quantity any
			if i.Known {
				quantity = i.Quantity
			}
			unit := "件"
			if i.Kind == "fluid" {
				unit = "mB"
			}
			rows = append(rows, map[string]any{"id": i.ID, "name": i.Name, "label": i.Label, "kind": i.Kind, "damage": i.Damage, "has_tag": i.NBT != "", "quantity": quantity, "last_quantity": i.Quantity, "unit": unit, "known": i.Known, "observed_at": i.Observed, "keywords": i.Keywords, "image_key": catalogImageKey(i.Catalog)})
		}
		respond(w, 200, map[string]any{"subnet_id": v.Subnet.ID, "name": v.Subnet.Name, "items": rows, "source": "cached_observations"})
	}))
}
func catalogImageKey(c *protocol.CatalogItem) string {
	if c == nil {
		return ""
	}
	return c.ImageKey
}
