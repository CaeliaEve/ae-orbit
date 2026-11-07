package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Poll(ctx context.Context, d Device, p protocol.Poll) (protocol.PollAck, error) {
	ack := protocol.PollAck{Commands: []protocol.Command{}, Verify: []protocol.Verification{}, PollMillis: s.options.Limits.PollMillis}
	if err := p.Validate(s.options.Limits.CPUs, 16); err != nil {
		return ack, errors.Join(ErrInvalid, err)
	}
	hash, err := digest(p)
	if err != nil {
		return ack, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		group, err := lockNetwork(ctx, tx, d, p.SessionID)
		if err != nil {
			return err
		}
		var last protocol.Quantity
		var oldHash, response, capsJSON []byte
		var responseBytes int
		if err = tx.QueryRow(ctx, "SELECT poll_seq,poll_hash,poll_response,capabilities,response_bytes FROM bridge_sessions WHERE id=$1", p.SessionID).Scan(&last, &oldHash, &response, &capsJSON, &responseBytes); err != nil {
			return err
		}
		if p.Sequence == last {
			if !bytes.Equal(oldHash, hash[:]) {
				return ErrSequence
			}
			if err = json.Unmarshal(response, &ack); err != nil {
				return err
			}
			// A confirmation can be replayed, but a command that became uncertain
			// or terminal while its response was lost must not be reissued.
			live := []protocol.Command{}
			for _, c := range ack.Commands {
				var valid bool
				if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1 AND attempt_id=$2 AND attempt_session=$3 AND state IN ('assigned','computing','running'))", c.OrderID, c.AttemptID, p.SessionID).Scan(&valid); err != nil {
					return err
				}
				if valid {
					live = append(live, c)
				}
			}
			ack.Commands = live
			_, err = tx.Exec(ctx, "UPDATE networks SET last_seen=now() WHERE id=$1", d.NetworkID)
			return err
		}
		if p.Sequence != last+1 {
			return ErrSequence
		}
		if _, err = tx.Exec(ctx, "SELECT name FROM server_groups WHERE name=$1 FOR UPDATE", group); err != nil {
			return err
		}
		var caps protocol.Capabilities
		if err = json.Unmarshal(capsJSON, &caps); err != nil {
			return err
		}
		if err = s.acceptReports(ctx, tx, d.NetworkID, p); err != nil {
			return err
		}
		if err = s.expireQueued(ctx, tx, d.NetworkID); err != nil {
			return err
		}
		if p.CPUs != nil {
			if err = s.saveCPUs(ctx, tx, d.NetworkID, p); err != nil {
				return err
			}
		}
		if p.Health != nil {
			health, _ := json.Marshal(p.Health)
			// Sparse diagnostic snapshots avoid resending unchanged details.
			// Retain only this field; ordinary health always reflects this poll.
			if _, err = tx.Exec(ctx, `UPDATE networks SET health=CASE
				WHEN health ? 'read_failures' AND NOT ($2::jsonb ? 'read_failures')
				THEN $2::jsonb || jsonb_build_object('read_failures',health->'read_failures')
				ELSE $2::jsonb END WHERE id=$1`, d.NetworkID, health); err != nil {
				return err
			}
		}
		ack.Ack = p.Sequence
		if caps.Inventory {
			tag, err := tx.Exec(ctx, "UPDATE server_groups SET next_scan_at=now()+$2::double precision*interval '1 millisecond' WHERE name=$1 AND next_scan_at<=now()", group, s.options.Limits.PollMillis)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				ack.ScanBudget = s.options.ScanItems
			}
		}
		if caps.Craft {
			if err = s.assignCommands(ctx, tx, d.NetworkID, group, p.SessionID, caps, responseBytes, &ack); err != nil {
				return err
			}
		}
		if caps.Inventory {
			slots, queueBytes := 16, 1<<30
			if p.RecheckSlots != nil {
				slots = *p.RecheckSlots
			}
			if p.RecheckBytes != nil {
				queueBytes = *p.RecheckBytes
			}
			if queueBytes == 0 {
				slots = 0
			}
			if err = s.verifications(ctx, tx, d.NetworkID, responseBytes, slots, queueBytes, &ack); err != nil {
				return err
			}
		}
		encoded, _ := json.Marshal(ack)
		if len(encoded) > responseBytes {
			return ErrCapacity
		}
		if _, err = tx.Exec(ctx, "UPDATE bridge_sessions SET poll_seq=$2,poll_hash=$3,poll_response=$4 WHERE id=$1", p.SessionID, int64(p.Sequence), hash[:], encoded); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE networks SET last_seen=now() WHERE id=$1", d.NetworkID)
		return err
	})
	return ack, err
}

