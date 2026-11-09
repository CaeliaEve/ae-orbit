package store_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestDeviceIdentityHasOneCredentialSource(t *testing.T) {
	s, _, n, token := fixture(t)
	var id, role string
	if err := s.Pool().QueryRow(ctx, `SELECT id,role FROM devices WHERE network_id=$1`, n.ID).Scan(&id, &role); err != nil {
		t.Fatal("new network has no default primary device", err)
	}
	if id == "" || role != "primary" {
		t.Fatal("new network has no stable primary identity", id, role)
	}
	if _, err := s.AuthDevice(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "UPDATE devices SET revoked=true WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthDevice(ctx, token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("revoked device authenticated through compatibility network hash", err)
	}
}

func TestPrimarySessionRevalidatesDeviceCredential(t *testing.T) {
	s, _, n, token := fixture(t)
	d, w := hello(t, s, token, "primary-session")
	if d.ID == "" || d.Role != "primary" {
		t.Fatal("credential did not resolve its stable primary identity", d)
	}
	var deviceID string
	if err := s.Pool().QueryRow(ctx, "SELECT device_id FROM bridge_sessions WHERE id=$1", w.SessionID).Scan(&deviceID); err != nil || deviceID != d.ID {
		t.Fatal("session is not owned by the authenticated device", deviceID, err)
	}
	if _, err := s.Pool().Exec(ctx, "UPDATE devices SET revoked=true WHERE id=$1 AND network_id=$2", d.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	h := protocol.Hello{Protocol: 1, BootID: "primary-session", Source: "simulator", MaxResponseBytes: 32768}
	if _, err := s.Hello(ctx, d, h); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("cached authentication bypassed revoked credential", err)
	}
}

func TestDeviceSessionsAreIndependent(t *testing.T) {
	s, admin, n, token := fixture(t)
	worker, workerToken, err := s.CreateDevice(ctx, admin.ID, n.ID, "Quantity helper")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE networks SET bridge_mode='cooperative' WHERE id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	primary, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := s.AuthDevice(ctx, workerToken)
	if err != nil {
		t.Fatal(err)
	}
	h := protocol.Hello{Protocol: 2, BootID: "same-boot", Source: "simulator", MaxResponseBytes: 32768,
		Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, QuantityRefresh: true, Craft: true, CPUOutput: true, MaxCraftQuantity: 1000, Enumeration: "simulated"}}
	a, err := s.HelloV2(ctx, primary, h)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.HelloV2(ctx, helper, h)
	if err != nil {
		t.Fatal("two devices cannot use independent identical boot IDs", err)
	}
	if a.DeviceID != primary.ID || a.Role != "primary" || b.DeviceID != worker.ID || b.Role != "quantity_worker" || a.SessionID == b.SessionID || b.Limits.ReadSlots != 16 {
		t.Fatal("welcome did not bind server-owned identity or bounded reads", a, b)
	}
	if _, err = s.Pool().Exec(ctx, `INSERT INTO cpu_snapshots(network_id,session_id,cpus,truncated) VALUES($1,$2,'[]',false)`, n.ID, a.SessionID); err != nil {
		t.Fatal(err)
	}
	h.BootID = "helper-restarted"
	newB, err := s.HelloV2(ctx, helper, h)
	if err != nil || newB.SessionID == b.SessionID {
		t.Fatal("helper restart did not create its own session", newB, err)
	}
	views, err := s.Devices(ctx, admin.ID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		switch view.Role {
		case "primary":
			if view.SessionID != a.SessionID || !view.Online {
				t.Fatal("helper restart retired primary", view)
			}
		case "quantity_worker":
			if view.SessionID != newB.SessionID || view.Capabilities.Craft || view.Capabilities.CPUOutput || view.Capabilities.Enumeration != "" {
				t.Fatal("helper gained primary capabilities", view)
			}
		}
	}
	cpus, err := s.CPUs(ctx, admin.ID, n.ID)
	if err != nil || cpus.SessionID != a.SessionID {
		t.Fatal("helper restart cleared primary CPU evidence", cpus, err)
	}
	h.BootID = "same-boot"
	if _, err = s.HelloV2(ctx, helper, h); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal("retired helper boot was resurrected", err)
	}
	if _, err = s.HelloV2(ctx, primary, h); err != nil {
		t.Fatal("helper restart retired primary boot", err)
	}
}

