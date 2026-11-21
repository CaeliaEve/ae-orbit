package store

import (
	"context"
	"encoding/json"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type QQPowerRequest struct {
	GroupID    string `json:"group_id"`
	RequestKey string `json:"request_key"`
	Device     string `json:"device"`
}
type QQPowerView struct {
	Title          string            `json:"title"`
	Devices        []json.RawMessage `json:"devices"`
	SendingEnabled bool              `json:"sending_enabled"`
	Device         string            `json:"requested_device,omitempty"`
}

func qqPowerView(ctx context.Context, tx pgx.Tx, a QQAuth, gid, device string) (QQPowerView, error) {
	v := QQPowerView{Title: "蓝波顿当前电量", Devices: []json.RawMessage{}, Device: device}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT c.sending_enabled,c.enabled AND c.catalog_ready AND g.enabled FROM qq_connectors c JOIN qq_groups g ON g.server_group=c.server_group WHERE c.server_group=$1 AND g.group_id=$2`, a.Group, gid).Scan(&v.SendingEnabled, &allowed)
	if err != nil {
		return v, notFound(err)
	}
	if !allowed {
		return v, ErrForbidden
	}
	rows, err := tx.Query(ctx, `SELECT qq_lsc_snapshot(o.id) FROM plugin_objects o JOIN plugin_installations p USING(server_group,plugin_id) WHERE o.server_group=$1 AND o.plugin_id='gtnh.lsc' AND o.enabled AND p.enabled AND ($2='' OR o.name=$2 OR o.id=$2) ORDER BY o.created_at,o.id LIMIT 16`, a.Group, device)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			return v, err
		}
		v.Devices = append(v.Devices, raw)
	}
	return v, rows.Err()
}
func (s *Store) QQPower(ctx context.Context, a QQAuth, gid, device string) (QQPowerView, error) {
	var v QQPowerView
	if !protocol.QQNumberValid(gid) || !monitorText(device, 128) {
		return v, ErrInvalid
	}
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error { var err error; v, err = qqPowerView(ctx, tx, a, gid, device); return err })
	return v, err
}
func (s *Store) QQRequestPower(ctx context.Context, a QQAuth, in QQPowerRequest) error {
	if !protocol.QQNumberValid(in.GroupID) || !protocol.ProductionJobKeyValid(in.RequestKey) || !monitorText(in.Device, 128) {
		return ErrInvalid
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		// The group row bounds manual requests and serializes duplicate message IDs.
		if _, err := tx.Exec(ctx, `SELECT group_id FROM qq_groups WHERE server_group=$1 AND group_id=$2 FOR UPDATE`, a.Group, in.GroupID); err != nil {
			return err
		}
		var existing bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qq_outbox WHERE server_group=$1 AND source_key=$2 AND group_id=$3)`, a.Group, "power:"+in.RequestKey, in.GroupID).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return nil
		}
		v, err := qqPowerView(ctx, tx, a, in.GroupID, in.Device)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE qq_groups SET last_power_request_at=now() WHERE server_group=$1 AND group_id=$2 AND (last_power_request_at IS NULL OR last_power_request_at<now()-interval '10 seconds')`, a.Group, in.GroupID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrCapacity
		}
		payload, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT qq_enqueue($1,NULL,NULL,$2,'power',$3,'',false,$4)`, a.Group, "power:"+in.RequestKey, in.GroupID, payload)
		return err
	})
}
