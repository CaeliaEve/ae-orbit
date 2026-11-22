package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type QQChannel struct {
	ID             string `json:"group_id"`
	Name           string `json:"name"`
	AlarmRecipient string `json:"alarm_recipient"`
}
type QQChannelSync struct {
	Groups       []QQChannel `json:"groups"`
	AllowPrivate bool        `json:"allow_private"`
}

func (s *Store) QQSyncChannels(ctx context.Context, a QQAuth, in QQChannelSync) error {
	if len(in.Groups) > 16 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, g := range in.Groups {
		if !protocol.QQNumberValid(g.ID) || seen[g.ID] || !monitorText(g.Name, 128) || g.AlarmRecipient != "" && !protocol.QQNumberValid(g.AlarmRecipient) {
			return ErrInvalid
		}
		seen[g.ID] = true
	}
	in.Groups = append([]QQChannel{}, in.Groups...)
	sort.Slice(in.Groups, func(i, j int) bool { return in.Groups[i].ID < in.Groups[j].ID })
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var previous string
		if err := tx.QueryRow(ctx, `SELECT catalog_digest FROM qq_connectors WHERE server_group=$1 FOR UPDATE`, a.Group).Scan(&previous); err != nil {
			return notFound(err)
		}
		if previous == digest {
			_, err := tx.Exec(ctx, `UPDATE qq_connectors SET catalog_at=now() WHERE server_group=$1`, a.Group)
			return err
		}
		ids := []string{}
		for _, g := range in.Groups {
			ids = append(ids, g.ID)
			if _, err := tx.Exec(ctx, `INSERT INTO qq_groups(server_group,group_id,name,enabled,alarm_recipient) VALUES($1,$2,$3,true,$4)
    ON CONFLICT(server_group,group_id) DO UPDATE SET name=excluded.name,enabled=true,alarm_recipient=excluded.alarm_recipient,
    refresh_version=CASE WHEN NOT qq_groups.enabled THEN qq_groups.refresh_version+1 ELSE qq_groups.refresh_version END`, a.Group, g.ID, g.Name, g.AlarmRecipient); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE qq_groups SET enabled=false WHERE server_group=$1 AND NOT(group_id=ANY($2::text[]))`, a.Group, ids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE qq_outbox SET state='expired',detail='MaiBot 已停用此通知渠道' WHERE server_group=$1 AND state IN ('queued','leased') AND (NOT(group_id=ANY($2::text[])) OR channel='private' AND NOT $3)`, a.Group, ids, in.AllowPrivate); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE qq_reminders r SET state='canceled',finished_at=now() FROM networks n WHERE r.network_id=n.id AND n.server_group=$1 AND r.state='watching' AND NOT(r.group_id=ANY($2::text[]))`, a.Group, ids); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE qq_connectors SET catalog_ready=true,catalog_at=now(),catalog_digest=$2,allow_private=$3,revision=revision+1 WHERE server_group=$1`, a.Group, digest, in.AllowPrivate)
		return err
	})
}
