package api

import (
	"net/http"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) monitorDevice(fn func(http.ResponseWriter, *http.Request, store.MonitorAuth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.db.AuthMonitor(r.Context(), bearer(r))
		if err != nil {
			fail(w, err)
			return
		}
		key := "monitor:" + a.ID
		s.deviceMu.Lock()
		if s.deviceInFlight[key] >= 2 {
			s.deviceMu.Unlock()
			fail(w, store.ErrCapacity)
			return
		}
		s.deviceInFlight[key]++
		s.deviceMu.Unlock()
		defer func() {
			s.deviceMu.Lock()
			s.deviceInFlight[key]--
			if s.deviceInFlight[key] == 0 {
				delete(s.deviceInFlight, key)
			}
			s.deviceMu.Unlock()
		}()
		fn(w, r, a)
	}
}
func (s *Server) monitoringRoutes() {
	s.mux.HandleFunc("GET /api/v1/monitoring/groups", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.MonitoringGroups(r.Context(), m.ID)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("GET /api/v1/monitoring/groups/{group}/plugins", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.MonitoringPlugins(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("PUT /api/v1/monitoring/groups/{group}/plugins/{plugin}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct {
			Enabled  bool             `json:"enabled"`
			Settings plugins.Settings `json:"settings"`
			Revision int64            `json:"revision"`
		}
		if e := readJSON(w, r, &in, 4096); e != nil {
			fail(w, e)
			return
		}
		e := s.db.SetMonitoringPlugin(r.Context(), m.ID, r.PathValue("group"), r.PathValue("plugin"), in.Enabled, in.Settings, in.Revision)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET /api/v1/monitoring/groups/{group}/collectors", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.MonitorCollectors(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /api/v1/monitoring/groups/{group}/collectors", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct {
			Name string `json:"name"`
		}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		v, token, e := s.db.CreateMonitorCollector(r.Context(), m.ID, r.PathValue("group"), in.Name)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 201, map[string]any{"collector": v, "token": token})
	}))
	s.mux.HandleFunc("POST /api/v1/monitoring/groups/{group}/collectors/{collector}/token", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct{}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		token, e := s.db.ChangeMonitorToken(r.Context(), m.ID, r.PathValue("group"), r.PathValue("collector"), false)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]string{"token": token})
	}))
	s.mux.HandleFunc("DELETE /api/v1/monitoring/groups/{group}/collectors/{collector}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		_, e := s.db.ChangeMonitorToken(r.Context(), m.ID, r.PathValue("group"), r.PathValue("collector"), true)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET /api/v1/monitoring/groups/{group}/objects", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.MonitorObjects(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	set := func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.MonitorObjectInput
		if e := readJSON(w, r, &in, 2048); e != nil {
			fail(w, e)
			return
		}
		id, e := s.db.SetMonitorObject(r.Context(), m.ID, r.PathValue("group"), r.PathValue("object"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]string{"id": id})
	}
	s.mux.HandleFunc("POST /api/v1/monitoring/groups/{group}/objects", s.user(set))
	s.mux.HandleFunc("PUT /api/v1/monitoring/groups/{group}/objects/{object}", s.user(set))
	s.mux.HandleFunc("GET /api/v1/monitoring/groups/{group}/objects/{object}/history", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		to := time.Now().UTC()
		from := to.Add(-time.Hour)
		var e error
		if v := r.URL.Query().Get("from"); v != "" {
			from, e = time.Parse(time.RFC3339, v)
			if e != nil {
				fail(w, store.ErrInvalid)
				return
			}
		}
		if v := r.URL.Query().Get("to"); v != "" {
			to, e = time.Parse(time.RFC3339, v)
			if e != nil {
				fail(w, store.ErrInvalid)
				return
			}
		}
		v, e := s.db.MonitorHistory(r.Context(), m.ID, r.PathValue("group"), r.PathValue("object"), from, to)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("GET /api/v1/monitoring/groups/{group}/alerts", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.MonitorAlerts(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /api/v1/monitoring/groups/{group}/alerts/{alert}/acknowledge", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct{}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.AcknowledgeMonitorAlert(r.Context(), m.ID, r.PathValue("group"), r.PathValue("alert")); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("POST /bridge/monitor/hello", s.monitorDevice(func(w http.ResponseWriter, r *http.Request, a store.MonitorAuth) {
		var in store.MonitorHello
		if e := readJSON(w, r, &in, 8192); e != nil {
			fail(w, e)
			return
		}
		v, e := s.db.MonitorHello(r.Context(), a, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /bridge/monitor/poll", s.monitorDevice(func(w http.ResponseWriter, r *http.Request, a store.MonitorAuth) {
		var in store.MonitorPoll
		if e := readJSON(w, r, &in, 16384); e != nil {
			fail(w, e)
			return
		}
		v, e := s.db.MonitorPoll(r.Context(), a, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
}
