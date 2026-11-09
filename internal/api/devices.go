package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func (s *Server) devices(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	result, err := s.db.Devices(r.Context(), member.ID, r.PathValue("network"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, result)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	if !member.Admin {
		fail(w, store.ErrForbidden)
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &input, 2048); err != nil {
		fail(w, err)
		return
	}
	device, token, err := s.db.CreateDevice(r.Context(), member.ID, r.PathValue("network"), input.Name)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 201, map[string]any{"device": device, "device_token": token})
}

func (s *Server) rotateDevice(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	if !member.Admin {
		fail(w, store.ErrForbidden)
		return
	}
	var input struct{}
	if err := readJSON(w, r, &input, 1024); err != nil {
		fail(w, err)
		return
	}
	token, err := s.db.RotateDevice(r.Context(), member.ID, r.PathValue("network"), r.PathValue("device"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]string{"device_token": token})
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	if err := s.db.RevokeDevice(r.Context(), member.ID, r.PathValue("network"), r.PathValue("device")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) enableCooperation(w http.ResponseWriter, r *http.Request, member protocol.Member) {
	if !member.Admin {
		fail(w, store.ErrForbidden)
		return
	}
	var input struct{}
	if err := readJSON(w, r, &input, 1024); err != nil {
		fail(w, err)
		return
	}
	if err := s.db.EnableCooperation(r.Context(), member.ID, r.PathValue("network")); err != nil {
		fail(w, err)
		return
	}
	result, err := s.db.Network(r.Context(), member.ID, r.PathValue("network"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, result)
}

func (s *Server) bootV2(w http.ResponseWriter, r *http.Request, _ store.Device) {
	var input struct{}
	if err := readJSON(w, r, &input, 1024); err != nil {
		fail(w, err)
		return
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]string{"boot_id": hex.EncodeToString(nonce[:])})
}

func (s *Server) helloV2(w http.ResponseWriter, r *http.Request, device store.Device) {
	var input protocol.Hello
	if err := readJSON(w, r, &input, s.db.Options().Limits.BatchBytes); err != nil {
		fail(w, err)
		return
	}
	result, err := s.db.HelloV2(r.Context(), device, input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, result)
}

func (s *Server) pollV2(w http.ResponseWriter, r *http.Request, device store.Device) {
	var input protocol.Poll
	if err := readJSON(w, r, &input, s.db.Options().Limits.BatchBytes); err != nil {
		fail(w, err)
		return
	}
	result, err := s.db.PollV2(r.Context(), device, input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, result)
}

func (s *Server) inventoryV2(w http.ResponseWriter, r *http.Request, device store.Device) {
	var input protocol.InventoryBatch
	if err := readJSON(w, r, &input, s.db.Options().Limits.BatchBytes); err != nil {
		fail(w, err)
		return
	}
	result, err := s.db.IngestV2(r.Context(), device, input)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, result)
}
