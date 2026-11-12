package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) PollV2(ctx context.Context, d Device, p protocol.Poll) (protocol.PollAck, error) {
	ack := protocol.PollAck{Commands: []protocol.Command{}, Reads: []protocol.ReadAssignment{}, PollMillis: s.options.Limits.PollMillis}
	if err := p.ValidateV2(s.options.Limits.CPUs, 16); err != nil {
		return ack, errors.Join(ErrInvalid, err)
	}
	hash, err := digest(p)
	if err != nil {
		return ack, err
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		group, err := lockDevice(ctx, tx, d, p.SessionID, 2)
		if err != nil {
			return err
		}
		if d.Role != "primary" && (p.CPUs != nil || len(p.Reports) > 0 || len(p.Hints) > 0 || p.CPUsTruncated) {
			return ErrForbidden
		}
		var last protocol.Quantity
		var oldHash, response, capsJSON []byte
		var responseBytes int
		if err = tx.QueryRow(ctx, `SELECT poll_seq,poll_hash,poll_response,capabilities,response_bytes
			FROM bridge_sessions WHERE id=$1`, p.SessionID).Scan(&last, &oldHash, &response, &capsJSON, &responseBytes); err != nil {
			return err
		}
		if p.Sequence == last {
			if !bytes.Equal(oldHash, hash[:]) {
				return ErrSequence
			}
			if err = json.Unmarshal(response, &ack); err != nil {
				return err
			}
			if err = s.filterCooperativeReplay(ctx, tx, d, p.SessionID, &ack); err != nil {
				return err
			}
			return touchDevice(ctx, tx, d, nil)
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
		if d.Role == "primary" {
			if err = s.acceptReports(ctx, tx, d.NetworkID, p); err != nil {
				return err
			}
			if err = s.expireQueued(ctx, tx, d.NetworkID); err != nil {
				return err
			}
			if err = s.admitProductionJobs(ctx, tx, d.NetworkID, caps); err != nil {
				return err
			}
			if p.CPUs != nil {
				if err = s.saveCPUs(ctx, tx, d.NetworkID, p); err != nil {
					return err
				}
			}
			if err = s.acceptReadHints(ctx, tx, d.NetworkID, p.Hints); err != nil {
				return err
			}
			if caps.Craft {
				if err = s.assignCommands(ctx, tx, d.NetworkID, group, p.SessionID, caps, responseBytes, &ack); err != nil {
					return err
				}
			}
		}
		ack.Ack = p.Sequence
		slots, queueBytes := 0, 0
		if caps.Inventory && (caps.ExactQuantities || d.Role == "primary") && p.ReadSlots != nil && p.ReadBytes != nil {
			slots, queueBytes = *p.ReadSlots, *p.ReadBytes
		}
		ack.ScanBudget, ack.GrantID, ack.GrantTTLMillis, err = s.acquisitionGrant(ctx, tx, d, p.SessionID, group, slots, queueBytes, 2)
		if err != nil {
			return err
		}
		if caps.ExactQuantities && ack.ScanBudget > 0 && slots > 0 && queueBytes > 0 {
			if err = s.cooperativeReads(ctx, tx, d, p.SessionID, responseBytes, slots, queueBytes, &ack); err != nil {
				return err
			}
		}
		if err = touchDevice(ctx, tx, d, p.Health); err != nil {
			return err
		}
		encoded, _ := json.Marshal(ack)
		if len(encoded) > responseBytes {
			return ErrCapacity
		}
		_, err = tx.Exec(ctx, "UPDATE bridge_sessions SET poll_seq=$2,poll_hash=$3,poll_response=$4 WHERE id=$1",
			p.SessionID, int64(p.Sequence), hash[:], encoded)
		return err
	})
	return ack, err
}

