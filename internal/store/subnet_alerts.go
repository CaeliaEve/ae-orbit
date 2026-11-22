package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func acceptSubnetSample(ctx context.Context, tx pgx.Tx, subnet, session string, r SubnetResult, stale, history int, now time.Time) error {
	var priorSession, priorError string
	var priorAt, recorded *time.Time
	var epoch int64
	var sample int64
	err := tx.QueryRow(ctx, `SELECT session_id,observed_at,last_recorded_at,epoch,sample_millis,error FROM subnet_latest WHERE subnet_id=$1 AND item_id=$2 FOR UPDATE`, subnet, r.ItemID).Scan(&priorSession, &priorAt, &recorded, &epoch, &sample, &priorError)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if err == nil && priorSession == session && r.SampleMillis <= sample {
		return nil
	}
	if epoch == 0 {
		epoch = 1
	}
	if priorSession != "" && r.Error == "" && (priorError != "" || priorSession != session || priorAt != nil && now.Sub(*priorAt) > time.Duration(stale)*time.Second) {
		epoch++
		recorded = nil
		if _, err = tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE subnet_id=$1 AND item_id=$2`, subnet, r.ItemID); err != nil {
			return err
		}
	}
	if r.Error != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO subnet_latest(subnet_id,item_id,session_id,error,reported_at,sample_millis,epoch) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(subnet_id,item_id) DO UPDATE SET session_id=excluded.session_id,error=excluded.error,reported_at=excluded.reported_at,sample_millis=excluded.sample_millis`, subnet, r.ItemID, session, r.Error, now, r.SampleMillis, epoch); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE subnet_id=$1 AND item_id=$2`, subnet, r.ItemID)
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO subnet_latest(subnet_id,item_id,session_id,quantity,observed_at,error,reported_at,sample_millis,epoch) VALUES($1,$2,$3,$4,$5,'',$5,$6,$7) ON CONFLICT(subnet_id,item_id) DO UPDATE SET session_id=excluded.session_id,quantity=excluded.quantity,observed_at=excluded.observed_at,error='',reported_at=excluded.reported_at,sample_millis=excluded.sample_millis,epoch=excluded.epoch`, subnet, r.ItemID, session, int64(*r.Quantity), now, r.SampleMillis, epoch); err != nil {
		return err
	}
	if recorded == nil || now.Sub(*recorded) >= time.Duration(history)*time.Second {
		if _, err = tx.Exec(ctx, `INSERT INTO subnet_history(subnet_id,item_id,session_id,quantity,epoch,observed_at) VALUES($1,$2,$3,$4,$5,$6)`, subnet, r.ItemID, session, int64(*r.Quantity), epoch, now); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_latest SET last_recorded_at=$3 WHERE subnet_id=$1 AND item_id=$2`, subnet, r.ItemID, now); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE subnet_latest SET revision=$3 WHERE subnet_id=$1 AND item_id=$2`, subnet, r.ItemID, r.Revision); err != nil {
		return err
	}
	return evaluateSubnetRules(ctx, tx, subnet, r.ItemID, *r.Quantity, now)
}
func evaluateSubnetRules(ctx context.Context, tx pgx.Tx, subnet, item string, quantity protocol.Quantity, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT r.id,r.kind,r.threshold,r.recover,r.duration_seconds,r.since,r.notification,r.member_id FROM subnet_rules r JOIN members m ON m.id=r.member_id JOIN monitored_subnets n ON n.id=r.subnet_id WHERE r.subnet_id=$1 AND r.item_id=$2 AND r.enabled AND NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM networks net JOIN network_grants g ON g.network_id=net.id WHERE net.server_group=n.server_group AND g.member_id=m.id)) ORDER BY r.id FOR UPDATE OF r`, subnet, item)
	if err != nil {
		return err
	}
	type rule struct {
		id, kind, member   string
		threshold, recover int64
		duration           int
		since              *time.Time
		notification       []byte
	}
	rs := []rule{}
	for rows.Next() {
		var r rule
		if err = rows.Scan(&r.id, &r.kind, &r.threshold, &r.recover, &r.duration, &r.since, &r.notification, &r.member); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, r := range rs {
		triggered := r.kind == "low" && int64(quantity) < r.threshold || r.kind == "high" && int64(quantity) > r.threshold || r.kind == "zero" && quantity == 0
		recovered := r.kind == "high" && int64(quantity) <= r.recover || r.kind != "high" && int64(quantity) >= r.recover
		if recovered {
			if _, err = tx.Exec(ctx, `UPDATE subnet_alerts SET state='resolved',resolved_at=$2,resolve_reason='quantity_recovered' WHERE rule_id=$1 AND state='active'`, r.id, now); err != nil {
				return err
			}
		}
		if !triggered {
			if _, err = tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE id=$1`, r.id); err != nil {
				return err
			}
			continue
		}
		var since time.Time
		if err = tx.QueryRow(ctx, `UPDATE subnet_rules SET since=coalesce(since,$2) WHERE id=$1 RETURNING since`, r.id, now).Scan(&since); err != nil {
			return err
		}
		if now.Sub(since) < time.Duration(r.duration)*time.Second {
			continue
		}
		id := newID()
		tag, err := tx.Exec(ctx, `INSERT INTO subnet_alerts(id,rule_id,quantity,state,triggered_at) VALUES($1,$2,$3,'active',$4) ON CONFLICT(rule_id) WHERE state='active' DO NOTHING`, id, r.id, int64(quantity), now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		var target *protocol.QQNotification
		if len(r.notification) > 0 {
			if err = json.Unmarshal(r.notification, &target); err != nil {
				return err
			}
		}
		if target == nil {
			continue
		}
		// Notifications are queued together after all accepted samples in this poll.
	}
	return nil
}

