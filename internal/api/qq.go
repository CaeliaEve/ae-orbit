package api

import (
	"net/http"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) qqBridge(fn func(http.ResponseWriter, *http.Request, store.QQAuth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.db.AuthQQ(r.Context(), bearer(r))
		if err != nil {
			fail(w, err)
			return
		}
		fn(w, r, a)
	}
}
func (s *Server) qqRoutes() {
	const group = "/api/v1/monitoring/groups/{group}/qq"
	s.mux.HandleFunc("GET /bridge/qq/v1/inventory/keywords", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		keys, e := s.db.QQInventoryKeywords(r.Context(), a)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]any{"keywords": keys})
	}))
	s.mux.HandleFunc("GET /bridge/qq/v1/inventory", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		v, e := s.db.QQInventory(r.Context(), a, r.URL.Query().Get("group_id"), r.URL.Query().Get("keyword"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /bridge/qq/v1/inventory", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in store.QQInventoryRequest
		if e := readJSON(w, r, &in, 2*1024*1024); e != nil {
			fail(w, e)
			return
		}
		queued, e := s.db.QQRequestInventory(r.Context(), a, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 202, map[string]bool{"queued": queued})
	}))
	s.mux.HandleFunc("GET "+group, s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.QQSettings(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("PUT "+group, s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.QQSettings
		if e := readJSON(w, r, &in, 16384); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.SetQQSettings(r.Context(), m.ID, r.PathValue("group"), in); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("POST "+group+"/token", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in struct{}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		token, e := s.db.QQToken(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, map[string]string{"token": token})
	}))
	for _, method := range []string{"GET", "POST"} {
		s.mux.HandleFunc(method+" "+group+"/groups/{qqgroup}/members", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
			if r.Method == "POST" {
				var in struct{}
				if e := readJSON(w, r, &in, 1024); e != nil {
					fail(w, e)
					return
				}
			}
			v, e := s.db.QQMembers(r.Context(), m.ID, r.PathValue("group"), r.PathValue("qqgroup"), r.Method == "POST")
			if e != nil {
				fail(w, e)
				return
			}
			respond(w, 200, v)
		}))
	}
	s.mux.HandleFunc("GET "+group+"/deliveries", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.QQDeliveries(r.Context(), m.ID, r.PathValue("group"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	const network = "/api/v1/networks/{network}/qq-reminders"
	s.mux.HandleFunc("POST "+network+"/targets", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.QQCPUTargetInput
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		v, e := s.db.PrepareQQCPUTarget(r.Context(), m.ID, r.PathValue("network"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 201, v)
	}))
	s.mux.HandleFunc("GET "+network+"/targets/{target}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.QQCPUTarget(r.Context(), m.ID, r.PathValue("network"), r.PathValue("target"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("GET "+network, s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		v, e := s.db.QQReminders(r.Context(), m.ID, r.PathValue("network"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST "+network, s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in store.QQReminderInput
		if e := readJSON(w, r, &in, 2048); e != nil {
			fail(w, e)
			return
		}
		id, e := s.db.AddQQCPUReminder(r.Context(), m.ID, r.PathValue("network"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 201, map[string]string{"id": id})
	}))
	s.mux.HandleFunc("PUT "+network+"/{reminder}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var in protocol.QQNotification
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.EditQQReminder(r.Context(), m.ID, r.PathValue("network"), r.PathValue("reminder"), &in); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("DELETE "+network+"/{reminder}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if e := s.db.EditQQReminder(r.Context(), m.ID, r.PathValue("network"), r.PathValue("reminder"), nil); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET /api/v1/plugins", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) { respond(w, 200, plugins.Catalog()) }))
	s.mux.HandleFunc("POST /bridge/qq/v1/poll", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in store.QQBridgePoll
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		v, e := s.db.QQBridgePoll(r.Context(), a, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("PUT /bridge/qq/v1/channels", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in store.QQChannelSync
		if err := readJSON(w, r, &in, 16384); err != nil {
			fail(w, err)
			return
		}
		if err := s.db.QQSyncChannels(r.Context(), a, in); err != nil {
			fail(w, err)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("GET /bridge/qq/v1/power", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		v, err := s.db.QQPower(r.Context(), a, r.URL.Query().Get("group_id"), r.URL.Query().Get("device"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, v)
	}))
	s.mux.HandleFunc("POST /bridge/qq/v1/power", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in store.QQPowerRequest
		if err := readJSON(w, r, &in, 2048); err != nil {
			fail(w, err)
			return
		}
		if err := s.db.QQRequestPower(r.Context(), a, in); err != nil {
			fail(w, err)
			return
		}
		respond(w, 202, map[string]bool{"queued": true})
	}))
	s.mux.HandleFunc("PUT /bridge/qq/v1/groups/{qqgroup}", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in store.QQGroupSync
		if e := readJSON(w, r, &in, 2*1024*1024); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.QQSyncGroup(r.Context(), a, r.PathValue("qqgroup"), in); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("POST /bridge/qq/v1/deliveries/{delivery}/begin", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in struct {
			AttemptID string `json:"attempt_id"`
		}
		if e := readJSON(w, r, &in, 1024); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.QQBeginDelivery(r.Context(), a, r.PathValue("delivery"), in.AttemptID); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("POST /bridge/qq/v1/deliveries/{delivery}/receipt", s.qqBridge(func(w http.ResponseWriter, r *http.Request, a store.QQAuth) {
		var in store.QQReceipt
		if e := readJSON(w, r, &in, 2048); e != nil {
			fail(w, e)
			return
		}
		if e := s.db.QQDeliveryReceipt(r.Context(), a, r.PathValue("delivery"), in); e != nil {
			fail(w, e)
			return
		}
		respond(w, 204, nil)
	}))
}