func touchDevice(ctx context.Context, tx pgx.Tx, d Device, health *protocol.Health) error {
	var encoded []byte
	if health != nil {
		encoded, _ = json.Marshal(health)
	}
	_, err := tx.Exec(ctx, `UPDATE devices SET last_seen=now(),health=CASE WHEN $2::jsonb IS NULL THEN health
		ELSE $2::jsonb
		|| CASE WHEN health ? 'read_failures' AND NOT ($2::jsonb ? 'read_failures')
			THEN jsonb_build_object('read_failures',health->'read_failures') ELSE '{}'::jsonb END
		|| CASE WHEN health ? 'sync' AND NOT ($2::jsonb ? 'sync')
			THEN jsonb_build_object('sync',health->'sync') ELSE '{}'::jsonb END END
		WHERE id=$1`, d.ID, encoded)
	if err != nil || d.Role != "primary" {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE networks n SET last_seen=d.last_seen,health=d.health
		FROM devices d WHERE d.id=$1 AND n.id=d.network_id`, d.ID)
	return err
}

func (s *Store) acceptReadHints(ctx context.Context, tx pgx.Tx, network string, hints []protocol.ReadHint) error {
	for _, hint := range hints {
		var id protocol.Quantity
		var err error
		if hint.ID > 0 {
			err = tx.QueryRow(ctx, `SELECT coalesce(a.canonical_id,d.id) FROM item_definitions d
				LEFT JOIN item_aliases a ON a.network_id=d.network_id AND a.alias_id=d.id
				WHERE d.network_id=$1 AND d.id=$2`, network, int64(hint.ID)).Scan(&id)
		} else {
			err = tx.QueryRow(ctx, `SELECT coalesce(a.canonical_id,d.id) FROM item_definitions d
				LEFT JOIN item_aliases a ON a.network_id=d.network_id AND a.alias_id=d.id
				WHERE d.network_id=$1 AND d.identity_key=$2`, network, hint.Item.Key()).Scan(&id)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if hint.ID > 0 {
				return ErrInvalid
			}
			continue
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE inventory_schedule SET next_quantity_at=least(next_quantity_at,now())
			WHERE network_id=$1 AND item_id=$2`, network, int64(id)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) filterCooperativeReplay(ctx context.Context, tx pgx.Tx, d Device, session string, ack *protocol.PollAck) error {
	commands := []protocol.Command{}
	for _, c := range ack.Commands {
		var live bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE network_id=$1 AND id=$2
			AND attempt_id=$3 AND attempt_session=$4 AND state IN ('assigned','computing','running'))`,
			d.NetworkID, c.OrderID, c.AttemptID, session).Scan(&live); err != nil {
			return err
		}
		if live {
			commands = append(commands, c)
		}
	}
	ack.Commands = commands
	reads := []protocol.ReadAssignment{}
	for _, r := range ack.Reads {
		var ttl int
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*),coalesce(min(greatest(0,floor(extract(epoch FROM expires_at-now())*1000)))::int,0)
			FROM cooperative_read_fields WHERE network_id=$1 AND item_id=$2 AND device_id=$3 AND session_id=$4
			AND assignment_id=$5 AND completed_at IS NULL AND expires_at>now()`, d.NetworkID, int64(r.ID), d.ID, session, r.AssignmentID).Scan(&count, &ttl); err != nil {
			return err
		}
		expected := 1
		if r.Kind == "quantity_recipe" {
			expected = 2
		}
		if count == expected && ttl > 0 {
			r.TTLMillis = min(r.TTLMillis, ttl)
			reads = append(reads, r)
		}
	}
	ack.Reads = reads
	var ttl int
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT greatest(0,floor(extract(epoch FROM grant_until-now())*1000))::int
		FROM acquisition_intents WHERE device_id=$1 AND session_id=$2 AND grant_id=$3),0)`, d.ID, session, ack.GrantID).Scan(&ttl); err != nil {
		return err
	}
	if ttl <= 0 {
		ack.ScanBudget, ack.GrantTTLMillis, ack.GrantID = 0, 0, ""
	} else {
		ack.GrantTTLMillis = min(ack.GrantTTLMillis, ttl)
	}
	return nil
}

type cooperativeCandidate struct {
	protocol.ReadAssignment
	RequestID string
	ExpiresAt time.Time
}

const noActiveCooperativeQuantity = `NOT EXISTS(SELECT 1 FROM cooperative_read_fields f
	JOIN devices owner ON owner.id=f.device_id AND owner.current_session=f.session_id AND NOT owner.revoked
	JOIN bridge_sessions bs ON bs.id=f.session_id AND NOT bs.retired AND bs.protocol=2
	WHERE f.network_id=d.network_id AND f.item_id=d.id AND f.field='quantity'
	AND f.completed_at IS NULL AND f.expires_at>now())`

const noActiveCooperativeRecipe = `NOT EXISTS(SELECT 1 FROM cooperative_read_fields f
	JOIN devices owner ON owner.id=f.device_id AND owner.current_session=f.session_id AND NOT owner.revoked
	JOIN bridge_sessions bs ON bs.id=f.session_id AND NOT bs.retired AND bs.protocol=2
	WHERE f.network_id=d.network_id AND f.item_id=d.id AND f.field='recipe'
	AND f.completed_at IS NULL AND f.expires_at>now())`

func (s *Store) cooperativeCandidates(ctx context.Context, tx pgx.Tx, d Device, category string, recipe bool) ([]cooperativeCandidate, error) {
	columns := `d.id,d.kind,d.name,d.damage,d.nbt,d.label,d.exact,`
	var query, kind string
	kind = "quantity"
	switch category {
	case "order":
		if d.Role != "primary" {
			return nil, nil
		}
		kind = "quantity_recipe"
		query = `SELECT ` + columns + `r.token,now()+interval '30 seconds'
			FROM inventory_rechecks r JOIN item_definitions d ON d.network_id=r.network_id AND d.id=r.item_id
			WHERE r.network_id=$1 AND d.kind='item' AND d.exact AND r.next_attempt_at<=now() AND ` + noActiveCooperativeQuantity + ` AND ` + noActiveCooperativeRecipe + `
			ORDER BY r.next_attempt_at,r.item_id LIMIT 32`
	case "refresh":
		query = `SELECT ` + columns + `r.token,least(r.expires_at,now()+interval '30 seconds')
			FROM inventory_refreshes r JOIN item_definitions d ON d.network_id=r.network_id AND d.id=r.item_id
			WHERE r.network_id=$1 AND d.kind='item' AND d.exact AND r.confirmed_at IS NULL AND r.expires_at>now()+interval '1 millisecond'
			AND r.next_attempt_at<=now() AND ` + noActiveCooperativeQuantity + `
			ORDER BY r.next_attempt_at,r.item_id LIMIT 32`
	default:
		due := "q.next_quantity_at"
		if recipe {
			if d.Role != "primary" {
				return nil, nil
			}
			due, kind = "q.next_verify_at", "quantity_recipe"
		}
		query = `SELECT ` + columns + `'',now()+interval '30 seconds'
			FROM inventory_schedule q JOIN item_definitions d ON d.network_id=q.network_id AND d.id=q.item_id
			WHERE q.network_id=$1 AND d.kind='item' AND d.exact AND ` + due + `<=now() AND ` + noActiveCooperativeQuantity + `
			AND NOT EXISTS(SELECT 1 FROM inventory_refreshes hot WHERE hot.network_id=q.network_id
				AND hot.item_id=q.item_id AND hot.confirmed_at IS NULL AND hot.expires_at>now())
			AND NOT EXISTS(SELECT 1 FROM inventory_rechecks urgent WHERE urgent.network_id=q.network_id AND urgent.item_id=q.item_id)`
		if recipe {
			query += ` AND ` + noActiveCooperativeRecipe
		}
		query += ` ORDER BY ` + due + `,q.item_id LIMIT 64`
	}
	rows, err := tx.Query(ctx, query, d.NetworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cooperativeCandidate{}
	for rows.Next() {
		c := cooperativeCandidate{}
		c.Kind, c.Purpose = kind, category
		if err = rows.Scan(&c.ID, &c.Item.Kind, &c.Item.Name, &c.Item.Damage, &c.Item.NBT,
			&c.Item.Label, &c.Item.Exact, &c.RequestID, &c.ExpiresAt); err != nil {
			return nil, err
		}
		if c.Item.Kind == "item" && c.Item.Exact {
			result = append(result, c)
		}
	}
	return result, rows.Err()
}

func (s *Store) cooperativeReads(ctx context.Context, tx pgx.Tx, d Device, session string, responseBytes, slots, queueBytes int, ack *protocol.PollAck) error {
	var owned, fastOwned, networkActive int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE f.device_id=$2),
		count(*) FILTER(WHERE f.device_id=$2 AND f.purpose<>'background'),count(*)
		FROM cooperative_read_fields f JOIN devices owner ON owner.id=f.device_id
		AND owner.current_session=f.session_id AND NOT owner.revoked
		JOIN bridge_sessions bs ON bs.id=f.session_id AND NOT bs.retired AND bs.protocol=2
		WHERE f.network_id=$1 AND f.field='quantity' AND f.completed_at IS NULL AND f.expires_at>now()`,
		d.NetworkID, d.ID).Scan(&owned, &fastOwned, &networkActive); err != nil {
		return err
	}
	slots = min(slots, 16-owned, 128-networkActive)
	if slots <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, "INSERT INTO cooperative_rotation(network_id) VALUES($1) ON CONFLICT DO NOTHING", d.NetworkID); err != nil {
		return err
	}
	var opportunity, background int
	if err := tx.QueryRow(ctx, "SELECT opportunity,background_turn FROM cooperative_rotation WHERE network_id=$1", d.NetworkID).Scan(&opportunity, &background); err != nil {
		return err
	}
	budget := ack.ScanBudget
	if d.Role == "primary" {
		if budget >= 8 {
			budget -= 4
		} else if background%2 == 0 {
			budget = 0
		}
	}
	order, err := s.cooperativeCandidates(ctx, tx, d, "order", false)
	if err != nil {
		return err
	}
	refresh, err := s.cooperativeCandidates(ctx, tx, d, "refresh", false)
	if err != nil {
		return err
	}
	cold, err := s.cooperativeCandidates(ctx, tx, d, "background", d.Role == "primary" && background%2 == 1)
	if err != nil {
		return err
	}
	queues := map[string][]cooperativeCandidate{"order": order, "refresh": refresh, "background": cold}
	cycle := []string{"order", "refresh", "refresh", "background"}
	seen := map[protocol.Quantity]bool{}
	refreshes := 0
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
		return err
	}
	var key []byte
	var observationFloor int64
	if err = tx.QueryRow(ctx, "SELECT observation_watermark FROM bridge_sessions WHERE id=$1", session).Scan(&observationFloor); err != nil {
		return err
	}
	for attempts := 0; attempts < 256 && len(ack.Reads) < slots && budget > 0; attempts++ {
		category := cycle[opportunity%len(cycle)]
		opportunity = (opportunity + 1) % len(cycle)
		if category != "background" && fastOwned >= 8 {
			queues[category] = nil
		}
		queue := queues[category]
		if len(queue) == 0 || (category == "refresh" && refreshes >= 4) {
			if len(queues["order"])+len(queues["refresh"])+len(queues["background"]) == 0 {
				break
			}
			continue
		}
		c := queue[0]
		queues[category] = queue[1:]
		if seen[c.ID] {
			continue
		}
		cost := 1
		if c.Kind == "quantity_recipe" {
			cost = 4
		}
		if cost > budget {
			continue
		}
		c.TTLMillis = min(30000, int(c.ExpiresAt.Sub(now).Milliseconds()))
		if key == nil {
			key, err = cooperativeSigningKey(ctx, tx)
			if err != nil {
				return err
			}
		}
		c.AssignmentID, err = newAssignmentID(key, d, session, c.ID, c.Kind)
		if err != nil {
			return err
		}
		memory, err := cooperativeReadMemory(c.ReadAssignment)
		if err != nil {
			return err
		}
		fits := memory <= queueBytes && c.TTLMillis > 0
		if fits {
			ack.Reads = append(ack.Reads, c.ReadAssignment)
			data, _ := json.Marshal(ack)
			fits = len(data) <= responseBytes
			if !fits {
				ack.Reads = ack.Reads[:len(ack.Reads)-1]
			}
		}
		if !fits {
			if err = deferCooperativeCandidate(ctx, tx, d.NetworkID, c); err != nil {
				return err
			}
			continue
		}
		seen[c.ID] = true
		fieldNames := []string{"quantity"}
		if c.Kind == "quantity_recipe" {
			fieldNames = append(fieldNames, "recipe")
		}
		for _, field := range fieldNames {
			if _, err = tx.Exec(ctx, `INSERT INTO cooperative_read_fields(network_id,item_id,field,generation,
				assignment_id,device_id,session_id,expires_at,purpose,request_id,observation_floor)
				VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT(network_id,item_id,field) DO UPDATE SET generation=cooperative_read_fields.generation+1,
					assignment_id=excluded.assignment_id,device_id=excluded.device_id,session_id=excluded.session_id,
					expires_at=excluded.expires_at,purpose=excluded.purpose,request_id=excluded.request_id,
					dispatched_at=now(),observation_floor=excluded.observation_floor,completed_at=NULL`,
				d.NetworkID, int64(c.ID), field, c.AssignmentID, d.ID, session, c.ExpiresAt, c.Purpose, c.RequestID, observationFloor); err != nil {
				return err
			}
		}
		queueBytes -= memory
		budget -= cost
		if category != "background" {
			fastOwned++
		}
		if category == "refresh" {
			refreshes++
		}
	}
	// Helpers share task opportunities, but cannot advance the primary's
	// discovery/quantity/recipe rotation through their polling cadence.
	_, err = tx.Exec(ctx, `UPDATE cooperative_rotation SET opportunity=$2,
		background_turn=(background_turn+CASE WHEN $3 THEN 1 ELSE 0 END)%2 WHERE network_id=$1`,
		d.NetworkID, opportunity, d.Role == "primary")
	return err
}

