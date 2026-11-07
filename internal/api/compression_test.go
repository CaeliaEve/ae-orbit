package api_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func seedInventory(t *testing.T, f fixture) {
	t.Helper()
	ctx := context.Background()
	d, err := f.db.AuthDevice(ctx, f.device)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.db.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: "compression-boot", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true}})
	if err != nil {
		t.Fatal(err)
	}
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1}
	for i := 0; i < 40; i++ {
		b.Updates = append(b.Updates, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:item_%02d", i), Label: fmt.Sprintf("测试库存物品 %02d", i), Exact: true}, Quantity: 9007199254740993, Observation: protocol.Quantity(i + 1)})
	}
	if _, err = f.db.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
}

func rawClient(t *testing.T) *http.Client {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func rawRequest(t *testing.T, client *http.Client, f fixture, method, path, token string, encoding []string) (*http.Response, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	for _, value := range encoding {
		r.Header.Add("Accept-Encoding", value)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

func TestInventoryGzipNegotiationAndExactRoundTrip(t *testing.T) {
	f := setup(t)
	seedInventory(t, f)
	client := rawClient(t)
	path := "/api/v1/networks/" + f.network.ID + "/items?limit=100"
	plainResponse, plain := rawRequest(t, client, f, "GET", path, adminToken, []string{"identity"})
	if plainResponse.StatusCode != 200 || !bytes.Contains(plain, []byte(`"quantity":"9007199254740993"`)) {
		t.Fatalf("invalid precision fixture: status=%d body=%s", plainResponse.StatusCode, plain)
	}
	for _, tc := range []struct {
		name     string
		encoding []string
		gzip     bool
	}{
		{"absent", nil, false},
		{"gzip", []string{"gzip"}, true},
		{"quality", []string{"br, gzip;q=0.5"}, true},
		{"case and whitespace", []string{"GZip ; q=1.000"}, true},
		{"wildcard", []string{"*;q=0.5"}, true},
		{"multiple fields", []string{"br", "gzip"}, true},
		{"explicit refusal", []string{"gzip;q=0, *;q=1"}, false},
		{"explicit allowance", []string{"gzip;q=0.8, *;q=0"}, true},
		{"unsupported", []string{"br"}, false},
		{"invalid quality", []string{"gzip;q=NaN"}, false},
		{"out of range", []string{"gzip;q=1.1"}, false},
		{"malformed decimal", []string{"gzip;q=0.1234"}, false},
		{"duplicate refusal", []string{"gzip;q=1", "gzip;q=0"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, body := rawRequest(t, client, f, "GET", path, adminToken, tc.encoding)
			if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(response.Header.Get("Vary"), "Accept-Encoding") {
				t.Fatalf("invalid response headers: %d %v", response.StatusCode, response.Header)
			}
			compressed := response.Header.Get("Content-Encoding") == "gzip"
			if compressed != tc.gzip {
				t.Fatalf("gzip=%v, want %v", compressed, tc.gzip)
			}
			if compressed {
				if len(body) >= len(plain) {
					t.Fatalf("compression enlarged response: %d >= %d", len(body), len(plain))
				}
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(zr)
				if err != nil {
					t.Fatal(err)
				}
				if err = zr.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(plain, body) {
				t.Fatal("encoding changed the inventory representation")
			}
		})
	}
}

func TestInventoryGzipKeepsSmallHeadAndErrorsPlain(t *testing.T) {
	f := setup(t)
	client := rawClient(t)
	path := "/api/v1/networks/" + f.network.ID + "/items"
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", path, adminToken, 200},
		{"HEAD", path, adminToken, 200},
		{"GET", path, "invalid", 401},
		{"GET", "/api/v1/me", adminToken, 200},
	} {
		response, body := rawRequest(t, client, f, tc.method, tc.path, tc.token, []string{"gzip"})
		if response.StatusCode != tc.status || response.Header.Get("Content-Encoding") != "" {
			t.Fatalf("unexpected encoding: %s %d %v", tc.path, response.StatusCode, response.Header)
		}
		if tc.method == "HEAD" && len(body) != 0 {
			t.Fatal("HEAD sent a body")
		}
	}
}

func TestInventoryEncodingHonorsIdentityRefusal(t *testing.T) {
	f := setup(t)
	client := rawClient(t)
	path := "/api/v1/networks/" + f.network.ID + "/items"
	response, body := rawRequest(t, client, f, "GET", path, adminToken, []string{"gzip, identity;q=0"})
	if response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("identity was used despite explicit refusal: %d %v", response.StatusCode, response.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	zr.Close()
	if err != nil || !bytes.Contains(decoded, []byte(`"items":[]`)) {
		t.Fatalf("invalid small compressed page: %q %v", decoded, err)
	}
	response, _ = rawRequest(t, client, f, "GET", path, adminToken, []string{"br, *;q=0"})
	if response.StatusCode != 406 {
		t.Fatalf("unsupported encodings returned %d, want 406", response.StatusCode)
	}
}
