package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestPlayerRegistrationLoginAndProfileHTTP(t *testing.T) {
	f := setup(t)
	invite := decode[struct {
		Code       string                 `json:"code"`
		Invitation store.PlayerInvitation `json:"invitation"`
	}](t, send(t, f, "POST", "/api/v1/player-invitations", adminToken, `{"network_id":"`+f.network.ID+`","can_craft":true,"max_uses":2}`), 201)
	registered := decode[struct {
		Member protocol.Member `json:"member"`
		CSRF   string          `json:"csrf"`
	}](t, send(t, f, "POST", "/api/v1/players/register", "", `{"game_id":"Player_A","password":"a long player password","invitation":"`+invite.Code+`"}`), 201)
	if registered.Member.Admin || registered.Member.GameID != "Player_A" {
		t.Fatal(registered)
	}
	if r := send(t, f, "POST", "/api/v1/session", "", `{"game_id":"player_a","password":"wrong"}`); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	login := send(t, f, "POST", "/api/v1/session", "", `{"game_id":"PLAYER_A","password":"a long player password"}`)
	cookies := login.Cookies()
	decode[map[string]any](t, login, 201)
	if len(cookies) == 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("player session not protected")
	}
	if r := send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`","game_id":"Player_A","password":"a long player password"}`); r.StatusCode != 400 {
		t.Fatal("mixed authentication accepted", r.StatusCode)
	}
	if r := send(t, f, "POST", "/api/v1/players/register", "", `{"game_id":"player_a","password":"a long player password","invitation":"`+invite.Code+`"}`); r.StatusCode != 409 {
		t.Fatal("duplicate ID accepted", r.StatusCode)
	}
	// Legacy admin login stays usable during team migration.
	decode[map[string]any](t, send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`"}`), 201)
}

func TestPlayerCookieIdentityCSRFAndSessionRetirement(t *testing.T) {
	f := setup(t)
	_, code, err := f.db.CreatePlayerInvitation(context.Background(), f.admin.ID, f.network.ID, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.db.RegisterPlayer(context.Background(), "Player_A", "a long player password", code)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.db.RegisterPlayer(context.Background(), "Player_B", "b long player password", code)
	if err != nil {
		t.Fatal(err)
	}
	sendCookie := func(method, path, body, token, csrf, member, origin string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("X-AE-Member", member)
		r.AddCookie(&http.Cookie{Name: "ac_session", Value: token})
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	profile := `{"game_id":"Renamed_B","current_password":"b long player password","password":""}`
	for _, auth := range []struct{ csrf, origin string }{{"wrong", "http://localhost"}, {second.CSRF, "https://other.example"}} {
		decode[map[string]any](t, sendCookie("PUT", "/api/v1/me/player", profile, second.Token, auth.csrf, second.Member.ID, auth.origin), 403)
	}
	// Another tab may have replaced the browser's cookie. Even with the new
	// session's valid CSRF, an old tab cannot act under the new member identity.
	for _, method := range []string{"GET", "PUT"} {
		path := "/api/v1/me"
		if method == "PUT" {
			path = "/api/v1/me/player"
		}
		result := decode[struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}](t, sendCookie(method, path, profile, second.Token, second.CSRF, first.Member.ID, "http://localhost"), 409)
		if result.Error.Code != "identity_changed" {
			t.Fatal(result)
		}
	}
	decode[map[string]any](t, sendCookie("GET", "/api/v1/player-invitations", "", second.Token, "", second.Member.ID, ""), 403)
	oldSession, err := f.db.PlayerLogin(context.Background(), "Player_B", "b long player password")
	if err != nil {
		t.Fatal(err)
	}
	response := sendCookie("PUT", "/api/v1/me/player", profile, second.Token, second.CSRF, second.Member.ID, "http://localhost")
	cookies := response.Cookies()
	updated := decode[struct {
		Member protocol.Member `json:"member"`
		CSRF   string          `json:"csrf"`
	}](t, response, 201)
	if updated.Member.ID != second.Member.ID || updated.Member.GameID != "Renamed_B" || len(cookies) != 1 {
		t.Fatal("profile changed stable identity or lost session")
	}
	for _, token := range []string{second.Token, oldSession.Token} {
		decode[map[string]any](t, sendCookie("GET", "/api/v1/me", "", token, "", second.Member.ID, ""), 401)
	}
	decode[protocol.Member](t, sendCookie("GET", "/api/v1/me", "", cookies[0].Value, "", second.Member.ID, ""), 200)
	decode[map[string]any](t, sendCookie("POST", "/api/v1/session", `{"game_id":"Renamed_B","password":"b long player password"}`, "", "", "", "https://other.example"), 403)
}
