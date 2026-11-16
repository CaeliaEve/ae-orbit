package api_test

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/catalog"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func catalogFixture(t *testing.T) *catalog.Catalog {
	t.Helper()
	root := t.TempDir()
	manifest := catalog.Manifest{Version: 1, Items: []catalog.Item{{
		Key: "minecraft:stone@0", Name: "minecraft:stone", Damage: 0,
		LocalizedName: "石头", ImagePath: "item/minecraft/stone~0.png",
		Frames:      []protocol.CatalogFrame{{X: 0, Y: 0, Width: 16, Height: 16, Ticks: 2}},
		Interpolate: true,
	}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "image.zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("item/minecraft/stone~0.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write([]byte{137, 80, 78, 71}); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	value, err := catalog.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCatalogMetadataAndImageEndpoint(t *testing.T) {
	f := setup(t)
	value := catalogFixture(t)
	t.Cleanup(func() { value.Close() })
	options := api.DefaultOptions()
	options.Origin = "http://localhost"
	options.SecureCookies = false
	server := httptest.NewServer(api.NewWithCatalog(f.db, options, value))
	t.Cleanup(server.Close)

	hello := protocol.Hello{Protocol: 1, BootID: "catalog-boot", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true}}
	welcome := decode[protocol.Welcome](t, sendServer(t, server, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	craftable := false
	batch := protocol.InventoryBatch{SessionID: welcome.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "石头", Exact: true}, Quantity: 64, Observation: 1, Craftable: &craftable}}}
	decode[protocol.InventoryAck](t, sendServer(t, server, "POST", "/bridge/v1/inventory", f.device, wire(t, batch)), 200)
	page := decode[protocol.ItemPage](t, sendServer(t, server, "GET", "/api/v1/networks/"+f.network.ID+"/items?limit=1", adminToken, ""), 200)
	if len(page.Items) != 1 || page.Items[0].Catalog == nil || page.Items[0].Catalog.ImageState != "available" {
		t.Fatalf("catalog metadata = %#v", page.Items)
	}
	if len(page.Items[0].Catalog.Frames) != 1 || !page.Items[0].Catalog.Interpolate {
		t.Fatalf("catalog animation metadata = %#v", page.Items[0].Catalog)
	}
	if page.Items[0].Catalog.ImageKey != "item/minecraft/stone~0.png" {
		t.Fatalf("catalog image key = %q", page.Items[0].Catalog.ImageKey)
	}
	imagePath := page.Items[0].Catalog.ImageURL
	response := sendServer(t, server, "GET", imagePath, adminToken, "")
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(data) != string([]byte{137, 80, 78, 71}) {
		t.Fatalf("catalog image = %d, %x, %v", response.StatusCode, data, err)
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		t.Fatal("catalog image omitted ETag")
	}
	request, err := http.NewRequest("GET", server.URL+imagePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("If-None-Match", etag)
	second, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("ETag response = %d", second.StatusCode)
	}
}

func TestTexturePackEndpointIsOptionalAndCacheable(t *testing.T) {
	f := setup(t)
	pack := filepath.Join(t.TempDir(), "textures.zip")
	if err := os.WriteFile(pack, []byte("zip-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	options := api.DefaultOptions()
	options.Origin = "http://localhost"
	options.SecureCookies = false
	options.TexturePackPath = pack
	server := httptest.NewServer(api.New(f.db, options))
	t.Cleanup(server.Close)

	response, err := server.Client().Get(server.URL + "/catalog-assets/texture-pack.zip")
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(data) != "zip-bytes" {
		t.Fatalf("texture pack = %d, %q, %v", response.StatusCode, data, readErr)
	}
	if response.Header.Get("Cache-Control") != "public, max-age=86400, must-revalidate" {
		t.Fatalf("texture pack cache header = %q", response.Header.Get("Cache-Control"))
	}
}

func sendServer(t *testing.T, server *httptest.Server, method, path, token, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
