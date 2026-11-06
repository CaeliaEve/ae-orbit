package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type definitionRow struct {
	Key string `json:"identity_key"`
	protocol.Definition
}
type inventoryRow struct {
	ID                      protocol.Quantity `json:"id"`
	Quantity                protocol.Quantity `json:"quantity"`
	Observation             protocol.Quantity `json:"observation"`
	Craftable               *bool             `json:"craftable"`
	CraftableObservation    protocol.Quantity `json:"craftable_observation"`
	Verification            string            `json:"verification,omitempty"`
	VerificationObservation protocol.Quantity `json:"verification_observation,omitempty"`
	Label                   *string           `json:"label"`
}

func digest(value any) ([32]byte, error) {
	data, err := json.Marshal(value)
	return sha256.Sum256(data), err
}

func (s *Store) Ingest(ctx context.Context, d Device, b protocol.InventoryBatch) (protocol.InventoryAck, error) {
	ack := protocol.InventoryAck{Bindings: []protocol.Binding{}}
	if err := b.Validate(s.options.Limits.BatchItems); err != nil {
		return ack, errors.Join(ErrInvalid, err)
	}
	hash, err := digest(b)
	if err != nil {
		return ack, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := lockNetwork(ctx, tx, d, b.SessionID); err != nil {
			return err
		}
		var last protocol.Quantity
		var oldHash, response, capsJSON []byte
		var responseBytes int
		if err := tx.QueryRow(ctx, "SELECT inventory_seq,inventory_hash,inventory_response,capabilities,response_bytes FROM bridge_sessions WHERE id=$1", b.SessionID).Scan(&last, &oldHash, &response, &capsJSON, &responseBytes); err != nil {
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
		limits := s.negotiatedLimits(responseBytes)
		if len(b.Updates) > limits.BatchItems {
			return ErrCapacity
		}
		var caps protocol.Capabilities
		if err := json.Unmarshal(capsJSON, &caps); err != nil {
			return err
		}
		if !caps.Inventory {
			return ErrCapability
		}
		ids, bindings, err := s.resolveDefinitions(ctx, tx, d.NetworkID, b.Updates)
		if err != nil {
			return err
		}
		ack.Bindings = bindings
		merged := map[protocol.Quantity]inventoryRow{}
		for i, u := range b.Updates {
			row := inventoryRow{ID: ids[i], Quantity: u.Quantity, Observation: u.Observation, Craftable: u.Craftable}
			if u.Craftable != nil {
				row.CraftableObservation = u.Observation
				if u.Verification != "" {
					row.Verification, row.VerificationObservation = u.Verification, u.Observation
				}
			}
			if u.Item != nil {
				label := u.Item.Label
				if label == "" {
					label = u.Item.Name
				}
				row.Label = &label
			}
			previous, exists := merged[row.ID]
			if exists {
				if previous.Observation == row.Observation && previous.Quantity != row.Quantity {
					return ErrConflict
				}
				if previous.CraftableObservation == row.CraftableObservation && previous.Craftable != nil && row.Craftable != nil && *previous.Craftable != *row.Craftable {
					return ErrConflict
				}
				// Counts and recipe flags may be observed independently. Keep the
				// newest known flag even when a later count has no recipe data.
				flag, observation := previous.Craftable, previous.CraftableObservation
				if row.CraftableObservation > observation {
					flag, observation = row.Craftable, row.CraftableObservation
				}
				confirmation, confirmedAt := previous.Verification, previous.VerificationObservation
				if row.VerificationObservation == confirmedAt && row.Verification != "" && confirmation != "" && row.Verification != confirmation {
					return ErrConflict
				}
				if row.VerificationObservation > confirmedAt {
					confirmation, confirmedAt = row.Verification, row.VerificationObservation
				}
				if row.Observation <= previous.Observation {
					row = previous
				}
				row.Craftable, row.CraftableObservation = flag, observation
				row.Verification, row.VerificationObservation = confirmation, confirmedAt
			}
			merged[row.ID] = row
		}
		input := make([]inventoryRow, 0, len(merged))
		for _, row := range merged {
			input = append(input, row)
		}
		sort.Slice(input, func(i, j int) bool { return input[i].ID < input[j].ID })
		var accepted int
		var changed bool
		if len(input) > 0 {
			encoded, _ := json.Marshal(input)
			err = tx.QueryRow(ctx, inventoryUpsert, d.NetworkID, encoded, b.SessionID, s.options.RecheckAfter.Seconds()).Scan(&accepted, &changed)
			if err != nil {
				return err
			}
		}
		if err = tx.QueryRow(ctx, `UPDATE networks SET last_seen=now(),
            last_inventory=CASE WHEN $2::int>0 THEN now() ELSE last_inventory END,
            last_scan=CASE WHEN $3 THEN now() ELSE last_scan END,
            revision=revision+CASE WHEN $4 THEN 1 ELSE 0 END WHERE id=$1 RETURNING revision`, d.NetworkID, accepted, b.ScanCompleted, changed).Scan(&ack.Revision); err != nil {
			return err
		}
		ack.Ack = b.Sequence
		encoded, _ := json.Marshal(ack)
		if len(encoded) > responseBytes {
			return ErrCapacity
		}
		if _, err = tx.Exec(ctx, "UPDATE bridge_sessions SET inventory_seq=$2,inventory_hash=$3,inventory_response=$4 WHERE id=$1", b.SessionID, int64(b.Sequence), hash[:], encoded); err != nil {
			return err
		}
		if changed || b.ScanCompleted {
			return s.event(ctx, tx, d.NetworkID, "inventory")
		}
		return nil
	})
	return ack, err
}

