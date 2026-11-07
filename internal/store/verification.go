package store

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

//go:embed verification_candidates.sql
var verificationCandidates string

func (s *Store) verifications(ctx context.Context, tx pgx.Tx, networkID string, responseBytes, slots, queueBytes int, ack *protocol.PollAck) error {
	window := 0
	if slots > 0 {
		// Rotate a bounded window even when recent observations satisfy every
		// candidate. Later polls then reach the colder entries behind it.
		window = 128
	}
	rows, err := tx.Query(ctx, verificationCandidates, networkID, window, slots, s.options.RecheckAfter.Seconds())
	if err != nil {
		return err
	}
	candidates := make([]protocol.Verification, 0, 16)
	for rows.Next() {
		var v protocol.Verification
		if err = rows.Scan(&v.ID, &v.Item.Kind, &v.Item.Name, &v.Item.Damage, &v.Item.NBT, &v.Item.Label, &v.Item.Exact, &v.Craftable, &v.Token); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var ordinary, shortRetry, priority []int64
	for _, v := range candidates {
		// Match the OC FIFO's two identity keys, display label and fixed
		// descriptor allowance. DecodedLen conservatively includes padding.
		cost := 2*(len(v.Item.Name)+len(strconv.Itoa(v.Item.Damage))+2+base64.StdEncoding.DecodedLen(len(v.Item.NBT))) + len(v.Item.Label) + 256
		if v.Token == "" {
			ordinary = append(ordinary, int64(v.ID))
			if cost > queueBytes {
				shortRetry = append(shortRetry, int64(v.ID))
				continue
			}
		}
		ack.Verify = append(ack.Verify, v)
		encoded, _ := json.Marshal(ack)
		if len(encoded) > responseBytes {
			ack.Verify = ack.Verify[:len(ack.Verify)-1]
			if v.Token == "" {
				shortRetry = append(shortRetry, int64(v.ID))
				continue
			}
		}
		if v.Token != "" {
			// Sending, or failing to fit, never completes a priority read.
			priority = append(priority, int64(v.ID))
		} else {
			queueBytes -= cost
		}
	}
	if len(ordinary) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE inventory_schedule SET next_verify_at=now()+
            CASE WHEN item_id=ANY($2::bigint[]) THEN interval '5 seconds'
                 ELSE $4::double precision*interval '1 second' END
            WHERE network_id=$1 AND item_id=ANY($3::bigint[])`, networkID, shortRetry, ordinary, s.options.RecheckAfter.Seconds()); err != nil {
			return err
		}
	}
	if len(priority) > 0 {
		_, err = tx.Exec(ctx, `UPDATE inventory_rechecks SET next_attempt_at=now()+interval '5 seconds'
            WHERE network_id=$1 AND item_id=ANY($2::bigint[])`, networkID, priority)
	}
	return err
}

func scheduleRecheck(ctx context.Context, tx pgx.Tx, networkID string, itemID protocol.Quantity) error {
	_, err := tx.Exec(ctx, `INSERT INTO inventory_rechecks(network_id,item_id,token)
        SELECT network_id,item_id,$3 FROM inventory WHERE network_id=$1 AND item_id=$2
        ON CONFLICT(network_id,item_id) DO UPDATE SET token=excluded.token,next_attempt_at=now()`, networkID, int64(itemID), newID())
	return err
}