func TestEnableCooperationRetiresOldProtocolSessions(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "single-boot")
	if err := s.CheckDeviceProtocol(ctx, d, 2); !errors.Is(err, store.ErrConflict) {
		t.Fatal("v2 accepted in single mode", err)
	}
	if err := s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeviceProtocol(ctx, d, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatal("v1 accepted in cooperative mode", err)
	}
	h := protocol.Hello{Protocol: 2, BootID: "single-boot", Source: "simulator", MaxResponseBytes: 32768}
	if _, err := s.HelloV2(ctx, d, h); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal("mode switch resurrected old boot", err)
	}
	views, err := s.Devices(ctx, admin.ID, n.ID)
	if err != nil || len(views) != 1 || views[0].Online || views[0].SessionID != "" || views[0].LastSeen != nil {
		t.Fatal("mode switch retained availability from old heartbeat", views, err)
	}
	h.BootID = "cooperative-boot"
	fresh, err := s.HelloV2(ctx, d, h)
	if err != nil || fresh.SessionID == w.SessionID {
		t.Fatal("new cooperative boot failed", fresh, err)
	}
	if err = s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	repeat, err := s.HelloV2(ctx, d, h)
	if err != nil || repeat.SessionID != fresh.SessionID {
		t.Fatal("idempotent enable retired cooperative session", repeat, err)
	}
}

func TestWorkerRotationAndRevocationAreLocal(t *testing.T) {
	s, admin, n, primaryToken := fixture(t)
	worker, token, err := s.CreateDevice(ctx, admin.ID, n.ID, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	primary, err := s.AuthDevice(ctx, primaryToken)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	h := protocol.Hello{Protocol: 2, BootID: "boot", Source: "simulator", MaxResponseBytes: 32768}
	a, err := s.HelloV2(ctx, primary, h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.HelloV2(ctx, d, h); err != nil {
		t.Fatal(err)
	}
	newToken, err := s.RotateDevice(ctx, admin.ID, n.ID, worker.ID)
	if err != nil || newToken == token {
		t.Fatal("worker token did not rotate", err)
	}
	if _, err = s.AuthDevice(ctx, token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("old token survived rotation", err)
	}
	if _, err = s.HelloV2(ctx, d, h); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("cached authentication bypassed rotation", err)
	}
	d, err = s.AuthDevice(ctx, newToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.HelloV2(ctx, d, h); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal("credential rotation resurrected retired boot", err)
	}
	h.BootID = "new-boot"
	if _, err = s.HelloV2(ctx, d, h); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeDevice(ctx, admin.ID, n.ID, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthDevice(ctx, newToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("revoked worker authenticated", err)
	}
	if _, err = s.HelloV2(ctx, d, h); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("cached authentication bypassed revocation", err)
	}
	h.BootID = "boot"
	repeat, err := s.HelloV2(ctx, primary, h)
	if err != nil || repeat.SessionID != a.SessionID {
		t.Fatal("helper credential changes retired primary", repeat, err)
	}
	if err = s.RevokeDevice(ctx, admin.ID, n.ID, primary.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatal("primary could be revoked", err)
	}
}

func TestNetworkServicesDoNotPromoteWorkerHeartbeat(t *testing.T) {
	s, admin, n, token := fixture(t)
	_, workerToken, err := s.CreateDevice(ctx, admin.ID, n.ID, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	primary, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.AuthDevice(ctx, workerToken)
	if err != nil {
		t.Fatal(err)
	}
	h := protocol.Hello{Protocol: 2, BootID: "availability", Source: "simulator", MaxResponseBytes: 32768,
		Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, Craft: true, CPUOutput: true, MaxCraftQuantity: 1000, Enumeration: "simulated"}}
	if _, err = s.HelloV2(ctx, primary, h); err != nil {
		t.Fatal(err)
	}
	b, err := s.HelloV2(ctx, worker, h)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Network(ctx, admin.ID, n.ID)
	if err != nil || before.BridgeMode != "cooperative" || before.Services == nil ||
		before.Services.QuantityReaders != 2 || !before.Services.Discovery || !before.Services.Recipes || !before.Services.Craft {
		t.Fatal("active device capabilities not exposed", before, err)
	}
	stale := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Microsecond)
	if _, err = s.Pool().Exec(ctx, "UPDATE networks SET last_seen=$2 WHERE id=$1", n.ID, stale); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE devices SET last_seen=$2 WHERE id=$1", primary.ID, stale); err != nil {
		t.Fatal(err)
	}
	if _, err = s.HelloV2(ctx, worker, h); err != nil {
		t.Fatal(err)
	}
	after, err := s.Network(ctx, admin.ID, n.ID)
	if err != nil || after.Online || !after.LastSeen.Equal(stale) || after.Services == nil ||
		after.Services.QuantityReaders != 1 || after.Services.Discovery || after.Services.Recipes || after.Services.Craft || !after.Capabilities.Craft {
		t.Fatal("worker heartbeat made primary services available", after, err)
	}
	networks, err := s.ListNetworks(ctx, admin.ID)
	if err != nil || len(networks) != 1 || networks[0].Services == nil || networks[0].Services.QuantityReaders != 1 {
		t.Fatal("network list omitted per-capability availability", networks, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE id=$1", b.SessionID); err != nil {
		t.Fatal(err)
	}
	after, err = s.Network(ctx, admin.ID, n.ID)
	if err != nil || after.Services == nil || after.Services.QuantityReaders != 0 {
		t.Fatal("retired session still counted as available", after, err)
	}
}

