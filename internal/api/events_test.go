package api_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func eventFrame(reader *bufio.Reader) (string, error) {
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return frame.String(), err
		}
		if line == "\n" {
			return frame.String(), nil
		}
		frame.WriteString(line)
	}
}

func streamReader(t *testing.T, f fixture, token, after string) *bufio.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/networks/"+f.network.ID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept-Encoding", "gzip")
	if after != "" {
		req.Header.Set("Last-Event-ID", after)
	}
	r, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	if r.StatusCode != 200 || r.Header.Get("Content-Encoding") != "" || r.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("invalid event stream: %d %v", r.StatusCode, r.Header)
	}
	return bufio.NewReader(r.Body)
}

func withStreamInterval(t *testing.T, f fixture, interval time.Duration) fixture {
	t.Helper()
	options := api.DefaultOptions()
	options.StreamInterval = interval
	f.server = httptest.NewServer(api.New(f.db, options))
	t.Cleanup(f.server.Close)
	return f
}

func TestSSECommitWakesBeforeFallbackForEverySubscriber(t *testing.T) {
	f := withStreamInterval(t, setup(t), time.Hour)
	a, b := streamReader(t, f, adminToken, ""), streamReader(t, f, adminToken, "")
	for _, reader := range []*bufio.Reader{a, b} {
		frame, err := eventFrame(reader)
		if err != nil || !strings.Contains(frame, "event: reset") {
			t.Fatalf("initial stream: %q %v", frame, err)
		}
	}
	d, err := f.db.AuthDevice(context.Background(), f.device)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Hello(context.Background(), d, protocol.Hello{Protocol: 1, BootID: "notify-boot", Source: "simulator", MaxResponseBytes: 32768}); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []*bufio.Reader{a, b} {
		frame, err := eventFrame(reader)
		if err != nil || !strings.Contains(frame, `"kind":"session"`) {
			t.Fatalf("commit was not delivered before fallback: %q %v", frame, err)
		}
	}
}

func TestSSEFallbackRecoversExternalEventsAndChecksAccess(t *testing.T) {
	f := withStreamInterval(t, setup(t), 25*time.Millisecond)
	m, token, err := f.db.CreateMember(context.Background(), f.admin.ID, "event-viewer", []protocol.Grant{{NetworkID: f.network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	reader := streamReader(t, f, token, "")
	if _, err = eventFrame(reader); err != nil {
		t.Fatal(err)
	}
	// A write from another process cannot use this Store's in-memory notifier.
	if _, err = f.db.Pool().Exec(context.Background(), "INSERT INTO network_events(network_id,kind,revision) VALUES($1,'inventory',1)", f.network.ID); err != nil {
		t.Fatal(err)
	}
	frame, err := eventFrame(reader)
	if err != nil || !strings.Contains(frame, `"kind":"inventory"`) {
		t.Fatalf("fallback missed durable event: %q %v", frame, err)
	}
	if err = f.db.DeleteGrant(context.Background(), f.admin.ID, m.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = eventFrame(reader); err != io.EOF {
		t.Fatalf("revoked stream was not closed: %v", err)
	}
}

func TestSSEReconnectReplaysEventsAndLiveCommits(t *testing.T) {
	f := withStreamInterval(t, setup(t), time.Hour)
	d, err := f.db.AuthDevice(context.Background(), f.device)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = f.db.Hello(context.Background(), d, protocol.Hello{Protocol: 1, BootID: fmt.Sprintf("replay-%d", i), Source: "simulator", MaxResponseBytes: 32768}); err != nil {
			t.Fatal(err)
		}
	}
	var first string
	if err = f.db.Pool().QueryRow(context.Background(), "SELECT min(id)::text FROM network_events WHERE network_id=$1", f.network.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	reader := streamReader(t, f, adminToken, first)
	frame, err := eventFrame(reader)
	if err != nil || !strings.Contains(frame, "event: change") || strings.HasPrefix(frame, "id: "+first+"\n") {
		t.Fatalf("replay failed: %q %v", frame, err)
	}
	if _, err = f.db.Hello(context.Background(), d, protocol.Hello{Protocol: 1, BootID: "replay-live", Source: "simulator", MaxResponseBytes: 32768}); err != nil {
		t.Fatal(err)
	}
	if frame, err = eventFrame(reader); err != nil || !strings.Contains(frame, "event: change") {
		t.Fatalf("live event after replay failed: %q %v", frame, err)
	}
}

func TestSSEHeadReturnsHeadersWithoutOpeningSubscription(t *testing.T) {
	f := withStreamInterval(t, setup(t), time.Hour)
	client := rawClient(t)
	client.Timeout = 500 * time.Millisecond
	response, body := rawRequest(t, client, f, "HEAD", "/api/v1/networks/"+f.network.ID+"/events", adminToken, nil)
	if response.StatusCode != 200 || len(body) != 0 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("invalid HEAD response: %d %v", response.StatusCode, response.Header)
	}
	if stats := f.db.NotificationStats(); stats.Subscribers != 0 {
		t.Fatalf("HEAD created a streaming subscription: %+v", stats)
	}
}
