package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

type loginAttempt struct {
	until time.Time
	count int
}
type loginLimiter struct {
	mu      sync.Mutex
	clients map[string]loginAttempt
	active  chan struct{}
}

func (s *Server) admitPlayerLogin(w http.ResponseWriter, r *http.Request) (func(), bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	s.playerLogin.mu.Lock()
	for key, value := range s.playerLogin.clients {
		if !value.until.After(now) {
			delete(s.playerLogin.clients, key)
		}
	}
	value, exists := s.playerLogin.clients[host]
	if (!exists && len(s.playerLogin.clients) >= 1024) || value.count >= 60 {
		s.playerLogin.mu.Unlock()
		w.Header().Set("Retry-After", "300")
		fail(w, store.ErrCapacity)
		return nil, false
	}
	if !exists {
		value.until = now.Add(5 * time.Minute)
	}
	value.count++
	s.playerLogin.clients[host] = value
	s.playerLogin.mu.Unlock()
	select {
	case s.playerLogin.active <- struct{}{}:
		return func() { <-s.playerLogin.active }, true
	default:
		w.Header().Set("Retry-After", "2")
		fail(w, store.ErrCapacity)
		return nil, false
	}
}

func (s *Server) playerRoutes() {
	s.mux.HandleFunc("POST /api/v1/players/register", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.options.Origin {
			fail(w, store.ErrForbidden)
			return
		}
		var input struct {
			GameID     string `json:"game_id"`
			Password   string `json:"password"`
			Invitation string `json:"invitation"`
		}
		if err := readJSON(w, r, &input, 4096); err != nil {
			fail(w, err)
			return
		}
		done, ok := s.admitPlayerLogin(w, r)
		if !ok {
			return
		}
		defer done()
		session, err := s.db.RegisterPlayer(r.Context(), input.GameID, input.Password, input.Invitation)
		if err != nil {
			fail(w, err)
			return
		}
		s.respondSession(w, session)
	})
	s.mux.HandleFunc("PUT /api/v1/me/player", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct {
			GameID          string `json:"game_id"`
			CurrentPassword string `json:"current_password"`
			Password        string `json:"password"`
		}
		if err := readJSON(w, r, &input, 4096); err != nil {
			fail(w, err)
			return
		}
		done, ok := s.admitPlayerLogin(w, r)
		if !ok {
			return
		}
		defer done()
		member, err := s.db.UpdatePlayerProfile(r.Context(), m.ID, input.GameID, input.CurrentPassword, input.Password, false)
		if err != nil {
			fail(w, err)
			return
		}
		session, err := s.db.SessionForMember(r.Context(), member)
		if err != nil {
			fail(w, err)
			return
		}
		s.respondSession(w, session)
	}))
	s.mux.HandleFunc("GET /api/v1/player-invitations", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		invites, err := s.db.PlayerInvitations(r.Context(), m.ID)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, invites)
	}))
	s.mux.HandleFunc("POST /api/v1/player-invitations", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		var input struct {
			NetworkID string `json:"network_id"`
			CanCraft  bool   `json:"can_craft"`
			MaxUses   int    `json:"max_uses"`
		}
		if err := readJSON(w, r, &input, 2048); err != nil {
			fail(w, err)
			return
		}
		invite, code, err := s.db.CreatePlayerInvitation(r.Context(), m.ID, input.NetworkID, input.CanCraft, input.MaxUses)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 201, map[string]any{"invitation": invite, "code": code})
	}))
	s.mux.HandleFunc("DELETE /api/v1/player-invitations/{invitation}", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if err := s.db.RevokePlayerInvitation(r.Context(), m.ID, r.PathValue("invitation")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 204, nil)
	}))
	s.mux.HandleFunc("PUT /api/v1/members/{member}/player", s.user(func(w http.ResponseWriter, r *http.Request, m protocol.Member) {
		if err := s.db.RequireAdmin(r.Context(), m.ID); err != nil {
			fail(w, err)
			return
		}
		var input struct {
			GameID   string `json:"game_id"`
			Password string `json:"password"`
		}
		if err := readJSON(w, r, &input, 4096); err != nil {
			fail(w, err)
			return
		}
		done, ok := s.admitPlayerLogin(w, r)
		if !ok {
			return
		}
		defer done()
		member, err := s.db.UpdatePlayerProfile(r.Context(), r.PathValue("member"), input.GameID, "", input.Password, true)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, member)
	}))
}
