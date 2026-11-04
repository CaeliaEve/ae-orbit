package api

import (
	"bytes"
	"net/http"
	"time"

	bridgefiles "github.com/CaeliaEve/ae-orbit/oc"
)

func (s *Server) ocFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("path")
	data, err := bridgefiles.Files.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
