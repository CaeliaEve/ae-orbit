package api_test

import (
	"io"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestHTTPInventoryHistoryRecordsSeriesAndExport(t *testing.T) {
	f := setup(t)
	w := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, protocol.Hello{Protocol: 1, BootID: "history", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, Enumeration: "simulated"}})), 200)
	uploadBrowse(t, f, w, 1, []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "test:steel", Label: "钢锭", Exact: true}, Quantity: 9007199254740993, Observation: 10}})
	base := "/api/v1/networks/" + f.network.ID + "/inventory-history"
	series := decode[store.HistorySeries](t, send(t, f, "GET", base, adminToken, ""), 200)
	if len(series.Epochs) != 1 {
		t.Fatal(series)
	}
	page := decode[store.HistoryPage](t, send(t, f, "GET", base+"/records", adminToken, ""), 200)
	if len(page.Records) != 1 || page.Records[0].Delta != nil {
		t.Fatal(page)
	}
	items := decode[[]store.HistoryItem](t, send(t, f, "GET", base+"/items?q=gd", adminToken, ""), 200)
	if len(items) != 1 {
		t.Fatal(items)
	}
	export := send(t, f, "GET", base+"/export", adminToken, "")
	defer export.Body.Close()
	data, err := io.ReadAll(export.Body)
	if err != nil || export.StatusCode != 200 || !strings.Contains(string(data), "9007199254740993") {
		t.Fatal(export.StatusCode, string(data), err)
	}
	if response := send(t, f, "GET", base+"?from=bad", adminToken, ""); response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	} else {
		response.Body.Close()
	}
	if response := send(t, f, "GET", base+"?resolution=1", adminToken, ""); response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	} else {
		response.Body.Close()
	}
}
