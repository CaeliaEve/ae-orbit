package store

import (
	"context"
	"encoding/json"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// Resolve the original identity before leasing a notification. The translated
// display text is persisted with the delivery; OC observations stay untouched.
func (s *Store) localizeQQDelivery(ctx context.Context, tx pgx.Tx, id string) error {
	if s.catalog == nil {
		return nil
	}
	var payload, definition, output []byte
	err := tx.QueryRow(ctx, `SELECT q.payload,
 CASE WHEN d.id IS NOT NULL THEN jsonb_build_object('kind',d.kind,'name',d.name,'damage',d.damage,'nbt',d.nbt,'label',d.label) END,
 coalesce(q.payload->'output',t.cpu->'output',r.cpu_fingerprint->'output')
 FROM qq_outbox q
 LEFT JOIN qq_reminders r ON r.network_id=q.network_id AND split_part(q.source_key,':',1)='reminder' AND r.id=split_part(q.source_key,':',2)
 LEFT JOIN production_job_entries e ON e.network_id=q.network_id AND e.id=coalesce(r.entry_id,CASE WHEN split_part(q.source_key,':',1)='entry' THEN split_part(q.source_key,':',2) END)
 LEFT JOIN item_definitions d ON d.network_id=q.network_id AND d.id=coalesce(e.item_id,
 CASE WHEN q.payload->>'item_id' ~ '^[1-9][0-9]{0,18}$' THEN
  CASE WHEN (q.payload->>'item_id')::numeric<=9223372036854775807 THEN (q.payload->>'item_id')::bigint END END)
 LEFT JOIN qq_cpu_targets t ON t.id=r.cpu_target_id
 WHERE q.id=$1 AND q.kind IN ('craft','cpu_end','exception') AND q.state='queued'`, id).Scan(&payload, &definition, &output)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var content map[string]json.RawMessage
	if err = json.Unmarshal(payload, &content); err != nil {
		return err
	}
	name := ""
	if len(definition) > 0 {
		var item protocol.Definition
		if err = json.Unmarshal(definition, &item); err != nil {
			return err
		}
		name = s.catalog.DefinitionLabel(item)
	} else if len(output) > 0 && string(output) != "null" {
		var stack protocol.Stack
		if err = json.Unmarshal(output, &stack); err != nil {
			return err
		}
		// Old fingerprints stored the visual label separately in the payload.
		if stack.Label == "" {
			if err = json.Unmarshal(content["item"], &stack.Label); err != nil && len(content["item"]) > 0 {
				return err
			}
		}
		name = s.catalog.CPUOutputLabel(stack)
	} else {
		return nil
	} // Without an original identity, retain the observed name.
	if name == "" {
		return nil
	}
	if _, ok := content["item_original"]; !ok {
		content["item_original"] = content["item"]
	}
	content["item"], err = json.Marshal(name)
	if err != nil {
		return err
	}
	updated, err := json.Marshal(content)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE qq_outbox SET payload=$2 WHERE id=$1 AND state='queued'`, id, updated)
	return err
}
