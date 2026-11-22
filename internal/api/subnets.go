package api

import (
	"net/http"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) subnetRoutes() {
	const base = "/api/v1/monitoring/groups/{group}/subnets"
	s.mux.HandleFunc("GET "+base+"/plugin", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.SubnetPlugin(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("PUT "+base+"/plugin", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct {
			Enabled  bool  `json:"enabled"`
			Revision int64 `json:"revision"`
		}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.SetSubnetPlugin(r.Context(), m.ID, r.PathValue("group"), in.Enabled, in.Revision); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET "+base+"/catalog", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.SearchSubnetCatalog(r.Context(), m.ID, r.PathValue("group"), r.URL.Query().Get("kind"), r.URL.Query().Get("q"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("GET "+base, s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.Subnets(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST "+base, s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.SubnetInput
		if e := readJSON(w, r, &in, 2048); e != nil {
			fail(w, e)
			return
		}
		id, token, e := s.db.CreateSubnet(r.Context(), m.ID, r.PathValue("group"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 201, map[string]string{"id": id, "token": token})
	}))
	s.mux.HandleFunc("GET "+base+"/{subnet}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.SubnetView(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("PUT "+base+"/{subnet}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.SubnetInput
		if e := readJSON(w, r, &in, 2048); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.UpdateSubnet(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"), in); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("POST "+base+"/{subnet}/items", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct {
			ID string `json:"item_id"`
		}
		if e := readJSON(w, r, &in, 512); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.AddSubnetItem(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"), in.ID); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("DELETE "+base+"/{subnet}/items/{item}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if e := s.db.RemoveSubnetItem(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"), r.PathValue("item")); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("POST "+base+"/{subnet}/token", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		token, e := s.db.ResetSubnetToken(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]string{"token": token})
	}))
	rule := s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.SubnetRuleInput
		if e := readJSON(w, r, &in, 2048); e != nil {
			fail(w, e)
			return
		}
		id, e := s.db.SetSubnetRule(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"), r.PathValue("rule"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]string{"id": id})
	})
	s.mux.HandleFunc("POST "+base+"/{subnet}/rules", rule)
	s.mux.HandleFunc("PUT "+base+"/{subnet}/rules/{rule}", rule)
	s.mux.HandleFunc("DELETE "+base+"/{subnet}/rules/{rule}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if e := s.db.RemoveSubnetRule(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"), r.PathValue("rule")); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET "+base+"/{subnet}/history/{item}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.SubnetHistory(r.Context(), m.ID, r.PathValue("group"), r.PathValue("subnet"), r.PathValue("item"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /bridge/subnet/hello", s.monitorDevice(func(w http.ResponseWriter, r *http.Request, a store.MonitorAuth) {
		var in store.SubnetHello
		if e := readJSON(w, r, &in, 4096); e != nil {
			fail(w, e)
			return
		}
		v, e := s.db.SubnetHello(r.Context(), a, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /bridge/subnet/poll", s.monitorDevice(func(w http.ResponseWriter, r *http.Request, a store.MonitorAuth) {
		var in store.SubnetPoll
		if e := readJSON(w, r, &in, 16384); e != nil {
			fail(w, e)
			return
		}
		v, e := s.db.SubnetPoll(r.Context(), a, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("GET /api/v1/subnet-catalog/{item}/image", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		groups, e := s.db.MonitoringGroups(r.Context(), m.ID)
		if e != nil {
			fail(w, e)
			return
		}
		if len(groups) == 0 {
			fail(w, store.ErrForbidden)
			return
		}
		var d protocol.Definition
		if e = s.db.Pool().QueryRow(r.Context(), `SELECT kind,name,damage,nbt,label FROM subnet_catalog WHERE id=$1`, r.PathValue("item")).Scan(&d.Kind, &d.Name, &d.Damage, &d.NBT, &d.Label); e != nil {
			fail(w, store.ErrNotFound)
			return
		}
		match, state, ok := s.catalog.LookupResource(d.Kind, d.Name, d.Damage, d.NBT)
		if !ok && d.NBT != "" {
			match, state, ok = s.catalog.LookupResource(d.Kind, d.Name, d.Damage, "")
		}
		if !ok || state != "available" {
			fail(w, store.ErrNotFound)
			return
		}
		s.serveCatalogImage(w, r, match)
	}))
}
