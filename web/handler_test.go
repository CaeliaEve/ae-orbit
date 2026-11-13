package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendBoundaries(t *testing.T) {
	h := Handler()
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/", 200},
		{"/app.js", 200},
		{"/style.css", 200},
		{"/compact.css", 200},
		{"/assets/brand/ae-orbit-mark.png", 200},
		{"/assets/brand/ae-orbit-mark.svg", 200},
		{"/missing.js", 404},
		{"/api/v1/not-a-route", 404},
		{"/assets/", 404},
		{"/assets/nei/audit.json", 404},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.code {
				t.Fatalf("%s: status %d, want %d", tc.path, w.Code, tc.code)
			}
			if tc.code == http.StatusOK && !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing frontend CSP")
			}
		})
	}
}
