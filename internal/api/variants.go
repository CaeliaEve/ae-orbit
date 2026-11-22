package api

import (
	"encoding/base64"
	"fmt"
	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"net/http"
	"strconv"
	"unicode/utf8"
)

func (s *Server) enableVariants(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	if !member.Admin {
		fail(w, store.ErrForbidden)
		return
	}
	if !s.variantImport.TryLock() {
		fail(w, store.ErrCapacity)
		return
	}
	defer s.variantImport.Unlock()
	var input struct{}
	if err := readJSON(w, r, &input, 1024); err != nil {
		fail(w, err)
		return
	}
	value, err := s.db.EnableVariantCatalog(r.Context(), member.ID, r.PathValue("network"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, value)
}
func (s *Server) registerSample(w http.ResponseWriter, r *http.Request, device store.Device) {
	var input struct {
		Session string              `json:"session_id"`
		Item    protocol.Definition `json:"item"`
	}
	if err := readJSON(w, r, &input, 32768); err != nil {
		fail(w, err)
		return
	}
	id, err := s.db.RegisterVariantSample(r.Context(), device, input.Session, input.Item)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]any{"id": id, "registered": true})
}
func (s *Server) itemVariants(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	base, err := strconv.ParseInt(r.PathValue("item"), 10, 64)
	if err != nil {
		fail(w, store.ErrInvalid)
		return
	}
	var after int64
	if value := r.URL.Query().Get("after"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			fail(w, store.ErrInvalid)
			return
		}
	}
	page, err := s.db.ItemVariants(r.Context(), member.ID, r.PathValue("network"), protocol.Quantity(base), protocol.Quantity(after), r.URL.Query().Get("view") == "all")
	if err != nil {
		fail(w, err)
		return
	}
	s.enrichCatalog(r.PathValue("network"), page.Items)
	respond(w, 200, page)
}
func (s *Server) variantData(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	id, err := strconv.ParseInt(r.PathValue("item"), 10, 64)
	if err != nil {
		fail(w, store.ErrInvalid)
		return
	}
	page, err := s.db.ItemsByID(r.Context(), member.ID, r.PathValue("network"), []protocol.Quantity{protocol.Quantity(id)})
	if err != nil {
		fail(w, err)
		return
	}
	if len(page.Items) != 1 {
		fail(w, store.ErrNotFound)
		return
	}
	item := page.Items[0]
	raw, err := base64.StdEncoding.DecodeString(item.NBT)
	if err != nil {
		fail(w, store.ErrInvalid)
		return
	}
	data, err := nbt.TypedJSON(raw)
	if err != nil {
		fail(w, store.ErrInvalid)
		return
	}
	respond(w, 200, map[string]any{"id": item.ID, "typed_data": data})
}
func variantInfo(item protocol.Item) *protocol.VariantInfo {
	if item.NBT == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(item.NBT)
	if err != nil {
		return nil
	}
	hash, err := nbt.Digest(raw)
	if err != nil {
		return nil
	}
	result := &protocol.VariantInfo{Hash: fmt.Sprintf("%x", hash[:])}
	data, err := nbt.TypedJSON(raw)
	if err != nil {
		return result
	}
	node, _ := data.(map[string]any)
	fields, _ := node["value"].(map[string]any)
	display, _ := fields["display"].(map[string]any)
	displayFields, _ := display["value"].(map[string]any)
	name, _ := displayFields["Name"].(map[string]any)
	result.CustomName, _ = name["value"].(string)
	if len(result.CustomName) > 512 {
		result.CustomName = result.CustomName[:512]
		for !utf8.ValidString(result.CustomName) {
			result.CustomName = result.CustomName[:len(result.CustomName)-1]
		}
	}
	for _, key := range []string{"ench", "StoredEnchantments", "Genome", "BlockEntityTag", "pages", "Damage"} {
		if _, ok := fields[key]; ok {
			result.Summary = append(result.Summary, map[string]string{"ench": "附魔", "StoredEnchantments": "储存附魔", "Genome": "基因数据", "BlockEntityTag": "方块数据", "pages": "书页", "Damage": "耐久数据"}[key])
		}
	}
	return result
}
