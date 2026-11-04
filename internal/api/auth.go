package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

type authExpiryKey struct{}

func bearer(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}
func (s *Server) user(fn func(http.ResponseWriter, *http.Request, protocol.Member)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var member protocol.Member
		if r.Header.Get("Authorization") != "" {
			m, err := s.db.AuthMember(r.Context(), bearer(r))
			if err != nil {
				fail(w, err)
				return
			}
			member = m
		} else {
			cookie, err := r.Cookie("ac_session")
			if err != nil {
				fail(w, store.ErrUnauthorized)
				return
			}
			auth, err := s.db.AuthSession(r.Context(), cookie.Value)
			if err != nil {
				fail(w, err)
				return
			}
			if r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("Origin") != s.options.Origin || !auth.CheckCSRF(r.Header.Get("X-CSRF-Token"))) {
				fail(w, store.ErrForbidden)
				return
			}
			member = auth.Member
			r = r.WithContext(context.WithValue(r.Context(), authExpiryKey{}, auth.ExpiresAt))
		}
		fn(w, r, member)
	}
}
func (s *Server) device(fn func(http.ResponseWriter, *http.Request, store.Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := s.db.AuthDevice(r.Context(), bearer(r))
		if err != nil {
			fail(w, err)
			return
		}
		s.deviceMu.Lock()
		count := s.deviceInFlight[d.NetworkID]
		if count < 2 {
			s.deviceInFlight[d.NetworkID] = count + 1
		}
		s.deviceMu.Unlock()
		if count >= 2 {
			w.Header().Set("Retry-After", "1")
			fail(w, store.ErrCapacity)
			return
		}
		defer func() {
			s.deviceMu.Lock()
			s.deviceInFlight[d.NetworkID]--
			if s.deviceInFlight[d.NetworkID] == 0 {
				delete(s.deviceInFlight, d.NetworkID)
			}
			s.deviceMu.Unlock()
		}()
		fn(w, r, d)
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.options.Origin {
		fail(w, store.ErrForbidden)
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := readJSON(w, r, &input, 2048); err != nil {
		fail(w, err)
		return
	}
	session, err := s.db.CreateSession(r.Context(), input.Token)
	if err != nil {
		fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ac_session", Value: session.Token, Path: "/api/", Expires: session.ExpiresAt, MaxAge: 86400, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteStrictMode})
	respond(w, 201, map[string]any{"member": session.Member, "csrf": session.CSRF, "expires_at": session.ExpiresAt})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ protocol.Member) {
	if cookie, err := r.Cookie("ac_session"); err == nil {
		if err = s.db.DeleteSession(r.Context(), cookie.Value); err != nil {
			fail(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "ac_session", Value: "", Path: "/api/", MaxAge: -1, HttpOnly: true, Secure: s.options.SecureCookies, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(204)
}
