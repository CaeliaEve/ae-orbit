package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) lookupItems(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	values := r.URL.Query()["ids"]
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 3999 {
		fail(w, store.ErrInvalid)
		return
	}
	parts := strings.Split(values[0], ",")
	if len(parts) > 200 {
		fail(w, store.ErrInvalid)
		return
	}
	ids := make([]protocol.Quantity, len(parts))
	for i, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != part {
			fail(w, store.ErrInvalid)
			return
		}
		ids[i] = protocol.Quantity(id)
	}
	result, err := s.db.ItemsByID(r.Context(), member.ID, r.PathValue("network"), ids)
	if err != nil {
		fail(w, err)
		return
	}
	respondInventory(w, r, result)
}