func (s *Store) acceptReports(ctx context.Context, tx pgx.Tx, networkID string, p protocol.Poll) error {
	changed := false
	for _, r := range p.Reports {
		var state, attempt, session string
		var itemID protocol.Quantity
		if err := tx.QueryRow(ctx, "SELECT state,attempt_id,attempt_session,item_id FROM orders WHERE network_id=$1 AND id=$2 FOR UPDATE", networkID, r.OrderID).Scan(&state, &attempt, &session, &itemID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrConflict
			}
			return err
		}
		if attempt != r.AttemptID || session != p.SessionID {
			return ErrConflict
		}
		// Transport can recover while the same process still holds a live AE
		// handle. Acknowledge intermediate observations without clearing review;
		// terminal evidence from that same attempt may resolve it later.
		if state == "needs_review" && (r.State == "computing" || r.State == "running") {
			continue
		}
		if !protocol.CanTransition(state, r.State) && !(state == "needs_review" && protocol.Terminal(r.State)) {
			return ErrConflict
		}
		if state == r.State {
			continue
		}
		if _, err := tx.Exec(ctx, "UPDATE orders SET state=$2,reason=$3,updated_at=now() WHERE id=$1", r.OrderID, r.State, r.Reason); err != nil {
			return err
		}
		if err := auditOrder(ctx, tx, r.OrderID); err != nil {
			return err
		}
		if protocol.Terminal(r.State) {
			if err := scheduleRecheck(ctx, tx, networkID, itemID); err != nil {
				return err
			}
		}
		changed = true
	}
	if changed {
		return s.event(ctx, tx, networkID, "orders")
	}
	return nil
}

func (s *Store) assignCommands(ctx context.Context, tx pgx.Tx, networkID, group, session string, caps protocol.Capabilities, responseBytes int, ack *protocol.PollAck) error {
	var active, planning int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM orders WHERE network_id=$1 AND state IN ('assigned','computing','running','needs_review')", networkID).Scan(&active); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders o JOIN networks n ON n.id=o.network_id WHERE n.server_group=$1 AND o.state IN ('assigned','computing','needs_review')`, group).Scan(&planning); err != nil {
		return err
	}
	available := min(s.options.ActiveWindow-active, s.options.GroupPlanning-planning, s.options.Limits.Commands)
	if available <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT o.id,o.item_id,o.quantity,i.quantity,i.craftable,d.kind,d.name,d.damage,d.nbt,d.label,d.exact
        FROM orders o JOIN item_definitions d ON d.network_id=o.network_id AND d.id=o.item_id
        JOIN inventory i ON i.network_id=o.network_id AND i.item_id=o.item_id
        WHERE o.network_id=$1 AND o.state='queued' AND o.expires_at>now() ORDER BY o.created_at,o.id LIMIT $2 FOR UPDATE OF o SKIP LOCKED`, networkID, available)
	if err != nil {
		return err
	}
	type candidate struct {
		command   protocol.Command
		craftable bool
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.command.OrderID, &c.command.ItemID, &c.command.Quantity, &c.command.CurrentQuantity, &c.craftable, &c.command.Item.Kind, &c.command.Item.Name, &c.command.Item.Damage, &c.command.Item.NBT, &c.command.Item.Label, &c.command.Item.Exact); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	changed := false
	for _, candidate := range candidates {
		c := candidate.command
		c.AttemptID = newID()
		reason := ""
		if !candidate.craftable || !c.Item.Exact || c.Quantity > caps.MaxCraftQuantity {
			reason = "crafting capability changed"
		}
		ack.Commands = append(ack.Commands, c)
		encoded, _ := json.Marshal(ack)
		if len(encoded) > responseBytes {
			reason = "command exceeds device response budget"
		}
		if reason != "" {
			ack.Commands = ack.Commands[:len(ack.Commands)-1]
			if _, err = tx.Exec(ctx, "UPDATE orders SET state='failed',reason=$2,updated_at=now() WHERE id=$1", c.OrderID, reason); err != nil {
				return err
			}
		} else {
			if _, err = tx.Exec(ctx, "UPDATE orders SET state='assigned',attempt_id=$2,attempt_session=$3,updated_at=now() WHERE id=$1", c.OrderID, c.AttemptID, session); err != nil {
				return err
			}
		}
		if err = auditOrder(ctx, tx, c.OrderID); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return s.event(ctx, tx, networkID, "orders")
	}
	return nil
}

func (s *Store) saveCPUs(ctx context.Context, tx pgx.Tx, networkID string, p protocol.Poll) error {
	encoded, _ := json.Marshal(*p.CPUs)
	if string(encoded) == "null" {
		encoded = []byte("[]")
	}
	var changed bool
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM cpu_snapshots WHERE network_id=$1 AND session_id=$2 AND cpus=$3::jsonb AND truncated=$4)`, networkID, p.SessionID, encoded, p.CPUsTruncated).Scan(&changed); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cpu_snapshots(network_id,session_id,cpus,truncated) VALUES($1,$2,$3,$4)
        ON CONFLICT(network_id) DO UPDATE SET session_id=excluded.session_id,cpus=excluded.cpus,truncated=excluded.truncated,observed_at=now()`, networkID, p.SessionID, encoded, p.CPUsTruncated); err != nil {
		return err
	}
	if changed {
		return s.event(ctx, tx, networkID, "cpus")
	}
	return nil
}

type CPUView struct {
	SessionID  string         `json:"session_id"`
	CPUs       []protocol.CPU `json:"cpus"`
	Truncated  bool           `json:"truncated"`
	ObservedAt *time.Time     `json:"observed_at,omitempty"`
}

func (s *Store) CPUs(ctx context.Context, userID, networkID string) (CPUView, error) {
	view := CPUView{CPUs: []protocol.CPU{}}
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return view, err
	}
	var data []byte
	err := s.pool.QueryRow(ctx, "SELECT session_id,cpus,truncated,observed_at FROM cpu_snapshots WHERE network_id=$1", networkID).Scan(&view.SessionID, &data, &view.Truncated, &view.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	return view, json.Unmarshal(data, &view.CPUs)
}
