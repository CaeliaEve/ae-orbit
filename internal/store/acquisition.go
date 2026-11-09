package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// The server-group row is locked by the caller. A round allocates all shares
// before the current intent is registered, so arrival order cannot win it.
func (s *Store) acquisitionGrant(ctx context.Context, tx pgx.Tx, d Device, session, group string, slots, queueBytes, version int) (budget int, grantID string, ttl int, err error) {
	var cooperative bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM networks WHERE server_group=$1 AND bridge_mode='cooperative')", group).Scan(&cooperative); err != nil {
		return
	}
	if !cooperative {
		if slots > 0 && queueBytes > 0 {
			tag, e := tx.Exec(ctx, `UPDATE server_groups SET next_scan_at=now()+$2::double precision*interval '1 millisecond'
				WHERE name=$1 AND next_scan_at<=now()`, group, s.options.Limits.PollMillis)
			err = e
			if err == nil && tag.RowsAffected() > 0 {
				budget = s.options.ScanItems
			}
		}
		return
	}
	var now time.Time
	var due bool
	if err = tx.QueryRow(ctx, "SELECT next_scan_at<=now(),now() FROM server_groups WHERE name=$1", group).Scan(&due, &now); err != nil {
		return
	}
	if due {
		if err = s.allocateAcquisitionRound(ctx, tx, group, now); err != nil {
			return
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO acquisition_intents(device_id,session_id,slots,bytes)
		VALUES($1,$2,$3,$4) ON CONFLICT(device_id) DO UPDATE SET
		session_id=excluded.session_id,slots=excluded.slots,bytes=excluded.bytes,last_intent_at=now(),
		grant_id=CASE WHEN acquisition_intents.session_id=excluded.session_id THEN acquisition_intents.grant_id ELSE '' END,
		grant_budget=CASE WHEN acquisition_intents.session_id=excluded.session_id THEN acquisition_intents.grant_budget ELSE 0 END,
		claimed=CASE WHEN acquisition_intents.session_id=excluded.session_id THEN acquisition_intents.claimed ELSE false END`,
		d.ID, session, slots, queueBytes)
	if err != nil || slots <= 0 || queueBytes <= 0 {
		return
	}
	err = tx.QueryRow(ctx, `UPDATE acquisition_intents SET claimed=true WHERE device_id=$1 AND session_id=$2
		AND NOT claimed AND grant_budget>0 AND grant_until>now()
		RETURNING grant_budget,grant_id,greatest(0,floor(extract(epoch FROM grant_until-now())*1000))::int`, d.ID, session).Scan(&budget, &grantID, &ttl)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if version == 1 {
		grantID, ttl = "", 0
	}
	return
}

func (s *Store) allocateAcquisitionRound(ctx context.Context, tx pgx.Tx, group string, now time.Time) error {
	if _, err := tx.Exec(ctx, "INSERT INTO acquisition_rounds(server_group) VALUES($1) ON CONFLICT DO NOTHING", group); err != nil {
		return err
	}
	var networkCursor string
	if err := tx.QueryRow(ctx, "SELECT network_cursor FROM acquisition_rounds WHERE server_group=$1", group).Scan(&networkCursor); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT n.id,d.id,coalesce(r.device_cursor,'') FROM acquisition_intents a
		JOIN devices d ON d.id=a.device_id AND d.current_session=a.session_id AND NOT d.revoked
		JOIN networks n ON n.id=d.network_id
		JOIN bridge_sessions bs ON bs.id=a.session_id AND bs.device_id=d.id AND NOT bs.retired
		LEFT JOIN cooperative_rotation r ON r.network_id=n.id
		WHERE n.server_group=$1 AND a.slots>0 AND a.bytes>0
		AND a.last_intent_at>now()-$2::double precision*interval '1 millisecond'
		AND bs.protocol=CASE n.bridge_mode WHEN 'single' THEN 1 ELSE 2 END
		AND (n.bridge_mode='cooperative' OR d.role='primary')
		AND bs.capabilities->>'inventory'='true'
		AND (n.bridge_mode='single' OR d.role='primary' OR bs.capabilities->>'exact_quantities'='true')
		ORDER BY n.id,d.id`, group, 2*s.options.Limits.PollMillis)
	if err != nil {
		return err
	}
	devices := map[string][]string{}
	cursors := map[string]string{}
	networks := []string{}
	for rows.Next() {
		var network, device, cursor string
		if err = rows.Scan(&network, &device, &cursor); err != nil {
			rows.Close()
			return err
		}
		if devices[network] == nil {
			networks = append(networks, network)
			cursors[network] = cursor
		}
		devices[network] = append(devices[network], device)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	after := func(values []string, cursor string) int {
		index := sort.Search(len(values), func(i int) bool { return values[i] > cursor })
		return index % len(values)
	}
	allocations := map[string]int{}
	if len(networks) > 0 {
		networkIndex := after(networks, networkCursor)
		for range s.options.ScanItems / 4 {
			network := networks[networkIndex]
			candidates := devices[network]
			device := candidates[after(candidates, cursors[network])]
			allocations[device] += 4
			cursors[network], networkCursor = device, network
			networkIndex = (networkIndex + 1) % len(networks)
		}
	}
	deadline := now.Add(time.Duration(s.options.Limits.PollMillis) * time.Millisecond)
	if _, err = tx.Exec(ctx, `UPDATE acquisition_intents a SET grant_id='',grant_budget=0,claimed=false,grant_until=$2
		FROM devices d JOIN networks n ON n.id=d.network_id WHERE a.device_id=d.id AND n.server_group=$1`, group, deadline); err != nil {
		return err
	}
	for device, budget := range allocations {
		if _, err = tx.Exec(ctx, `UPDATE acquisition_intents SET grant_id=$2,grant_budget=$3 WHERE device_id=$1`,
			device, newID(), budget); err != nil {
			return err
		}
	}
	for network, cursor := range cursors {
		if _, err = tx.Exec(ctx, `INSERT INTO cooperative_rotation(network_id,device_cursor) VALUES($1,$2)
			ON CONFLICT(network_id) DO UPDATE SET device_cursor=excluded.device_cursor`, network, cursor); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE acquisition_rounds SET network_cursor=$2 WHERE server_group=$1", group, networkCursor); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE server_groups SET next_scan_at=$2 WHERE name=$1", group, deadline)
	return err
}
