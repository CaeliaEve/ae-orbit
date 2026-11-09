package store_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestHelloV2NegotiatesUsableReadLimits(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, legacy := hello(t, s, token, "legacy-limits")
	if legacy.Limits.ResponseBytes != 32768 || legacy.Limits.ReadBytes != 0 || legacy.Limits.ReadSlots != 0 {
		t.Fatalf("single welcome limits changed: %+v", legacy.Limits)
	}
	if err := s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	for _, responseBytes := range []int{2048, 16384, 32768} {
		t.Run(fmt.Sprint(responseBytes), func(t *testing.T) {
			h := protocol.Hello{Protocol: 2, BootID: fmt.Sprintf("limits-%d", responseBytes),
				Source: "simulator", MaxResponseBytes: responseBytes,
				Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true}}
			for attempt := 0; attempt < 2; attempt++ {
				w, err := s.HelloV2(ctx, d, h)
				if err != nil {
					t.Fatal(err)
				}
				if w.Limits.ResponseBytes != responseBytes || w.Limits.ReadBytes != min(responseBytes, 16384) {
					t.Errorf("unusable welcome limits: %+v", w.Limits)
				}
				body, err := json.Marshal(protocol.Poll{SessionID: w.SessionID, Sequence: 1,
					ReadSlots: &w.Limits.ReadSlots, ReadBytes: &w.Limits.ReadBytes})
				if err != nil {
					t.Fatal(err)
				}
				var poll protocol.Poll
				if err = json.Unmarshal(body, &poll); err != nil {
					t.Fatal(err)
				}
				if err = poll.ValidateV2(64, 16); err != nil {
					t.Errorf("advertised limits cannot be sent in a legal v2 poll: %s: %v", body, err)
				}
			}
		})
	}
}