func TestDeviceMigrationPreservesLegacyEvidenceAndAcknowledgments(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "legacy-migration")
	craftable := true
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, ScanCompleted: true, Updates: []protocol.Update{{
		Item:     &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true},
		Quantity: 9007199254740993, Observation: 17, Craftable: &craftable,
	}}}
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	order, err := s.CreateOrder(ctx, admin.ID, n.ID, protocol.OrderInput{ItemID: ack.Bindings[0].ID, Quantity: 1, IdempotencyKey: "devices-migration-order"})
	if err != nil {
		t.Fatal(err)
	}
	var alias protocol.Quantity
	if err = s.Pool().QueryRow(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,label,exact)
		VALUES($1,'legacy-alias','item','minecraft:stone',0,'Stone alias',true) RETURNING id`, n.ID).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "INSERT INTO item_aliases(network_id,alias_id,canonical_id) VALUES($1,$2,$3)", n.ID, int64(alias), int64(ack.Bindings[0].ID)); err != nil {
		t.Fatal(err)
	}
	var beforeInventory, beforeOrder, beforeAliases []byte
	if err = s.Pool().QueryRow(ctx, "SELECT row_to_json(i) FROM inventory i WHERE network_id=$1", n.ID).Scan(&beforeInventory); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT row_to_json(o) FROM orders o WHERE id=$1", order.ID).Scan(&beforeOrder); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT row_to_json(a) FROM item_aliases a WHERE network_id=$1", n.ID).Scan(&beforeAliases); err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-device identity schema without touching catalog or ACK data.
	if _, err = s.Pool().Exec(ctx, `ALTER TABLE bridge_sessions DROP COLUMN device_id;
		ALTER TABLE bridge_sessions DROP COLUMN protocol;
		ALTER TABLE bridge_sessions ADD UNIQUE(network_id,boot_id);
		DROP TABLE devices CASCADE;
		ALTER TABLE networks DROP COLUMN bridge_mode;
		DELETE FROM schema_versions WHERE version='migrations/009_devices.sql'`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	d, err = s.AuthDevice(ctx, token)
	if err != nil || d.NetworkID != n.ID || d.Role != "primary" || d.ID == "" {
		t.Fatal("legacy token did not map to its primary", d, err)
	}
	resumed, err := s.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: "legacy-migration", Source: "simulator", MaxResponseBytes: 32768})
	if err != nil || resumed.SessionID != w.SessionID || resumed.InventoryAck != 1 {
		t.Fatal("migration retired or reset the existing session", resumed, err)
	}
	replay, err := s.Ingest(ctx, d, b)
	if err != nil || !reflect.DeepEqual(replay, ack) {
		t.Fatal("migration changed immutable ACK", replay, ack, err)
	}
	var afterInventory, afterOrder, afterAliases []byte
	if err = s.Pool().QueryRow(ctx, "SELECT row_to_json(i) FROM inventory i WHERE network_id=$1", n.ID).Scan(&afterInventory); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT row_to_json(o) FROM orders o WHERE id=$1", order.ID).Scan(&afterOrder); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT row_to_json(a) FROM item_aliases a WHERE network_id=$1", n.ID).Scan(&afterAliases); err != nil {
		t.Fatal(err)
	}
	if string(beforeInventory) != string(afterInventory) || string(beforeOrder) != string(afterOrder) || string(beforeAliases) != string(afterAliases) {
		t.Fatal("migration changed cached evidence, timestamps, aliases or orders")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := s.AuthDevice(ctx, token)
	if err != nil || again.ID != d.ID {
		t.Fatal("migration did not preserve stable primary identity", again, err)
	}
}

func TestDeviceAdministrationAccessAndCapacity(t *testing.T) {
	s, admin, n, token := fixture(t)
	primary, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	viewer, _, err := s.CreateMember(ctx, admin.ID, "device-viewer", []protocol.Grant{{NetworkID: n.ID}})
	if err != nil {
		t.Fatal(err)
	}
	outsider, _, err := s.CreateMember(ctx, admin.ID, "device-outsider", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Devices(ctx, outsider.ID, n.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("ungranted member listed devices", err)
	}
	if views, err := s.Devices(ctx, viewer.ID, n.ID); err != nil || len(views) != 1 {
		t.Fatal("viewer could not list devices", views, err)
	}
	if _, _, err = s.CreateDevice(ctx, viewer.ID, n.ID, "forbidden"); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer created a device", err)
	}
	if _, err = s.RotateDevice(ctx, viewer.ID, n.ID, primary.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer rotated a device", err)
	}
	if err = s.RevokeDevice(ctx, viewer.ID, n.ID, primary.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer revoked a device", err)
	}
	if err = s.EnableCooperation(ctx, viewer.ID, n.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer enabled cooperation", err)
	}
	if _, _, err = s.CreateDevice(ctx, admin.ID, n.ID, "  "); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("empty name accepted", err)
	}
	var last protocol.DeviceView
	for i := 0; i < 7; i++ {
		last, _, err = s.CreateDevice(ctx, admin.ID, n.ID, fmt.Sprintf("Helper %d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = s.CreateDevice(ctx, admin.ID, n.ID, "overflow"); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("more than eight unrevoked devices accepted", err)
	}
	if err = s.RevokeDevice(ctx, admin.ID, n.ID, last.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateDevice(ctx, admin.ID, n.ID, "replacement"); err != nil {
		t.Fatal("revoked historical device consumed active capacity", err)
	}
}

func TestEnableCooperationPreservesOrdersAndRejectsActiveExecution(t *testing.T) {
	for _, state := range []string{"assigned", "computing", "running", "queued", "needs_review"} {
		t.Run(state, func(t *testing.T) {
			s, admin, n, token := fixture(t)
			d, w := hello(t, s, token, "order-mode")
			craftable := true
			b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{{
				Item:     &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true},
				Quantity: 10, Observation: 1, Craftable: &craftable,
			}}}
			ack, err := s.Ingest(ctx, d, b)
			if err != nil {
				t.Fatal(err)
			}
			order, err := s.CreateOrder(ctx, admin.ID, n.ID, protocol.OrderInput{ItemID: ack.Bindings[0].ID, Quantity: 1, IdempotencyKey: "cooperation-order"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Pool().Exec(ctx, "UPDATE orders SET state=$2 WHERE id=$1", order.ID, state); err != nil {
				t.Fatal(err)
			}
			active := state == "assigned" || state == "computing" || state == "running"
			err = s.EnableCooperation(ctx, admin.ID, n.ID)
			if active && !errors.Is(err, store.ErrConflict) || !active && err != nil {
				t.Fatal("wrong cooperation admission for order state", state, err)
			}
			got, err := s.Order(ctx, admin.ID, n.ID, order.ID)
			if err != nil || got.State != state || !got.ExpiresAt.Equal(order.ExpiresAt) {
				t.Fatal("mode transition changed order or original deadline", got, err)
			}
			network, err := s.Network(ctx, admin.ID, n.ID)
			if err != nil {
				t.Fatal(err)
			}
			if active && (network.BridgeMode != "single" || network.SessionID != w.SessionID) {
				t.Fatal("rejected transition retired primary session", network)
			}
			if !active && (network.BridgeMode != "cooperative" || network.SessionID != "") {
				t.Fatal("accepted transition retained old protocol session", network)
			}
		})
	}
}

func TestPrimaryRotationKeepsWorkerSessionAndReviewsExecution(t *testing.T) {
	s, admin, n, token := fixture(t)
	old, oldWelcome := hello(t, s, token, "before-cooperation")
	craftable := true
	b := protocol.InventoryBatch{SessionID: oldWelcome.SessionID, Sequence: 1, Updates: []protocol.Update{{
		Item:     &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true},
		Quantity: 10, Observation: 1, Craftable: &craftable,
	}}}
	ack, err := s.Ingest(ctx, old, b)
	if err != nil {
		t.Fatal(err)
	}
	order, err := s.CreateOrder(ctx, admin.ID, n.ID, protocol.OrderInput{ItemID: ack.Bindings[0].ID, Quantity: 1, IdempotencyKey: "primary-rotation"})
	if err != nil {
		t.Fatal(err)
	}
	_, workerToken, err := s.CreateDevice(ctx, admin.ID, n.ID, "Helper")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	primary, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.AuthDevice(ctx, workerToken)
	if err != nil {
		t.Fatal(err)
	}
	h := protocol.Hello{Protocol: 2, BootID: "cooperative", Source: "simulator", MaxResponseBytes: 32768}
	a, err := s.HelloV2(ctx, primary, h)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := s.HelloV2(ctx, worker, h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE orders SET state='running',attempt_session=$2,attempt_id='live-attempt' WHERE id=$1`, order.ID, a.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "INSERT INTO cpu_snapshots(network_id,session_id,cpus,truncated) VALUES($1,$2,'[]',false)", n.ID, a.SessionID); err != nil {
		t.Fatal(err)
	}
	// Worker restart and credential changes must not retire A or mark its execution uncertain.
	h.BootID = "worker-restarted"
	if _, err = s.HelloV2(ctx, worker, h); err != nil {
		t.Fatal(err)
	}
	newWorkerToken, err := s.RotateDevice(ctx, admin.ID, n.ID, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	worker, err = s.AuthDevice(ctx, newWorkerToken)
	if err != nil {
		t.Fatal(err)
	}
	h.BootID = "worker-rotated"
	bb, err = s.HelloV2(ctx, worker, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Order(ctx, admin.ID, n.ID, order.ID)
	if err != nil || got.State != "running" {
		t.Fatal("worker lifecycle changed primary execution", got, err)
	}
	cpus, err := s.CPUs(ctx, admin.ID, n.ID)
	if err != nil || cpus.SessionID != a.SessionID {
		t.Fatal("worker lifecycle erased primary CPU evidence", cpus, err)
	}
	newPrimaryToken, err := s.RotateDeviceToken(ctx, admin.ID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Order(ctx, admin.ID, n.ID, order.ID)
	if err != nil || got.State != "needs_review" {
		t.Fatal("primary rotation lost uncertain execution", got, err)
	}
	h.BootID = "worker-rotated"
	repeat, err := s.HelloV2(ctx, worker, h)
	if err != nil || repeat.SessionID != bb.SessionID {
		t.Fatal("primary compatibility rotation retired worker session", repeat, err)
	}
	if _, err = s.AuthDevice(ctx, token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("old primary credential survived", err)
	}
	primary, err = s.AuthDevice(ctx, newPrimaryToken)
	if err != nil {
		t.Fatal(err)
	}
	h.BootID = "cooperative"
	if _, err = s.HelloV2(ctx, primary, h); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal("primary rotation resurrected retired handles", err)
	}
}