// One durable message per target and at most ten rules, with immutable values
// from the triggering observation. Receipt/replay semantics stay unchanged.
func queueSubnetNotifications(ctx context.Context, tx pgx.Tx, subnet string) error {
	_, err := tx.Exec(ctx, `WITH pending AS (
 SELECT a.id,r.member_id,r.notification,n.server_group,n.name subnet,d.label,d.name registry,d.kind,a.quantity,r.kind rule,r.threshold,
 (row_number() OVER(PARTITION BY r.member_id,r.notification ORDER BY a.id)-1)/10 batch
 FROM subnet_alerts a JOIN subnet_rules r ON r.id=a.rule_id JOIN monitored_subnets n ON n.id=r.subnet_id JOIN subnet_catalog d ON d.id=r.item_id
 WHERE n.id=$1 AND a.state='active' AND NOT a.notification_queued AND r.notification IS NOT NULL AND r.notification<>'null'::jsonb
 ), grouped AS (
 SELECT server_group,subnet,member_id,notification,batch,md5(string_agg(id,',' ORDER BY id)) source,
 jsonb_agg(jsonb_build_object('item',label,'registry',registry,'quantity',quantity::text,'unit',CASE WHEN kind='fluid' THEN 'mB' ELSE '件' END,'rule',rule,'threshold',threshold::text) ORDER BY id) items
 FROM pending GROUP BY server_group,subnet,member_id,notification,batch
 ) SELECT qq_enqueue(server_group,NULL,member_id,'subnet-batch:'||source,'subnet',notification->>'group_id',coalesce(notification->>'recipient',''),coalesce((notification->>'private_copy')::boolean,false),jsonb_build_object('title','子网库存预警','subnet',subnet,'items',items)) FROM grouped`, subnet)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE subnet_alerts SET notification_queued=true WHERE rule_id IN(SELECT id FROM subnet_rules WHERE subnet_id=$1) AND state='active' AND NOT notification_queued`, subnet)
	return err
}

func (s *Store) MaintainSubnets(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `UPDATE subnet_rules r SET since=NULL FROM monitored_subnets n JOIN subnet_plugins p ON p.server_group=n.server_group JOIN monitor_collectors c ON c.id=n.collector_id WHERE r.subnet_id=n.id AND r.since IS NOT NULL AND (NOT n.enabled OR NOT p.enabled OR c.revoked OR c.last_seen IS NULL OR c.last_seen<now()-n.stale_seconds*interval '1 second' OR NOT EXISTS(SELECT 1 FROM subnet_latest l WHERE l.subnet_id=r.subnet_id AND l.item_id=r.item_id AND l.error='' AND l.session_id=c.current_session AND l.revision=n.revision AND l.observed_at>now()-n.stale_seconds*interval '1 second'))`); err != nil {
		return err
	}
	// A bounded delete keeps dedicated monitoring from filling the database.
	_, err := s.pool.Exec(ctx, `DELETE FROM subnet_history WHERE id IN(SELECT id FROM subnet_history WHERE observed_at<now()-interval '30 days' ORDER BY id LIMIT 5000)`)
	return err
}
