package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func jobHTTPFixture(t *testing.T) (fixture, string, protocol.ProductionJobInput) {
	t.Helper()
	f := setup(t)
	w := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, protocol.Hello{Protocol: 1, BootID: "jobs-boot", Source: "simulator", MaxResponseBytes: 32768,
		Capabilities: protocol.Capabilities{Inventory: true, Craft: true, ExactQuantities: true, MaxCraftQuantity: 1000, Enumeration: "simulated"}})), 200)
	yes := true
	a := decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v1/inventory", f.device, wire(t, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{{
		Item: &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true}, Quantity: 1, Observation: 1, Craftable: &yes,
	}}})), 200)
	in := protocol.ProductionJobInput{RequestKey: "http-job-001", Entries: []protocol.ProductionJobEntryInput{{EntryKey: "http-source-001", ItemID: a.Bindings[0].ID, Quantity: 1}}}
	return f, "/api/v1/networks/" + f.network.ID + "/jobs", in
}

// Missing routes, status-only replay metadata leaks and permissive JSON all break this contract.
func TestHTTPProductionJobsContract(t *testing.T) {
	f, base, in := jobHTTPFixture(t)
	a := decode[protocol.ProductionJobAcceptance](t, send(t, f, "POST", base, adminToken, wire(t, in)), 201)
	if a.Job == nil {
		t.Fatal(a)
	}
	raw := decode[map[string]any](t, send(t, f, "POST", base, adminToken, wire(t, in)), 200)
	if _, leaked := raw["replayed"]; leaked {
		t.Fatal("internal replay field leaked")
	}
	byKey := decode[protocol.ProductionJobAcceptance](t, send(t, f, "GET", base+"/by-key/"+in.RequestKey, adminToken, ""), 200)
	if byKey.Receipt.JobID != a.Receipt.JobID {
		t.Fatal(byKey)
	}
	viewer, viewerToken, err := f.db.CreateMember(context.Background(), f.admin.ID, "viewer", []protocol.Grant{{NetworkID: f.network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_ = viewer
	for _, path := range []string{base, base + "/" + a.Receipt.JobID, base + "/history", base + "/changes?after=0"} {
		r := send(t, f, "GET", path, viewerToken, "")
		body, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 || strings.Contains(string(body), `"nbt"`) {
			t.Fatal(path, r.StatusCode, string(body))
		}
	}
	if r := send(t, f, "GET", base+"/by-key/"+in.RequestKey, viewerToken, ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if r := send(t, f, "GET", base+"/by-key/"+in.RequestKey+"?owner_id="+f.admin.ID, viewerToken, ""); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	if r := send(t, f, "GET", base+"/by-key/"+in.RequestKey+"?owner_id="+viewer.ID, viewerToken, ""); r.StatusCode != 403 {
		t.Fatal("explicit owner selector was not admin-only", r.StatusCode)
	}
	_, crafterToken, err := f.db.CreateMember(context.Background(), f.admin.ID, "other crafter", []protocol.Grant{{NetworkID: f.network.ID, CanCraft: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/changes?after=-1", "/changes?after=1.5", "/changes?after=01", "/changes?after=9223372036854775808", "/history?limit=101", "/history?cursor=garbage"} {
		if r := send(t, f, "GET", base+path, adminToken, ""); r.StatusCode != 400 {
			t.Fatal(path, r.StatusCode)
		}
	}
	change := decode[map[string]any](t, send(t, f, "GET", base+"/changes?after=0", adminToken, ""), 200)
	if change["watermark"] != "1" || change["next_after"] != "1" {
		t.Fatal(change)
	}
	job := base + "/" + a.Receipt.JobID
	control := `{"operation_key":"http-pause-001","expected_control_version":"1"}`
	if r := send(t, f, "POST", job+"/pause", viewerToken, control); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	if r := send(t, f, "POST", job+"/pause", crafterToken, control); r.StatusCode != 403 {
		t.Fatal("nonowner crafter controlled", r.StatusCode)
	}
	pause := decode[protocol.ProductionJobSummary](t, send(t, f, "POST", job+"/pause", adminToken, control), 200)
	if pause.Control != "paused" || pause.ControlVersion != 2 {
		t.Fatal(pause)
	}
	decode[protocol.ProductionJobSummary](t, send(t, f, "POST", job+"/pause", adminToken, control), 200)
	if r := send(t, f, "POST", job+"/resume", adminToken, `{"operation_key":"http-resume-001","expected_control_version":"1"}`); r.StatusCode != 409 {
		t.Fatal(r.StatusCode)
	}
	for _, action := range []string{"pause", "resume", "stop"} {
		if r := send(t, f, "POST", job+"/"+action, adminToken, `{"operation_key":"http-bad-001","expected_control_version":"2","cpu_name":"x"}`); r.StatusCode != 400 {
			t.Fatal(action, r.StatusCode)
		}
	}
	for _, body := range []string{
		strings.TrimSuffix(wire(t, in), "}") + `,"cpu_policy":"auto"}`,
		strings.Replace(wire(t, in), `"request_key":`, `"unknown":1,"request_key":`, 1),
		strings.TrimSuffix(wire(t, in), "}") + `,"name":"` + string([]byte{0xff}) + `"}`,
		wire(t, in) + ` {}`, strings.Repeat(" ", 256*1024+1),
	} {
		r := send(t, f, "POST", base, adminToken, body)
		want := 400
		if len(body) > 256*1024 {
			want = 413
		}
		if r.StatusCode != want {
			t.Fatal("strict manifest", r.StatusCode, want)
		}
	}
	login := send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`"}`)
	cookies := login.Cookies()
	decode[map[string]any](t, login, 201)
	req, _ := http.NewRequest("POST", f.server.URL+job+"/stop", strings.NewReader(`{"operation_key":"http-stop-001","expected_control_version":"2"}`))
	req.AddCookie(cookies[0])
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost")
	r, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("missing CSRF", r.StatusCode)
	}
	// Capacity must advertise retry even when requests have spare HTTP concurrency.
	for i := 1; i < 16; i++ {
		in.RequestKey = fmt.Sprintf("http-job-%03d", i+1)
		in.Entries[0].EntryKey = fmt.Sprintf("http-source-%03d", i+1)
		decode[protocol.ProductionJobAcceptance](t, send(t, f, "POST", base, adminToken, wire(t, in)), 201)
	}
	in.RequestKey = "http-capacity-new"
	in.Entries[0].EntryKey = "http-capacity-source"
	r = send(t, f, "POST", base, adminToken, wire(t, in))
	if r.StatusCode != 429 || r.Header.Get("Retry-After") != "1" {
		t.Fatal(r.StatusCode, r.Header)
	}
	metrics := decode[struct {
		HTTP []struct {
			Route string `json:"route"`
		} `json:"http"`
	}](t, send(t, f, "GET", "/api/v1/admin/metrics", adminToken, ""), 200)
	found := false
	for _, metric := range metrics.HTTP {
		if strings.Contains(metric.Route, in.RequestKey) || strings.Contains(metric.Route, a.Receipt.JobID) {
			t.Fatal("identifier in metric label", metric.Route)
		}
		if metric.Route == "GET /api/v1/networks/{network}/jobs/by-key/{key}" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing registered fixed by-key metric")
	}
}

func TestHTTPProductionJobArchivedAcceptance(t *testing.T) {
	f, base, in := jobHTTPFixture(t)
	a := decode[protocol.ProductionJobAcceptance](t, send(t, f, "POST", base, adminToken, wire(t, in)), 201)
	decode[protocol.ProductionJobSummary](t, send(t, f, "POST", base+"/"+a.Receipt.JobID+"/stop", adminToken, `{"operation_key":"archive-stop-001","expected_control_version":"1"}`), 200)
	if _, err := f.db.Pool().Exec(context.Background(), "UPDATE production_jobs SET settled_at=now()-interval '31 days' WHERE id=$1", a.Receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		path := base
		body := wire(t, in)
		if method == "GET" {
			path += "/by-key/" + in.RequestKey
			body = ""
		}
		result := decode[map[string]any](t, send(t, f, method, path, adminToken, body), 200)
		receipt := result["receipt"].(map[string]any)
		if receipt["archived"] != true || receipt["job_id"] != a.Receipt.JobID || result["job"] != nil {
			t.Fatal(result)
		}
	}
	if r := send(t, f, "GET", base+"/"+a.Receipt.JobID, adminToken, ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if r := send(t, f, "POST", base+"/"+a.Receipt.JobID+"/pause", adminToken, `{"operation_key":"archive-pause-001","expected_control_version":"2"}`); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	in.RequestKey = "archive-replacement"
	errBody := decode[struct {
		Error struct {
			Code   string   `json:"code"`
			JobIDs []string `json:"job_ids"`
		} `json:"error"`
	}](t, send(t, f, "POST", base, adminToken, wire(t, in)), 409)
	if errBody.Error.Code != "entry_already_accepted" || len(errBody.Error.JobIDs) != 1 || errBody.Error.JobIDs[0] != a.Receipt.JobID {
		t.Fatal(errBody)
	}
}
