package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func nbtDefinition(t *testing.T, name string) protocol.Definition {
	t.Helper()
	data, err := os.ReadFile("../nbt/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct{ Name, Gzip string }
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		if v.Name == name {
			return protocol.Definition{Kind: "item", Name: "test:machine", Damage: 54, NBT: v.Gzip, Label: "Machine", Exact: true}
		}
	}
	t.Fatal("missing NBT vector", name)
	return protocol.Definition{}
}

func legacyKey(d protocol.Definition) string {
	data, _ := json.Marshal([]any{d.Kind, d.Name, d.Damage, d.NBT})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestNBTIngestUpdatesRepresentationOnlyWithAcceptedObservation(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "semantic-ingest")
	a, b := nbtDefinition(t, "ordered"), nbtDefinition(t, "reordered")
	input := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &a, Quantity: 7, Observation: 10}}}
	first, err := s.Ingest(ctx, d, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Sequence = 2
	input.Updates[0].Item = &b
	input.Updates[0].Observation = 9
	input.Updates[0].Quantity = 999
	second, err := s.Ingest(ctx, d, input)
	if err != nil || second.Bindings[0].ID != first.Bindings[0].ID {
		t.Fatal("equivalent NBT split", second, err)
	}
	var raw string
	if err = s.Pool().QueryRow(ctx, "SELECT nbt FROM item_definitions WHERE id=$1", int64(first.Bindings[0].ID)).Scan(&raw); err != nil || raw != a.NBT {
		t.Fatal("stale definition replaced accepted native encoding", err)
	}
	input.Sequence = 3
	input.Updates[0].Observation = 11
	input.Updates[0].Quantity = 8
	ack, err := s.Ingest(ctx, d, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT nbt FROM item_definitions WHERE id=$1", int64(first.Bindings[0].ID)).Scan(&raw); err != nil || raw != b.NBT {
		t.Fatal("fresh encoding never reached stored definition", err)
	}
	page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Quantity != 8 {
		t.Fatal("equivalent observations added or duplicated", page, err)
	}
	replay, err := s.Ingest(ctx, d, input)
	if err != nil || !reflect.DeepEqual(ack, replay) {
		t.Fatal("NBT update lost exact ACK replay", replay, err)
	}
	input.Sequence = 4
	input.Updates = []protocol.Update{{Item: &a, Quantity: 9, Observation: 12}, {Item: &b, Quantity: 10, Observation: 12}}
	if _, err = s.Ingest(ctx, d, input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("conflicting observations of one semantic item accepted", err)
	}
}

type nbtLegacy struct {
	s           *store.Store
	admin       protocol.Member
	n           protocol.Network
	d           store.Device
	w           protocol.Welcome
	ids         []protocol.Quantity
	definitions []protocol.Definition
	token       string
}

func seedLegacyNBT(t *testing.T) nbtLegacy {
	t.Helper()
	s, admin, n, token := fixture(t)
	_, old := hello(t, s, token, "legacy-nbt-old")
	d, w := hello(t, s, token, "legacy-nbt-current")
	defs := []protocol.Definition{nbtDefinition(t, "ordered"), nbtDefinition(t, "reordered"), nbtDefinition(t, "fixed"), nbtDefinition(t, "different_value")}
	invalid := defs[0]
	invalid.NBT = "AQID"
	defs = append(defs, invalid)
	ids := make([]protocol.Quantity, len(defs))
	for i, def := range defs {
		if err := s.Pool().QueryRow(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,nbt,label,exact)
		 VALUES($1,$2,$3,$4,$5,$6,$7,true) RETURNING id`, n.ID, legacyKey(def), def.Kind, def.Name, def.Damage, def.NBT, def.Label).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
		session, quantity, observation, recipe := w.SessionID, 7, 10, 0
		if i == 0 {
			session, quantity, observation, recipe = old.SessionID, 900, 999, 999
		}
		if i == 2 {
			quantity, observation, recipe = 8, 9, 9
		}
		if _, err := s.Pool().Exec(ctx, `INSERT INTO inventory(network_id,item_id,quantity,craftable,session_id,observation,observed_at,craftable_observation,craftable_observed_at)
		 VALUES($1,$2,$3,$4,$5,$6,now(),$7,CASE WHEN $7::bigint>0 THEN now() ELSE NULL END)`, n.ID, int64(ids[i]), quantity, recipe > 0, session, observation, recipe); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool().Exec(ctx, "INSERT INTO inventory_schedule VALUES($1,$2,$3)", n.ID, int64(ids[i]), time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO orders(id,network_id,member_id,item_id,quantity,idempotency_key,ttl_seconds,state,expires_at)
	 VALUES('legacy-nbt-order',$1,$2,$3,1,'legacy-nbt-idempotency',3600,'queued',now()+interval '1 hour')`, n.ID, admin.ID, int64(ids[2])); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "INSERT INTO inventory_rechecks(network_id,item_id,token) VALUES($1,$2,$3),($1,$4,$5)", n.ID, int64(ids[0]), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", int64(ids[2]), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "DROP TABLE IF EXISTS item_aliases; DELETE FROM schema_versions WHERE version='migrations/006_nbt_identity.sql'"); err != nil {
		t.Fatal(err)
	}
	return nbtLegacy{s, admin, n, d, w, ids, defs, token}
}

