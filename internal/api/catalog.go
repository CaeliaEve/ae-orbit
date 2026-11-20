package api

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/CaeliaEve/ae-orbit/internal/catalog"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) texturePack(w http.ResponseWriter, r *http.Request) {
	if s.options.TexturePackPath == "" {
		fail(w, store.ErrNotFound)
		return
	}
	file, err := os.Open(s.options.TexturePackPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fail(w, store.ErrNotFound)
			return
		}
		fail(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		fail(w, store.ErrNotFound)
		return
	}
	if info.Size() > 1<<30 {
		fail(w, store.ErrInvalid)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400, must-revalidate")
	w.Header().Set("Content-Type", "application/zip")
	http.ServeContent(w, r, "texture-pack.zip", info.ModTime(), file)
}

func (s *Server) enrichCatalog(network string, items []protocol.Item) {
	if s.catalog == nil {
		return
	}
	for index := range items {
		match, state, ok := s.catalog.LookupResource(items[index].Kind, items[index].Name, items[index].Damage, items[index].NBT)
		if !ok {
			continue
		}
		public := match.Public(state)
		if state == "available" {
			public.ImageURL = catalog.ImageURLPath(network, items[index].ID) + "?v=" + url.QueryEscape(public.ImageKey)
		}
		items[index].Catalog = &public
	}
}

func (s *Server) catalogImage(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	if s.catalog == nil {
		fail(w, store.ErrNotFound)
		return
	}
	value, err := strconv.ParseInt(r.PathValue("item"), 10, 64)
	if err != nil || value < 1 {
		fail(w, store.ErrInvalid)
		return
	}
	result, err := s.db.ItemsByID(r.Context(), member.ID, r.PathValue("network"), []protocol.Quantity{protocol.Quantity(value)})
	if err != nil {
		fail(w, err)
		return
	}
	if len(result.Items) != 1 {
		fail(w, store.ErrNotFound)
		return
	}
	item := result.Items[0]
	match, state, ok := s.catalog.LookupResource(item.Kind, item.Name, item.Damage, item.NBT)
	if !ok || state != "available" {
		fail(w, store.ErrNotFound)
		return
	}
	data, digest, err := s.catalog.Image(match)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fail(w, store.ErrNotFound)
			return
		}
		fail(w, err)
		return
	}
	etag := `"` + digest + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