func cooperativeReadMemory(read protocol.ReadAssignment) (int, error) {
	encoded, err := json.Marshal(read)
	if err != nil {
		return 0, err
	}
	// Lua re-encodes the complete assignment with ASCII-only JSON. Go's HTML
	// escaping can overestimate its cost, but UTF-8 must never underestimate it.
	encodedBytes := len(encoded)
	for _, r := range string(encoded) {
		if r >= utf8.RuneSelf {
			escapedBytes := 6
			if r > 0xffff {
				escapedBytes = 12
			}
			encodedBytes += escapedBytes - utf8.RuneLen(r)
		}
	}
	// adapter:key retains registry name, NUL, decimal metadata, NUL, and an
	// optional fixed-width canonical NBT SHA-256 hex digest.
	keyBytes := len(read.Item.Name) + len(strconv.Itoa(read.Item.Damage)) + 2
	if read.Item.NBT != "" {
		keyBytes += 64
	}
	return 2*encodedBytes + keyBytes + 256, nil
}

func deferCooperativeCandidate(ctx context.Context, tx pgx.Tx, network string, c cooperativeCandidate) error {
	var query string
	switch c.Purpose {
	case "refresh":
		query = "UPDATE inventory_refreshes SET next_attempt_at=now()+interval '2 seconds' WHERE network_id=$1 AND item_id=$2"
	case "order":
		query = "UPDATE inventory_rechecks SET next_attempt_at=now()+interval '2 seconds' WHERE network_id=$1 AND item_id=$2"
	default:
		column := "next_quantity_at"
		if c.Kind == "quantity_recipe" {
			column = "next_verify_at"
		}
		query = "UPDATE inventory_schedule SET " + column + "=now()+interval '2 seconds' WHERE network_id=$1 AND item_id=$2"
	}
	_, err := tx.Exec(ctx, query, network, int64(c.ID))
	return err
}