func TestNBTMigrationKeepsLegacyIDsOrdersAndIndependentEvidence(t *testing.T) {
	f := seedLegacyNBT(t)
	s, n, ids := f.s, f.n, f.ids
	oldBatch := protocol.InventoryBatch{SessionID: f.w.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &f.definitions[1], Quantity: 7, Observation: 10}}}
	oldACK := protocol.InventoryAck{Ack: 1, Bindings: []protocol.Binding{{Index: 1, ID: ids[1]}}}
	encoded, _ := json.Marshal(oldBatch)
	hash := sha256.Sum256(encoded)
	response, _ := json.Marshal(oldACK)
	if _, err := s.Pool().Exec(ctx, "UPDATE bridge_sessions SET inventory_seq=1,inventory_hash=$2,inventory_response=$3 WHERE id=$1", f.w.SessionID, hash[:], response); err != nil {
		t.Fatal(err)
	}
	before, err := s.Order(ctx, f.admin.ID, n.ID, "legacy-nbt-order")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if replay, err := s.Ingest(ctx, f.d, oldBatch); err != nil || !reflect.DeepEqual(replay, oldACK) {
		t.Fatal("migration changed a previously acknowledged legacy binding", replay, err)
	}
	var aliases int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM item_aliases WHERE network_id=$1 AND canonical_id=$2", n.ID, int64(ids[0])).Scan(&aliases); err != nil || aliases != 2 {
		t.Fatal("missing legacy aliases", aliases, err)
	}
	page, err := s.Items(ctx, f.admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || len(page.Items) != 3 {
		t.Fatal("migration merged distinct/unsupported NBT or retained duplicates", page, err)
	}
	item, err := s.ItemsByID(ctx, f.admin.ID, n.ID, []protocol.Quantity{ids[0], ids[1], ids[2]})
	if err != nil || len(item.Items) != 3 || len(item.MissingIDs) != 0 {
		t.Fatal("old IDs no longer resolve", item, err)
	}
	for i, value := range item.Items {
		if value.ID != ids[i] || value.Quantity != 7 || !value.Craftable || !value.CurrentSession || value.CraftableObservedAt == nil {
			t.Fatal("alias lost newest count or independent current recipe evidence", value)
		}
		if (i == 0 && value.CanonicalID != 0) || (i > 0 && value.CanonicalID != ids[0]) {
			t.Fatal("alias lookup hid canonical identity", value)
		}
	}
	var raw, key string
	if err = s.Pool().QueryRow(ctx, "SELECT nbt,identity_key FROM item_definitions WHERE id=$1", int64(ids[0])).Scan(&raw, &key); err != nil || raw != f.definitions[1].NBT || key != f.definitions[0].Key() {
		t.Fatal("migration lost current native encoding or stable key", err)
	}
	var deadline time.Time
	if err = s.Pool().QueryRow(ctx, "SELECT next_verify_at FROM inventory_schedule WHERE network_id=$1 AND item_id=$2", n.ID, int64(ids[0])).Scan(&deadline); err != nil || !deadline.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("migration postponed oldest scheduled work", err)
	}
	var count int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1", n.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration lost or duplicated priority work", count, err)
	}
	after, err := s.Order(ctx, f.admin.ID, n.ID, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("historical order changed", after, err)
	}
	for _, id := range ids[:3] {
		replay, err := s.CreateOrder(ctx, f.admin.ID, n.ID, protocol.OrderInput{ItemID: id, Quantity: 1, IdempotencyKey: "legacy-nbt-idempotency"})
		if err != nil || replay.ID != before.ID || replay.ItemID != before.ItemID {
			t.Fatal("alias replay became a new order or conflict", replay, err)
		}
	}
	input := protocol.InventoryBatch{SessionID: f.w.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: ids[1], Quantity: 11, Observation: 11}}}
	ack, err := s.Ingest(ctx, f.d, input)
	if err != nil {
		t.Fatal("old short ID rejected", err)
	}
	replay, err := s.Ingest(ctx, f.d, input)
	if err != nil || !reflect.DeepEqual(ack, replay) {
		t.Fatal("alias ACK replay changed", err)
	}
	item, err = s.ItemsByID(ctx, f.admin.ID, n.ID, []protocol.Quantity{ids[0]})
	if err != nil || item.Items[0].Quantity != 11 {
		t.Fatal("short alias wrote a duplicate inventory row", item, err)
	}
	poll, err := s.Poll(ctx, f.d, protocol.Poll{SessionID: f.w.SessionID, Sequence: 1})
	if err != nil || len(poll.Commands) != 1 || poll.Commands[0].ItemID != ids[0] || poll.Commands[0].Item.NBT != raw {
		t.Fatal("historical queued order lost canonical target", poll, err)
	}
	command := poll.Commands[0]
	if _, err = s.Poll(ctx, f.d, protocol.Poll{SessionID: f.w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: command.OrderID, AttemptID: command.AttemptID, State: "done"}}}); err != nil {
		t.Fatal("legacy order completion failed to schedule canonical recheck", err)
	}
	created, err := s.CreateOrder(ctx, f.admin.ID, n.ID, protocol.OrderInput{ItemID: ids[1], Quantity: 1, IdempotencyKey: "new-order-from-alias"})
	if err != nil || created.ItemID != ids[0] {
		t.Fatal("new order retained obsolete identity", created, err)
	}
}

