package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) events(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	network := r.PathValue("network")
	if err := s.db.RequireAccess(r.Context(), m.ID, network, false); err != nil {
		fail(w, err)
		return
	}
	var after protocol.Quantity
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || number < 0 {
			fail(w, store.ErrInvalid)
			return
		}
		after = protocol.Quantity(number)
	}
	page, err := s.db.Events(r.Context(), m.ID, network, after)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(id protocol.Quantity, kind string, value any) error {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		data, _ := json.Marshal(value)
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id.String(), kind, data); err != nil {
			return err
		}
		return controller.Flush()
	}
	apply := func(page store.EventPage, initial bool) error {
		if initial || page.Reset {
			after = page.LastID
			return send(after, "reset", map[string]any{"network_id": network, "reason": "refresh_view"})
		}
		for _, event := range page.Events {
			if err := send(event.ID, "change", event); err != nil {
				return err
			}
			after = event.ID
		}
		return nil
	}
	initial := r.Header.Get("Last-Event-ID") == ""
	if err = apply(page, initial); err != nil {
		return
	}
	if !initial && len(page.Events) == 0 && !page.Reset {
		if _, err = fmt.Fprint(w, ": connected\n\n"); err != nil {
			return
		}
		if err = controller.Flush(); err != nil {
			return
		}
	}
	ticker := time.NewTicker(s.options.StreamInterval)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		if expiry, ok := r.Context().Value(authExpiryKey{}).(time.Time); ok && !time.Now().Before(expiry) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		page, err = s.db.Events(ctx, m.ID, network, after)
		cancel()
		if err != nil {
			return
		}
		if err = apply(page, false); err != nil {
			return
		}
		if time.Since(lastHeartbeat) > 15*time.Second {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
			lastHeartbeat = time.Now()
		}
	}
}
