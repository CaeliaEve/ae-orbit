package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type QQDelivery struct {
	ID         string          `json:"id"`
	AttemptID  string          `json:"attempt_id"`
	Kind       string          `json:"kind"`
	GroupID    string          `json:"group_id"`
	Recipient  string          `json:"recipient"`
	Channel    string          `json:"channel"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
	State      string          `json:"state"`
	ExternalID string          `json:"external_id"`
	Detail     string          `json:"detail"`
}
type QQBridgePoll struct {
	BotID   string `json:"bot_id"`
	Adapter string `json:"adapter"`
}
type QQBridgeAck struct {
	Enabled        bool         `json:"enabled"`
	SendingEnabled bool         `json:"sending_enabled"`
	PollSeconds    int          `json:"poll_seconds"`
	Groups         []QQGroup    `json:"groups"`
	Deliveries     []QQDelivery `json:"deliveries"`
}
type QQGroupSync struct {
	Version int64      `json:"version"`
	Name    string     `json:"name"`
	Members []QQMember `json:"members"`
	Error   string     `json:"error"`
}
type QQReceipt struct {
	AttemptID  string `json:"attempt_id"`
	State      string `json:"state"`
	ExternalID string `json:"external_id"`
	Detail     string `json:"detail"`
}

func (s *Store) QQBridgePoll(ctx context.Context, a QQAuth, in QQBridgePoll) (QQBridgeAck, error) {
	out := QQBridgeAck{PollSeconds: 3, Groups: []QQGroup{}, Deliveries: []QQDelivery{}}
	if !protocol.QQNumberValid(in.BotID) || in.Adapter != "snowluma" {
		return out, ErrInvalid
	}
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		// Locking the connector serializes configuration, claims and delivery starts.
		err := tx.QueryRow(ctx, `UPDATE qq_connectors SET last_seen=now(),bot_id=$2,adapter=$3 WHERE server_group=$1 RETURNING enabled,sending_enabled`, a.Group, in.BotID, in.Adapter).Scan(&out.Enabled, &out.SendingEnabled)
		if err != nil {
			return err
		}
		if !out.Enabled {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET state='expired',detail='采集状态播报已关闭，仅保留低电量通知' WHERE server_group=$1 AND (kind='network' OR kind='alarm' AND (coalesce(payload->>'alert_kind','')<>'low_energy' OR coalesce(payload->>'state','')<>'active')) AND state IN ('queued','leased')`, a.Group); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET state='uncertain',detail='发送进程中断，结果待确认' WHERE server_group=$1 AND state='sending' AND lease_until<now()`, a.Group); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET state='queued',attempt_id='',lease_until=NULL WHERE server_group=$1 AND state='leased' AND lease_until<now()`, a.Group); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET state='expired',detail='通知已过期' WHERE server_group=$1 AND state='queued' AND expires_at<=now()`, a.Group); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT group_id,name,enabled,alarm_recipient,members_at,refresh_version,refreshed_version,refresh_error FROM qq_groups WHERE server_group=$1 AND enabled ORDER BY group_id`, a.Group)
		if err != nil {
			return err
		}
		for rows.Next() {
			var g QQGroup
			if err = rows.Scan(&g.ID, &g.Name, &g.Enabled, &g.AlarmRecipient, &g.MembersAt, &g.RefreshVersion, &g.RefreshedVersion, &g.Error); err != nil {
				rows.Close()
				return err
			}
			out.Groups = append(out.Groups, g)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if !out.SendingEnabled {
			return nil
		}
		// A queued alarm that already recovered becomes obsolete, not a late warning.
		if _, err = tx.Exec(ctx, `UPDATE qq_outbox q SET state='expired',detail='告警已恢复，跳过过时告警' FROM monitor_alerts m WHERE q.server_group=$1 AND q.state='queued' AND q.kind='alarm' AND q.source_key='alert:'||m.id||':active' AND m.state='resolved'`, a.Group); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT q.id FROM qq_outbox q JOIN qq_groups g ON g.server_group=q.server_group AND g.group_id=q.group_id JOIN qq_connectors c ON c.server_group=q.server_group WHERE q.server_group=$1 AND c.catalog_ready AND (q.channel='group' OR c.allow_private) AND q.kind<>'network' AND (q.kind<>'alarm' OR q.payload->>'alert_kind'='low_energy' AND q.payload->>'state'='active') AND q.state='queued' AND q.expires_at>now() AND g.enabled ORDER BY q.created_at,q.id FOR UPDATE OF q SKIP LOCKED LIMIT 4`, a.Group)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			var d QQDelivery
			if err := s.localizeQQDelivery(ctx, tx, id); err != nil {
				return err
			}
			// Keep trigger-time telemetry and add a fresh cached observation at
			// dispatch, so a delayed notification never labels old EU as current.
			if _, err := tx.Exec(ctx, `UPDATE qq_outbox SET payload=payload||jsonb_build_object('current_energy',qq_lsc_snapshot(payload->>'object_id')) WHERE id=$1 AND kind='alarm' AND payload ? 'object_id'`, id); err != nil {
				return err
			}
			err = tx.QueryRow(ctx, `UPDATE qq_outbox SET state='leased',attempt_id=$2,lease_until=now()+interval '90 seconds',attempts=attempts+1 WHERE id=$1 RETURNING id,attempt_id,kind,group_id,recipient,channel,payload,created_at,state,external_id,detail`, id, newID()).Scan(&d.ID, &d.AttemptID, &d.Kind, &d.GroupID, &d.Recipient, &d.Channel, &d.Payload, &d.CreatedAt, &d.State, &d.ExternalID, &d.Detail)
			if err != nil {
				return err
			}
			if d.Kind == "power" {
				var requested struct {
					Device string `json:"requested_device"`
				}
				if err := json.Unmarshal(d.Payload, &requested); err != nil {
					return err
				}
				current, err := qqPowerView(ctx, tx, a, d.GroupID, requested.Device)
				if err != nil {
					return err
				}
				payload, err := json.Marshal(current)
				if err != nil {
					return err
				}
				d.Payload = payload
				if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET payload=$2 WHERE id=$1`, d.ID, payload); err != nil {
					return err
				}
			}
			if d.Kind == "inventory_query" {
				var requested struct {
					Keyword  string   `json:"keyword"`
					Keywords []string `json:"keywords"`
				}
				if err := json.Unmarshal(d.Payload, &requested); err != nil {
					return err
				}
				keys := requested.Keywords
				if len(keys) == 0 {
					keys = []string{requested.Keyword}
				}
				current, err := qqInventoryKeysView(ctx, tx, a, d.GroupID, keys)
				if err != nil {
					return err
				}
				if len(current.Items) == 0 {
					if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET state='expired',detail='关键词已移除或监控已停用' WHERE id=$1`, d.ID); err != nil {
						return err
					}
					continue
				}
				payload, err := json.Marshal(current)
				if err != nil {
					return err
				}
				d.Payload = payload
				if _, err = tx.Exec(ctx, `UPDATE qq_outbox SET payload=$2 WHERE id=$1`, d.ID, payload); err != nil {
					return err
				}
			}
			out.Deliveries = append(out.Deliveries, d)
		}
		return nil
	})
	return out, err
}
func (s *Store) QQSyncGroup(ctx context.Context, a QQAuth, gid string, in QQGroupSync) error {
	if !protocol.QQNumberValid(gid) || in.Version < 1 || !monitorText(in.Name, 128) || !monitorText(in.Error, 256) || len(in.Members) > 5000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range in.Members {
		if !protocol.QQNumberValid(m.ID) || seen[m.ID] || !monitorText(m.Name, 128) || !monitorText(m.Card, 128) {
			return ErrInvalid
		}
		seen[m.ID] = true
	}
	raw, err := json.Marshal(in.Members)
	if err != nil {
		return err
	}
	if in.Members == nil {
		raw = []byte("[]")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE qq_groups SET members=CASE WHEN $6='' THEN $4::jsonb ELSE members END,members_at=CASE WHEN $6='' THEN now() ELSE members_at END,name=CASE WHEN $5='' THEN name ELSE $5 END,refreshed_version=$3,refresh_error=$6 WHERE server_group=$1 AND group_id=$2 AND enabled AND refresh_version=$3`, a.Group, gid, in.Version, raw, in.Name, in.Error)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrViewChanged
	}
	return err
}
func (s *Store) QQBeginDelivery(ctx context.Context, a QQAuth, id, attempt string) error {
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT enabled AND sending_enabled FROM qq_connectors WHERE server_group=$1 FOR UPDATE`, a.Group).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return ErrForbidden
		}
		tag, err := tx.Exec(ctx, `UPDATE qq_outbox q SET state='sending',lease_until=now()+interval '120 seconds' FROM qq_groups g,qq_connectors c WHERE q.id=$1 AND q.server_group=$2 AND q.attempt_id=$3 AND q.kind<>'network' AND (q.kind<>'alarm' OR q.payload->>'alert_kind'='low_energy' AND q.payload->>'state'='active') AND (q.kind<>'inventory_query' OR EXISTS(SELECT 1 FROM subnet_qq_keywords k JOIN subnet_items i ON i.subnet_id=k.subnet_id AND i.item_id=k.item_id JOIN subnet_plugins p ON p.server_group=k.server_group WHERE k.server_group=q.server_group AND (k.keyword=q.payload->>'keyword' OR coalesce(q.payload->'keywords','[]'::jsonb) ? k.keyword) AND i.enabled AND p.enabled)) AND q.state='leased' AND q.lease_until>now() AND q.expires_at>now() AND g.server_group=q.server_group AND g.group_id=q.group_id AND g.enabled AND c.server_group=q.server_group AND c.catalog_ready AND (q.channel='group' OR c.allow_private)`, id, a.Group, attempt)
		if err == nil && tag.RowsAffected() == 0 {
			err = ErrConflict
		}
		return err
	})
}
func (s *Store) QQDeliveryReceipt(ctx context.Context, a QQAuth, id string, in QQReceipt) error {
	if !monitorID.MatchString(in.AttemptID) || len(in.ExternalID) > 128 || !monitorText(in.Detail, 256) || (in.State != "confirmed" && in.State != "uncertain" && in.State != "failed") {
		return ErrInvalid
	}
	if in.State == "confirmed" && (in.ExternalID == "" || !monitorText(in.ExternalID, 128)) {
		return ErrInvalid
	}
	tag, err := s.pool.Exec(ctx, `UPDATE qq_outbox SET state=$4,external_id=$5,detail=$6,delivered_at=CASE WHEN $4='confirmed' THEN coalesce(delivered_at,now()) ELSE delivered_at END WHERE id=$1 AND server_group=$2 AND attempt_id=$3 AND (state='sending' OR state=$4 OR state='uncertain' AND $4='confirmed' OR state='leased' AND $4='uncertain')`, id, a.Group, in.AttemptID, in.State, in.ExternalID, in.Detail)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrConflict
	}
	return err
}
func (s *Store) QQDeliveries(ctx context.Context, member, group string) ([]QQDelivery, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT q.id,q.attempt_id,q.kind,q.group_id,q.recipient,q.channel,q.payload,q.created_at,q.state,q.external_id,q.detail FROM qq_outbox q JOIN members m ON m.id=$2 WHERE q.server_group=$1 AND (m.is_admin OR q.member_id=$2 OR q.kind IN ('alarm','network','exception') AND (q.network_id IS NULL OR EXISTS(SELECT 1 FROM network_grants g WHERE g.network_id=q.network_id AND g.member_id=$2))) ORDER BY q.created_at DESC,q.id DESC LIMIT 100`, group, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QQDelivery{}
	for rows.Next() {
		var d QQDelivery
		if err = rows.Scan(&d.ID, &d.AttemptID, &d.Kind, &d.GroupID, &d.Recipient, &d.Channel, &d.Payload, &d.CreatedAt, &d.State, &d.ExternalID, &d.Detail); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) MaintainQQ(ctx context.Context) error {
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		// Network heartbeat changes remain visible in the web UI, without QQ broadcasts.
		_, err := tx.Exec(ctx, `DELETE FROM qq_outbox WHERE id IN (SELECT id FROM qq_outbox WHERE state IN ('confirmed','failed','expired') AND created_at<now()-interval '30 days' LIMIT 1000)`)
		return err
	})
}
