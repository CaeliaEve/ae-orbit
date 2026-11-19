package api

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func historyQuery(r *http.Request) (store.HistoryQuery, int64, error) {
	var q store.HistoryQuery
	values := r.URL.Query()
	parse := func(key string) (int64, error) {
		v := values.Get(key)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || strconv.FormatInt(n, 10) != v {
			return 0, store.ErrInvalid
		}
		return n, nil
	}
	var err error
	if q.Epoch, err = parse("epoch"); err != nil {
		return q, 0, err
	}
	if q.Item, err = parse("item"); err != nil {
		return q, 0, err
	}
	if q.Before, err = parse("before"); err != nil {
		return q, 0, err
	}
	n, err := parse("resolution")
	if err != nil || n > 86400 {
		return q, 0, store.ErrInvalid
	}
	q.Resolution = int(n)
	n, err = parse("limit")
	if err != nil || n > 200 {
		return q, 0, store.ErrInvalid
	}
	q.Limit = int(n)
	snapshot, err := parse("snapshot")
	if err != nil {
		return q, 0, err
	}
	for _, field := range []struct {
		key    string
		target *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if v := values.Get(field.key); v != "" {
			t, e := time.Parse(time.RFC3339Nano, v)
			if e != nil {
				return q, 0, store.ErrInvalid
			}
			*field.target = t
		}
	}
	q.Kind, q.Direction = values.Get("kind"), values.Get("direction")
	return q, snapshot, nil
}
func (s *Server) inventoryHistorySeries(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	q, _, err := historyQuery(r)
	if err != nil {
		fail(w, err)
		return
	}
	value, err := s.db.InventoryHistorySeries(r.Context(), m.ID, r.PathValue("network"), q)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, value)
}
func (s *Server) inventoryHistoryRecords(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	q, snapshot, err := historyQuery(r)
	if err != nil {
		fail(w, err)
		return
	}
	value, err := s.db.InventoryHistoryRecords(r.Context(), m.ID, r.PathValue("network"), q, snapshot)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, value)
}
func (s *Server) inventoryHistoryItems(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	q, _, err := historyQuery(r)
	if err != nil {
		fail(w, err)
		return
	}
	value, err := s.db.InventoryHistoryItems(r.Context(), m.ID, r.PathValue("network"), q.Epoch, r.URL.Query().Get("q"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, value)
}
func csvText(s string) string {
	if strings.HasPrefix(s, "=") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "@") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") {
		return "'" + s
	}
	return s
}
func (s *Server) inventoryHistoryCSV(w http.ResponseWriter, r *http.Request, m protocol.Member) {
	q, snapshot, err := historyQuery(r)
	if err != nil {
		fail(w, err)
		return
	}
	q.Limit = 200
	page, err := s.db.InventoryHistoryRecords(r.Context(), m.ID, r.PathValue("network"), q, snapshot)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="inventory-history.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	out := csv.NewWriter(w)
	_ = out.Write([]string{"记录时间", "观测时间", "物品ID", "物品名称", "注册名称", "元数据", "数量", "上次数量", "净变化", "记录类型"})
	// A frozen watermark keeps exports stable while new quantities arrive.
	snapshot, _ = strconv.ParseInt(page.Snapshot, 10, 64)
	for {
		for _, row := range page.Records {
			previous, delta := "", ""
			if row.Previous != nil {
				previous = *row.Previous
			}
			if row.Delta != nil {
				delta = *row.Delta
			}
			if err = out.Write([]string{row.RecordedAt.Format(time.RFC3339Nano), row.ObservedAt.Format(time.RFC3339Nano), row.ItemID, csvText(row.Label), csvText(row.Name), strconv.Itoa(row.Damage), row.Quantity, previous, delta, row.Kind}); err != nil {
				return
			}
		}
		out.Flush()
		if out.Error() != nil {
			return
		}
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		if !page.HasMore {
			return
		}
		q.Before, _ = strconv.ParseInt(page.Next, 10, 64)
		page, err = s.db.InventoryHistoryRecords(r.Context(), m.ID, r.PathValue("network"), q, snapshot)
		if err != nil {
			return
		}
	}
}