func TestNBTMigrationFailureRollsBackCatalogAliasesAndInventory(t *testing.T) {
	f := seedLegacyNBT(t)
	if _, err := f.s.Pool().Exec(ctx, `CREATE FUNCTION fail_nbt_merge() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test nbt merge failure'; END $$;
	 CREATE TRIGGER fail_nbt_merge BEFORE DELETE ON inventory FOR EACH ROW EXECUTE FUNCTION fail_nbt_merge()`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Migrate(ctx); err == nil {
		t.Fatal("migration ignored injected merge failure")
	}
	var count int
	var clean bool
	if err := f.s.Pool().QueryRow(ctx, `SELECT count(*),to_regclass('item_aliases') IS NULL AND NOT EXISTS(SELECT 1 FROM schema_versions WHERE version='migrations/006_nbt_identity.sql') FROM inventory WHERE network_id=$1`, f.n.ID).Scan(&count, &clean); err != nil || count != len(f.ids) || !clean {
		t.Fatal("failed migration partially changed database", count, clean, err)
	}
	var key string
	if err := f.s.Pool().QueryRow(ctx, "SELECT identity_key FROM item_definitions WHERE id=$1", int64(f.ids[0])).Scan(&key); err != nil || key != legacyKey(f.definitions[0]) {
		t.Fatal("failed migration changed old key", err)
	}
	if _, err := f.s.Pool().Exec(ctx, "DROP TRIGGER fail_nbt_merge ON inventory"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Migrate(ctx); err != nil {
		t.Fatal("rollback prevented migration retry", err)
	}
}