func (s *Store) resolveDefinitions(ctx context.Context, tx pgx.Tx, networkID string, updates []protocol.Update) ([]protocol.Quantity, []protocol.Binding, error) {
	definitions := map[string]protocol.Definition{}
	for _, u := range updates {
		if u.Item != nil {
			definitions[u.Item.Key()] = *u.Item
		}
	}
	resolved := map[string]protocol.Quantity{}
	if len(definitions) > 0 {
		input := make([]definitionRow, 0, len(definitions))
		keys := make([]string, 0, len(definitions))
		for key, d := range definitions {
			if d.Label == "" {
				d.Label = d.Name
			}
			input = append(input, definitionRow{Key: key, Definition: d})
			keys = append(keys, key)
		}
		sort.Slice(input, func(i, j int) bool { return input[i].Key < input[j].Key })
		encoded, _ := json.Marshal(input)
		if _, err := tx.Exec(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,nbt,label,exact)
            SELECT $1,identity_key,kind,name,damage,coalesce(nbt,''),label,exact FROM jsonb_to_recordset($2::jsonb)
            AS x(identity_key text,kind text,name text,damage int,nbt text,label text,exact boolean)
            ON CONFLICT(network_id,identity_key) DO NOTHING`, networkID, encoded); err != nil {
			return nil, nil, err
		}
		rows, err := tx.Query(ctx, "SELECT identity_key,id FROM item_definitions WHERE network_id=$1 AND identity_key=ANY($2::text[])", networkID, keys)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var key string
			var id protocol.Quantity
			if err = rows.Scan(&key, &id); err != nil {
				rows.Close()
				return nil, nil, err
			}
			resolved[key] = id
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	ids := make([]protocol.Quantity, len(updates))
	bindings := []protocol.Binding{}
	unique := map[int64]bool{}
	for i, u := range updates {
		ids[i] = u.ID
		if u.Item != nil {
			ids[i] = resolved[u.Item.Key()]
			bindings = append(bindings, protocol.Binding{Index: i + 1, ID: ids[i]})
		}
		unique[int64(ids[i])] = true
	}
	if len(unique) > 0 {
		values := make([]int64, 0, len(unique))
		for id := range unique {
			values = append(values, id)
		}
		var found int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM item_definitions WHERE network_id=$1 AND id=ANY($2::bigint[])", networkID, values).Scan(&found); err != nil {
			return nil, nil, err
		}
		if found != len(unique) {
			return nil, nil, ErrInvalid
		}
	}
	return ids, bindings, nil
}

// Metadata changes only when the associated observation is accepted. The CTE
// separates freshness writes from changes that invalidate sorted browser pages.
const inventoryUpsert = `WITH input AS (
    SELECT * FROM jsonb_to_recordset($2::jsonb)
    AS x(id bigint,quantity bigint,observation bigint,craftable boolean,craftable_observation bigint,label text,verification text,verification_observation bigint)
), observations AS (
    SELECT x.id,x.quantity,x.observation,x.label,x.verification,x.verification_observation,
        CASE WHEN x.craftable IS NOT NULL AND (i.item_id IS NULL OR i.session_id<>$3 OR x.craftable_observation>i.observation)
            THEN x.craftable WHEN i.session_id=$3 THEN i.craftable ELSE false END AS craftable,
        i.quantity AS previous_quantity,i.craftable AS previous_craftable,i.session_id AS previous_session,
        i.observation AS previous_observation,d.label AS previous_label
    FROM input x JOIN item_definitions d ON d.network_id=$1 AND d.id=x.id
    LEFT JOIN inventory i ON i.network_id=$1 AND i.item_id=x.id
    WHERE i.item_id IS NULL OR i.session_id<>$3 OR x.observation>i.observation
), eligible AS MATERIALIZED (
    SELECT o.*, (previous_quantity IS DISTINCT FROM quantity OR previous_craftable IS DISTINCT FROM craftable
        OR previous_session IS DISTINCT FROM $3 OR (label IS NOT NULL AND previous_label IS DISTINCT FROM label)) AS changed
    FROM observations o
), written AS (
    INSERT INTO inventory(network_id,item_id,quantity,craftable,session_id,observation,observed_at,next_verify_at)
    SELECT $1,id,quantity,craftable,$3,observation,now(),now()+$4::double precision*interval '1 second' FROM eligible
    ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=excluded.quantity,craftable=excluded.craftable,
        session_id=excluded.session_id,observation=excluded.observation,observed_at=excluded.observed_at,next_verify_at=excluded.next_verify_at
    RETURNING item_id
), labels AS (
    UPDATE item_definitions d SET label=e.label,updated_at=now() FROM eligible e
    WHERE d.network_id=$1 AND d.id=e.id AND e.label IS NOT NULL AND d.label IS DISTINCT FROM e.label RETURNING d.id
), confirmed AS (
    DELETE FROM inventory_rechecks r USING eligible e
    WHERE r.network_id=$1 AND r.item_id=e.id AND r.token=e.verification AND e.verification_observation>0
      AND (e.previous_session IS DISTINCT FROM $3 OR e.verification_observation>e.previous_observation)
    RETURNING r.item_id
)
SELECT (SELECT count(*) FROM written),coalesce((SELECT bool_or(changed) FROM eligible),false)`
