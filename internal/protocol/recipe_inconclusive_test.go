package protocol_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestBackgroundRecipeInconclusiveWire(t *testing.T) {
	var batch protocol.InventoryBatch
	err := json.Unmarshal([]byte(`{"session_id":"session","sequence":"1","updates":[{"id":"1","quantity":"42","observation":"2","craftable":null,"assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":true}]}`), &batch)
	if err != nil {
		t.Fatalf("valid inconclusive outcome rejected: %v", err)
	}
	if err := batch.ValidateV2(16); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(16); err == nil {
		t.Fatal("v1 accepted v2 outcome")
	}
	encoded, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip["updates"].([]any)[0].(map[string]any)["recipe_inconclusive"] != true {
		t.Fatal("frozen batch lost inconclusive discriminator", string(encoded))
	}
}

func TestBackgroundRecipeInconclusiveInvalidWire(t *testing.T) {
	for _, flag := range []string{"false", "null", "1", `"true"`, "{}", "[]"} {
		t.Run(flag, func(t *testing.T) {
			var update protocol.Update
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"id":"1","quantity":"42","observation":"2","craftable":null,"assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":%s}`, flag)), &update); err == nil {
				t.Fatal("accepted non-true recipe_inconclusive")
			}
		})
	}
	for name, raw := range map[string]string{
		"boolean recipe": `{"id":"1","quantity":"42","observation":"2","craftable":false,"assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":true}`,
		"no assignment":  `{"id":"1","quantity":"42","observation":"2","craftable":null,"recipe_inconclusive":true}`,
		"definition":     `{"item":{"kind":"item","name":"test:item","damage":0,"exact":true,"label":"Item"},"quantity":"42","observation":"2","craftable":null,"assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":true}`,
		"no quantity":    `{"id":"1","observation":"2","craftable":null,"assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":true}`,
		"no craftable":   `{"id":"1","quantity":"42","observation":"2","assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":true}`,
		"number":         `{"id":"1","quantity":42,"observation":"2","craftable":null,"assignment_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","recipe_inconclusive":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			var update protocol.Update
			if err := json.Unmarshal([]byte(raw), &update); err == nil {
				batch := protocol.InventoryBatch{SessionID: "session", Sequence: 1, Updates: []protocol.Update{update}}
				if err := batch.ValidateV2(16); err == nil {
					t.Fatal("accepted malformed inconclusive outcome")
				}
			}
		})
	}
}

func TestBackgroundRecipeInconclusiveAbsentStaysOmitted(t *testing.T) {
	var update protocol.Update
	if err := json.Unmarshal([]byte(`{"id":"1","quantity":"42","observation":"2","craftable":null}`), &update); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["recipe_inconclusive"]; exists {
		t.Fatal("ordinary receipt emitted inconclusive flag", string(encoded))
	}
}
