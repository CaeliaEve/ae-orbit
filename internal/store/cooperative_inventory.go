package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) IngestV2(ctx context.Context, d Device, b protocol.InventoryBatch) (protocol.InventoryAck, error) {
	ack := protocol.InventoryAck{Bindings: []protocol.Binding{}}
	if err := b.ValidateV2(s.options.Limits.BatchItems); err != nil {
		return ack, errors.Join(ErrInvalid, err)
	}
	hash, err := digest(b)
	if err != nil {
		return ack, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := lockDevice(ctx, tx, d, b.SessionID, 2); err != nil {
			return err
		}
		var last protocol.Quantity
		var oldHash, response, capsJSON []byte
		var responseBytes int
		if err := tx.QueryRow(ctx, `SELECT inventory_seq,inventory_hash,inventory_response,capabilities,response_bytes
			FROM bridge_sessions WHERE id=$1`, b.SessionID).Scan(&last, &oldHash, &response, &capsJSON, &responseBytes); err != nil {
			return err
		}
		if b.Sequence == last {
			if !bytes.Equal(oldHash, hash[:]) {
				return ErrSequence
			}
			return json.Unmarshal(response, &ack)
		}
		if b.Sequence != last+1 {
			return ErrSequence
		}
		if len(b.Updates) > s.negotiatedLimits(responseBytes).BatchItems {
			return ErrCapacity
		}
		var caps protocol.Capabilities
		if err := json.Unmarshal(capsJSON, &caps); err != nil {
			return err
		}
		if !caps.Inventory {
			return ErrCapability
		}
		if !caps.ExactQuantities {
			for _, u := range b.Updates {
				if u.AssignmentID != "" {
					return ErrCapability
				}
			}
		}
		if d.Role != "primary" {
			if b.ScanCompleted {
				return ErrForbidden
			}
			for _, u := range b.Updates {
				if u.Item != nil || u.Craftable != nil || u.RecipeInconclusive || u.AssignmentID == "" {
					return ErrForbidden
				}
			}
		}
		ids, bindings, err := s.resolveDefinitions(ctx, tx, d.NetworkID, b.Updates)
		if err != nil {
			return err
		}
		ack.Bindings = bindings
		accepted, changed, catalog, recipes, confirmed, err := s.acceptCooperativeInventory(ctx, tx, d, b, ids)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE devices SET last_seen=now(),
			last_inventory=CASE WHEN $2 THEN now() ELSE last_inventory END WHERE id=$1`, d.ID, accepted); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `UPDATE networks SET
			last_seen=CASE WHEN $2 THEN now() ELSE last_seen END,
			last_inventory=CASE WHEN $2 AND $3 THEN now() ELSE last_inventory END,
			first_scan=CASE WHEN $4 AND last_scan IS NULL THEN now() ELSE first_scan END,
			last_scan=CASE WHEN $4 THEN now() ELSE last_scan END,
			revision=revision+CASE WHEN $5 THEN 1 ELSE 0 END,
			catalog_revision=catalog_revision+CASE WHEN $6 THEN 1 ELSE 0 END,
			craftable_revision=craftable_revision+CASE WHEN $7 THEN 1 ELSE 0 END
			WHERE id=$1 RETURNING revision`, d.NetworkID, d.Role == "primary", accepted, b.ScanCompleted, changed, catalog, recipes).Scan(&ack.Revision); err != nil {
			return err
		}
		ack.Ack = b.Sequence
		encoded, _ := json.Marshal(ack)
		if len(encoded) > responseBytes {
			return ErrCapacity
		}
		// The client counter is session-global. Remember all authenticated input
		// for future dispatches; current receipts use their captured task floors.
		var observation protocol.Quantity
		for _, update := range b.Updates {
			observation = max(observation, update.Observation)
		}
		if _, err = tx.Exec(ctx, `UPDATE bridge_sessions SET inventory_seq=$2,inventory_hash=$3,inventory_response=$4,
			observation_watermark=greatest(observation_watermark,$5) WHERE id=$1`,
			b.SessionID, int64(b.Sequence), hash[:], encoded, int64(observation)); err != nil {
			return err
		}
		if changed || b.ScanCompleted || confirmed {
			return s.event(ctx, tx, d.NetworkID, "inventory")
		}
		return nil
	})
	return ack, err
}

type cooperativeField struct {
	Field, AssignmentID, DeviceID, SessionID, Purpose, RequestID string
	Generation, Floor                                            int64
	ExpiresAt, DispatchedAt                                      time.Time
	CompletedAt                                                  *time.Time
}

type cooperativePrevious struct {
	Quantity, Observation, RecipeObservation protocol.Quantity
	Craftable                                bool
	Session, RecipeSession                   string
	RecipeTime                               *time.Time
	Label, NBT, MetadataSession              string
	MetadataObservation                      protocol.Quantity
}

type cooperativeWrite struct {
	ID                    protocol.Quantity `json:"id"`
	Quantity              protocol.Quantity `json:"quantity"`
	Observation           protocol.Quantity `json:"observation"`
	Craftable             *bool             `json:"craftable"`
	RecipeObservation     protocol.Quantity `json:"recipe_observation"`
	QuantityAccepted      bool              `json:"quantity_accepted"`
	RecipeAccepted        bool              `json:"recipe_accepted"`
	RecipeAttemptAccepted bool              `json:"recipe_attempt_accepted"`
	RecipeInconclusive    bool              `json:"-"`
	QuantityGeneration    int64             `json:"quantity_generation"`
	RecipeGeneration      int64             `json:"recipe_generation"`
	QuantityTask          *cooperativeField `json:"-"`
	RecipeTask            *cooperativeField `json:"-"`
}

func cooperativeSigningKey(ctx context.Context, tx pgx.Tx) ([]byte, error) {
	var key []byte
	err := tx.QueryRow(ctx, "SELECT signing_key FROM cooperative_secrets WHERE singleton").Scan(&key)
	if !errors.Is(err, pgx.ErrNoRows) {
		return key, err
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO cooperative_secrets(signing_key) VALUES($1) ON CONFLICT DO NOTHING", key); err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, "SELECT signing_key FROM cooperative_secrets WHERE singleton").Scan(&key)
	return key, err
}

func assignmentMAC(key []byte, d Device, session string, id protocol.Quantity, kind string, nonce []byte) []byte {
	mac := hmac.New(sha256.New, key)
	identity, _ := json.Marshal([]string{d.NetworkID, d.ID, session, id.String(), kind})
	mac.Write(identity)
	mac.Write(nonce)
	return mac.Sum(nil)[:8]
}

func newAssignmentID(key []byte, d Device, session string, id protocol.Quantity, kind string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:8]); err != nil {
		return "", err
	}
	copy(token[8:], assignmentMAC(key, d, session, id, kind, token[:8]))
	return hex.EncodeToString(token[:]), nil
}

func authenticAssignment(key []byte, d Device, session string, id protocol.Quantity, kind, token string) bool {
	raw, err := hex.DecodeString(token)
	return err == nil && len(raw) == 16 && hmac.Equal(raw[8:], assignmentMAC(key, d, session, id, kind, raw[:8]))
}

func (s *Store) acceptCooperativeInventory(ctx context.Context, tx pgx.Tx, d Device, b protocol.InventoryBatch, ids []protocol.Quantity) (accepted, changed, catalog, recipes, confirmed bool, err error) {
	if len(ids) == 0 {
		return
	}
	unique := map[protocol.Quantity]bool{}
	values := []int64{}
	for _, id := range ids {
		if !unique[id] {
			unique[id] = true
			values = append(values, int64(id))
		}
	}
	previous := map[protocol.Quantity]cooperativePrevious{}
	rows, err := tx.Query(ctx, `SELECT d.id,i.quantity,i.observation,i.craftable,i.craftable_observation,
		i.session_id,i.craftable_session_id,i.craftable_observed_at,d.label,d.nbt,d.discovery_session_id,d.discovery_observation
		FROM item_definitions d JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id
		WHERE d.network_id=$1 AND d.id=ANY($2::bigint[])`, d.NetworkID, values)
	if err != nil {
		return
	}
	for rows.Next() {
		var id protocol.Quantity
		var p cooperativePrevious
		if err = rows.Scan(&id, &p.Quantity, &p.Observation, &p.Craftable, &p.RecipeObservation,
			&p.Session, &p.RecipeSession, &p.RecipeTime, &p.Label, &p.NBT, &p.MetadataSession, &p.MetadataObservation); err != nil {
			rows.Close()
			return
		}
		previous[id] = p
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}
	fields := map[protocol.Quantity]map[string]cooperativeField{}
	rows, err = tx.Query(ctx, `SELECT item_id,field,assignment_id,device_id,session_id,purpose,request_id,
		generation,observation_floor,expires_at,dispatched_at,completed_at FROM cooperative_read_fields
		WHERE network_id=$1 AND item_id=ANY($2::bigint[])`, d.NetworkID, values)
	if err != nil {
		return
	}
	for rows.Next() {
		var id protocol.Quantity
		var f cooperativeField
		if err = rows.Scan(&id, &f.Field, &f.AssignmentID, &f.DeviceID, &f.SessionID, &f.Purpose, &f.RequestID,
			&f.Generation, &f.Floor, &f.ExpiresAt, &f.DispatchedAt, &f.CompletedAt); err != nil {
			rows.Close()
			return
		}
		if fields[id] == nil {
			fields[id] = map[string]cooperativeField{}
		}
		fields[id][f.Field] = f
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
		return
	}
	var signingKey []byte
	writes := map[protocol.Quantity]*cooperativeWrite{}
	jointObservations := map[protocol.Quantity]map[protocol.Quantity]protocol.Update{}
	type metadataWrite struct {
		ID          protocol.Quantity `json:"id"`
		Label       string            `json:"label"`
		NBT         string            `json:"nbt"`
		Observation protocol.Quantity `json:"observation"`
	}
	metadata := map[protocol.Quantity]metadataWrite{}
	for i, u := range b.Updates {
		id := ids[i]
		p, existed := previous[id]
		var qTask, rTask *cooperativeField
		q, r := false, false
		joint := false
		if u.AssignmentID != "" {
			kind := "quantity"
			if u.Craftable != nil || u.RecipeInconclusive {
				kind = "quantity_recipe"
			}
			if signingKey == nil {
				signingKey, err = cooperativeSigningKey(ctx, tx)
				if err != nil {
					return
				}
			}
			if !authenticAssignment(signingKey, d, b.SessionID, id, kind, u.AssignmentID) {
				err = ErrForbidden
				return
			}
			qualifies := func(f cooperativeField) bool {
				return f.AssignmentID == u.AssignmentID && f.DeviceID == d.ID && f.SessionID == b.SessionID &&
					f.CompletedAt == nil && f.ExpiresAt.After(now) && int64(u.Observation) > f.Floor
			}
			qf, rf := fields[id]["quantity"], fields[id]["recipe"]
			q = qualifies(qf)
			r = kind == "quantity_recipe" && qualifies(rf)
			if kind == "quantity_recipe" && (!q || !r) {
				q, r = false, false
			}
			joint = kind == "quantity_recipe" && q && r
			if joint && u.RecipeInconclusive && (qf.Purpose != "background" || rf.Purpose != "background") {
				err = ErrForbidden
				return
			}
			if q {
				qTask = &qf
			}
			if r {
				rTask = &rf
			}
		} else {
			// Canonical identity has already been resolved. A fresh encoding or
			// a delayed discovery is not permission to replace existing evidence.
			q = !existed && fields[id]["quantity"].Generation == 0
			r = u.Craftable != nil && (!existed || (p.RecipeTime == nil && p.RecipeSession == "")) &&
				fields[id]["recipe"].Generation == 0
		}
		if q || r {
			w := writes[id]
			if w == nil {
				w = &cooperativeWrite{ID: id}
				writes[id] = w
			}
			if joint {
				// Joint candidates compete as whole receipts, including the absence
				// of recipe evidence. Check all ties, not only the current winner.
				seen := jointObservations[id]
				if seen == nil {
					seen = map[protocol.Quantity]protocol.Update{}
					jointObservations[id] = seen
				}
				if old, ok := seen[u.Observation]; ok &&
					(old.Quantity != u.Quantity || old.RecipeInconclusive != u.RecipeInconclusive ||
						(old.Craftable != nil && u.Craftable != nil && *old.Craftable != *u.Craftable)) {
					err = ErrConflict
					return
				}
				seen[u.Observation] = u
				if !w.QuantityAccepted || w.QuantityTask == nil || u.Observation > w.Observation {
					w.QuantityAccepted, w.Quantity, w.Observation, w.QuantityTask = true, u.Quantity, u.Observation, qTask
					w.QuantityGeneration = qTask.Generation
					w.RecipeAttemptAccepted, w.RecipeAccepted = true, !u.RecipeInconclusive
					w.RecipeInconclusive, w.Craftable, w.RecipeObservation, w.RecipeTask = u.RecipeInconclusive, u.Craftable, u.Observation, rTask
					w.RecipeGeneration = rTask.Generation
				}
			}
			if q && !joint {
				if w.QuantityAccepted && w.Observation == u.Observation && w.Quantity != u.Quantity {
					err = ErrConflict
					return
				}
				if !w.QuantityAccepted || (w.QuantityTask == nil && qTask != nil) ||
					((w.QuantityTask == nil) == (qTask == nil) && u.Observation > w.Observation) {
					w.QuantityAccepted, w.Quantity, w.Observation, w.QuantityTask = true, u.Quantity, u.Observation, qTask
					if qTask != nil {
						w.QuantityGeneration = qTask.Generation
					}
				}
			}
			if r && !joint {
				if w.RecipeAccepted && w.RecipeObservation == u.Observation && *w.Craftable != *u.Craftable {
					err = ErrConflict
					return
				}
				if !w.RecipeAccepted || (w.RecipeTask == nil && rTask != nil) ||
					((w.RecipeTask == nil) == (rTask == nil) && u.Observation > w.RecipeObservation) {
					w.RecipeAccepted, w.Craftable, w.RecipeObservation, w.RecipeTask = true, u.Craftable, u.Observation, rTask
					w.RecipeAttemptAccepted = true
					if rTask != nil {
						w.RecipeGeneration = rTask.Generation
					}
				}
			}
		}
		if u.Item != nil && (p.MetadataSession != b.SessionID || u.Observation > p.MetadataObservation) {
			label := u.Item.Label
			if label == "" {
				label = u.Item.Name
			}
			m := metadataWrite{ID: id, Label: label, NBT: u.Item.NBT, Observation: u.Observation}
			old, found := metadata[id]
			if found && old.Observation == m.Observation && (old.Label != m.Label || old.NBT != m.NBT) {
				err = ErrConflict
				return
			}
			if !found || m.Observation > old.Observation {
				metadata[id] = m
			}
		}
	}
	input := make([]cooperativeWrite, 0, len(writes))
	for id, w := range writes {
		p, existed := previous[id]
		changed = changed || !existed || (w.QuantityAccepted && (p.Quantity != w.Quantity || p.Session != b.SessionID)) ||
			(w.RecipeAccepted && (p.Craftable != *w.Craftable || p.RecipeSession != b.SessionID))
		catalog = catalog || !existed
		recipes = recipes || (w.RecipeAccepted && (!existed || p.Craftable != *w.Craftable))
		input = append(input, *w)
	}
	sort.Slice(input, func(i, j int) bool { return input[i].ID < input[j].ID })
	if len(input) > 0 {
		data, _ := json.Marshal(input)
		_, err = tx.Exec(ctx, `WITH x AS (
			SELECT * FROM jsonb_to_recordset($2::jsonb) AS x(id bigint,quantity bigint,observation bigint,
				craftable boolean,recipe_observation bigint,quantity_accepted boolean,recipe_accepted boolean,
				quantity_generation bigint,recipe_generation bigint)
		) INSERT INTO inventory(network_id,item_id,quantity,observation,session_id,observed_at,craftable,
			craftable_observation,craftable_session_id,craftable_observed_at,quantity_generation,craftable_generation)
		SELECT $1,x.id,CASE WHEN x.quantity_accepted THEN x.quantity ELSE i.quantity END,
			CASE WHEN x.quantity_accepted THEN x.observation ELSE i.observation END,
			CASE WHEN x.quantity_accepted THEN $3 ELSE i.session_id END,
			CASE WHEN x.quantity_accepted THEN now() ELSE i.observed_at END,
			CASE WHEN x.recipe_accepted THEN x.craftable ELSE coalesce(i.craftable,false) END,
			CASE WHEN x.recipe_accepted THEN x.recipe_observation ELSE coalesce(i.craftable_observation,0) END,
			CASE WHEN x.recipe_accepted THEN $3 ELSE coalesce(i.craftable_session_id,'') END,
			CASE WHEN x.recipe_accepted THEN now() ELSE i.craftable_observed_at END,
			CASE WHEN x.quantity_accepted THEN x.quantity_generation ELSE i.quantity_generation END,
			CASE WHEN x.recipe_accepted THEN x.recipe_generation ELSE coalesce(i.craftable_generation,0) END
		FROM x LEFT JOIN inventory i ON i.network_id=$1 AND i.item_id=x.id
		ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=excluded.quantity,observation=excluded.observation,
			session_id=excluded.session_id,observed_at=excluded.observed_at,craftable=excluded.craftable,
			craftable_observation=excluded.craftable_observation,craftable_session_id=excluded.craftable_session_id,
			craftable_observed_at=excluded.craftable_observed_at,quantity_generation=excluded.quantity_generation,
			craftable_generation=excluded.craftable_generation`, d.NetworkID, data, b.SessionID)
		if err != nil {
			return
		}
		accepted = true
		_, err = tx.Exec(ctx, `WITH x AS (
			SELECT * FROM jsonb_to_recordset($2::jsonb) AS x(id bigint,quantity_accepted boolean,recipe_attempt_accepted boolean)
		) INSERT INTO inventory_schedule(network_id,item_id,next_quantity_at,next_verify_at)
			SELECT $1,id,now()+$3::double precision*interval '1 second',now()+$3::double precision*interval '1 second' FROM x
			ON CONFLICT(network_id,item_id) DO UPDATE SET
			next_quantity_at=CASE WHEN (SELECT quantity_accepted FROM x WHERE x.id=excluded.item_id)
				THEN excluded.next_quantity_at ELSE inventory_schedule.next_quantity_at END,
			next_verify_at=CASE WHEN (SELECT recipe_attempt_accepted FROM x WHERE x.id=excluded.item_id)
				THEN excluded.next_verify_at ELSE inventory_schedule.next_verify_at END`, d.NetworkID, data, s.options.RecheckAfter.Seconds())
		if err != nil {
			return
		}
		for _, w := range input {
			for _, f := range []*cooperativeField{w.QuantityTask, w.RecipeTask} {
				if f == nil {
					continue
				}
				if _, err = tx.Exec(ctx, `UPDATE cooperative_read_fields SET completed_at=now()
					WHERE network_id=$1 AND item_id=$2 AND field=$3 AND assignment_id=$4`, d.NetworkID, int64(w.ID), f.Field, f.AssignmentID); err != nil {
					return
				}
			}
			if f := w.QuantityTask; f != nil && f.Purpose == "refresh" && !w.RecipeInconclusive {
				tag, e := tx.Exec(ctx, `UPDATE inventory_refreshes SET confirmed_at=now()
					WHERE network_id=$1 AND item_id=$2 AND token=$3 AND confirmed_at IS NULL AND expires_at>now()
					AND requested_at<=$4`, d.NetworkID, int64(w.ID), f.RequestID, f.DispatchedAt)
				if e != nil {
					err = e
					return
				}
				confirmed = confirmed || tag.RowsAffected() > 0
			}
			if q, r := w.QuantityTask, w.RecipeTask; q != nil && r != nil && q.AssignmentID == r.AssignmentID && q.Purpose == "order" && w.RecipeAccepted {
				if _, err = tx.Exec(ctx, `DELETE FROM inventory_rechecks WHERE network_id=$1 AND item_id=$2 AND token=$3`,
					d.NetworkID, int64(w.ID), q.RequestID); err != nil {
					return
				}
			}
		}
	}
	if len(metadata) > 0 {
		input := make([]metadataWrite, 0, len(metadata))
		for id, m := range metadata {
			p, existed := previous[id]
			labelChanged := !existed || p.Label != m.Label
			changed, catalog = changed || labelChanged, catalog || labelChanged
			input = append(input, m)
		}
		data, _ := json.Marshal(input)
		_, err = tx.Exec(ctx, `UPDATE item_definitions d SET label=x.label,nbt=x.nbt,
			discovery_session_id=$3,discovery_observation=x.observation,updated_at=now()
			FROM jsonb_to_recordset($2::jsonb) AS x(id bigint,label text,nbt text,observation bigint)
			WHERE d.network_id=$1 AND d.id=x.id`, d.NetworkID, data, b.SessionID)
	}
	return
}
