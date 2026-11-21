package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type QQSettings struct {
	Enabled        bool       `json:"enabled"`
	SendingEnabled bool       `json:"sending_enabled"`
	Revision       int64      `json:"revision"`
	Connected      bool       `json:"connected"`
	LastSeen       *time.Time `json:"last_seen,omitempty"`
	BotID          string     `json:"bot_id"`
	Adapter        string     `json:"adapter"`
	Alarms         bool       `json:"alarms"`
	Exceptions     bool       `json:"exceptions"`
	Recoveries     bool       `json:"recoveries"`
	Groups         []QQGroup  `json:"groups"`
	CatalogReady   bool       `json:"catalog_ready"`
	CatalogAt      *time.Time `json:"catalog_at,omitempty"`
	AllowPrivate   bool       `json:"allow_private"`
}
type QQGroup struct {
	ID               string     `json:"group_id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	AlarmRecipient   string     `json:"alarm_recipient"`
	MembersAt        *time.Time `json:"members_at,omitempty"`
	RefreshVersion   int64      `json:"refresh_version"`
	RefreshedVersion int64      `json:"refreshed_version"`
	Error            string     `json:"error"`
}
type QQMember struct {
	ID   string `json:"user_id"`
	Name string `json:"nickname"`
	Card string `json:"card"`
}
type QQMembers struct {
	Members []QQMember `json:"members"`
	Group   QQGroup    `json:"group"`
}
type QQAuth struct{ Group string }

func (s *Store) QQSettings(ctx context.Context, member, group string) (QQSettings, error) {
	v := QQSettings{Alarms: true, Exceptions: true, Recoveries: true, Groups: []QQGroup{}}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return v, err
	}
	err := s.pool.QueryRow(ctx, `SELECT enabled,sending_enabled,revision,last_seen,bot_id,adapter,alarms,exceptions,recoveries,catalog_ready,catalog_at,allow_private FROM qq_connectors WHERE server_group=$1`, group).Scan(&v.Enabled, &v.SendingEnabled, &v.Revision, &v.LastSeen, &v.BotID, &v.Adapter, &v.Alarms, &v.Exceptions, &v.Recoveries, &v.CatalogReady, &v.CatalogAt, &v.AllowPrivate)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	v.Connected = v.LastSeen != nil && time.Since(*v.LastSeen) < 30*time.Second
	rows, err := s.pool.Query(ctx, `SELECT group_id,name,enabled,alarm_recipient,members_at,refresh_version,refreshed_version,refresh_error FROM qq_groups WHERE server_group=$1 ORDER BY group_id`, group)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var g QQGroup
		if err = rows.Scan(&g.ID, &g.Name, &g.Enabled, &g.AlarmRecipient, &g.MembersAt, &g.RefreshVersion, &g.RefreshedVersion, &g.Error); err != nil {
			return v, err
		}
		v.Groups = append(v.Groups, g)
	}
	return v, rows.Err()
}
func (s *Store) SetQQSettings(ctx context.Context, member, group string, v QQSettings) error {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	if v.Revision < 0 || len(v.Groups) > 0 {
		return ErrInvalid
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		inserted, err := tx.Exec(ctx, `INSERT INTO qq_connectors(server_group) VALUES($1) ON CONFLICT DO NOTHING`, group)
		if err != nil {
			return err
		}
		var revision int64
		if err = tx.QueryRow(ctx, `SELECT revision FROM qq_connectors WHERE server_group=$1 FOR UPDATE`, group).Scan(&revision); err != nil {
			return err
		}
		if v.Revision != revision && !(v.Revision == 0 && inserted.RowsAffected() == 1) {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE qq_connectors SET enabled=$2,sending_enabled=$3,revision=revision+1,alarms=$4,exceptions=$5,recoveries=$6 WHERE server_group=$1`, group, v.Enabled, v.SendingEnabled, v.Alarms, v.Exceptions, v.Recoveries); err != nil {
			return err
		}
		return nil
	})
}
func (s *Store) QQToken(ctx context.Context, member, group string) (string, error) {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return "", err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return "", err
	}
	token := newToken("ac_qq_")
	h := hashToken(token)
	tag, err := s.pool.Exec(ctx, `UPDATE qq_connectors SET token_hash=$2,last_seen=NULL,revision=revision+1 WHERE server_group=$1`, group, h[:])
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return token, err
}
func (s *Store) AuthQQ(ctx context.Context, token string) (QQAuth, error) {
	var a QQAuth
	if len(token) < 32 || len(token) > 256 {
		return a, ErrUnauthorized
	}
	h := hashToken(token)
	err := s.pool.QueryRow(ctx, `SELECT server_group FROM qq_connectors WHERE token_hash=$1`, h[:]).Scan(&a.Group)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthorized
	}
	return a, err
}
func (s *Store) QQMembers(ctx context.Context, member, group, gid string, refresh bool) (QQMembers, error) {
	out := QQMembers{Members: []QQMember{}}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return out, err
	}
	if refresh {
		_, err := s.pool.Exec(ctx, `UPDATE qq_groups SET refresh_version=refresh_version+1 WHERE server_group=$1 AND group_id=$2 AND enabled AND (members_at IS NULL OR members_at<now()-interval '30 seconds') AND (refresh_version=refreshed_version OR refresh_error<>'')`, group, gid)
		if err != nil {
			return out, err
		}
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT group_id,name,enabled,alarm_recipient,members_at,refresh_version,refreshed_version,refresh_error,members FROM qq_groups WHERE server_group=$1 AND group_id=$2 AND enabled`, group, gid).Scan(&out.Group.ID, &out.Group.Name, &out.Group.Enabled, &out.Group.AlarmRecipient, &out.Group.MembersAt, &out.Group.RefreshVersion, &out.Group.RefreshedVersion, &out.Group.Error, &raw)
	if err != nil {
		return out, notFound(err)
	}
	err = json.Unmarshal(raw, &out.Members)
	return out, err
}
func validateQQTarget(ctx context.Context, tx pgx.Tx, network string, n *protocol.QQNotification) error {
	if n == nil {
		return nil
	}
	if n.Validate() != nil {
		return ErrInvalid
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM networks n JOIN qq_connectors c ON c.server_group=n.server_group JOIN qq_groups g ON g.server_group=c.server_group WHERE n.id=$1 AND c.enabled AND c.catalog_ready AND g.enabled AND g.group_id=$2 AND (NOT $3 OR c.allow_private))`, network, n.GroupID, n.PrivateCopy).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

type QQReminder struct {
	ID           string                  `json:"id"`
	NetworkID    string                  `json:"network_id"`
	MemberID     string                  `json:"member_id"`
	CPUName      string                  `json:"cpu_name"`
	EntryID      *string                 `json:"entry_id,omitempty"`
	State        string                  `json:"state"`
	Notification protocol.QQNotification `json:"notification"`
	Payload      json.RawMessage         `json:"payload"`
	CreatedAt    time.Time               `json:"created_at"`
}
type QQReminderInput struct {
	RequestKey   string                  `json:"request_key"`
	SessionID    string                  `json:"session_id"`
	CPUID        string                  `json:"cpu_id"`
	Notification protocol.QQNotification `json:"notification"`
}

func (s *Store) QQReminders(ctx context.Context, member, network string) ([]QQReminder, error) {
	if err := s.RequireAccess(ctx, member, network, false); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,network_id,member_id,cpu_name,entry_id,state,group_id,recipient,private_copy,payload,created_at FROM qq_reminders WHERE network_id=$1 AND (member_id=$2 OR EXISTS(SELECT 1 FROM members WHERE id=$2 AND is_admin)) ORDER BY created_at DESC LIMIT 100`, network, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QQReminder{}
	for rows.Next() {
		var v QQReminder
		if err = rows.Scan(&v.ID, &v.NetworkID, &v.MemberID, &v.CPUName, &v.EntryID, &v.State, &v.Notification.GroupID, &v.Notification.Recipient, &v.Notification.PrivateCopy, &v.Payload, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func addQQEntryReminder(ctx context.Context, tx pgx.Tx, member, network, entry string, n *protocol.QQNotification) error {
	if n == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO qq_reminders(id,network_id,member_id,request_key,entry_id,cpu_name,group_id,recipient,private_copy,payload)
  SELECT $1,$2,$3,'entry:'||e.id||':'||$1,e.id,e.cpu_name,$5,$6,$7,jsonb_build_object('cpu',e.cpu_name,'item',d.label,'registry',d.name,'quantity',e.quantity::text,'unit',CASE WHEN d.kind='fluid' THEN 'mB' ELSE '件' END,'owner',coalesce(nullif(m.game_id,''),m.name))
  FROM production_job_entries e JOIN item_definitions d ON d.network_id=e.network_id AND d.id=e.item_id JOIN members m ON m.id=$3 WHERE e.id=$4
  ON CONFLICT(entry_id,member_id) WHERE entry_id IS NOT NULL AND state='watching' DO UPDATE SET group_id=excluded.group_id,recipient=excluded.recipient,private_copy=excluded.private_copy`, newID(), network, member, entry, n.GroupID, n.Recipient, n.PrivateCopy)
	return err
}
func (s *Store) AddQQCPUReminder(ctx context.Context, member, network string, in QQReminderInput) (string, error) {
	if err := s.RequireAccess(ctx, member, network, true); err != nil {
		return "", err
	}
	if !protocol.ProductionJobKeyValid(in.RequestKey) || len(in.SessionID) > 128 || len(in.CPUID) > 128 {
		return "", ErrInvalid
	}
	id := newID()
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM networks WHERE id=$1 FOR UPDATE`, network); err != nil {
			return err
		}
		if err := validateQQTarget(ctx, tx, network, &in.Notification); err != nil {
			return err
		}
		var raw []byte
		var truncated bool
		var session string
		err := tx.QueryRow(ctx, `SELECT c.cpus,c.truncated,c.session_id FROM cpu_snapshots c JOIN networks n ON n.id=c.network_id WHERE c.network_id=$1 AND c.session_id=n.current_session AND c.observed_at>now()-interval '30 seconds' AND n.last_seen>now()-interval '30 seconds'`, network).Scan(&raw, &truncated, &session)
		if err != nil {
			return notFound(err)
		}
		if truncated || session != in.SessionID {
			return ErrViewChanged
		}
		var cpus []protocol.CPU
		if err = json.Unmarshal(raw, &cpus); err != nil {
			return err
		}
		var target *protocol.CPU
		for i := range cpus {
			if cpus[i].ID == in.CPUID {
				target = &cpus[i]
			}
		}
		if target == nil || !target.Busy {
			return ErrViewChanged
		}
		if strings.TrimSpace(target.Name) == "" {
			return ErrConflict
		}
		count := 0
		for _, c := range cpus {
			if c.Name == target.Name {
				count++
			}
		}
		if count != 1 {
			return ErrConflict
		}
		var entry *string
		rows, err := tx.Query(ctx, `SELECT e.id FROM production_job_entries e JOIN production_jobs j ON j.id=e.job_id WHERE e.network_id=$1 AND j.member_id=$2 AND e.cpu_name=$3 AND e.state='running' LIMIT 2`, network, member, target.Name)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var e string
			if err = rows.Scan(&e); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, e)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(ids) == 1 && target.Output != nil && target.Output.Damage != nil && target.Output.HasTag != nil && !*target.Output.HasTag {
			var matches bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM production_job_entries e JOIN orders o ON o.id=e.order_id JOIN item_definitions d ON d.network_id=e.network_id AND d.id=e.item_id WHERE e.id=$1 AND o.attempt_session=$2 AND d.name=$3 AND d.kind=$4 AND d.damage=$5 AND d.nbt='')`, ids[0], session, target.Output.Name, target.Output.Kind, *target.Output.Damage).Scan(&matches); err != nil {
				return err
			}
			if matches {
				entry = &ids[0]
			}
		}
		if entry != nil {
			if err = addQQEntryReminder(ctx, tx, member, network, *entry, &in.Notification); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT id FROM qq_reminders WHERE entry_id=$1 AND member_id=$2 AND state='watching'`, *entry, member).Scan(&id)
		}
		var existingTarget []byte
		var existingState string
		err = tx.QueryRow(ctx, `SELECT id,state,jsonb_build_object('group_id',group_id,'recipient',recipient,'private_copy',private_copy) FROM qq_reminders WHERE network_id=$1 AND member_id=$2 AND request_key=$3`, network, member, in.RequestKey).Scan(&id, &existingState, &existingTarget)
		if err == nil {
			var n protocol.QQNotification
			if err = json.Unmarshal(existingTarget, &n); err != nil {
				return err
			}
			if n != in.Notification {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		err = tx.QueryRow(ctx, `UPDATE qq_reminders SET group_id=$4,recipient=$5,private_copy=$6 WHERE network_id=$1 AND member_id=$2 AND cpu_name=$3 AND session_id=$7 AND entry_id IS NULL AND state='watching' RETURNING id`, network, member, target.Name, in.Notification.GroupID, in.Notification.Recipient, in.Notification.PrivateCopy, session).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var active int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM qq_reminders WHERE network_id=$1 AND state='watching' AND member_id=$2`, network, member).Scan(&active); err != nil {
			return err
		}
		if active >= 32 {
			return ErrCapacity
		}
		fingerprint, _ := json.Marshal(cpuQQFingerprint(*target))
		payload, _ := json.Marshal(cpuQQPayload(*target))
		_, err = tx.Exec(ctx, `INSERT INTO qq_reminders(id,network_id,member_id,request_key,cpu_name,session_id,cpu_fingerprint,group_id,recipient,private_copy,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, network, member, in.RequestKey, target.Name, session, fingerprint, in.Notification.GroupID, in.Notification.Recipient, in.Notification.PrivateCopy, payload)
		return err
	})
	return id, err
}
func (s *Store) EditQQReminder(ctx context.Context, member, network, id string, n *protocol.QQNotification) error {
	if err := s.RequireAccess(ctx, member, network, true); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if n != nil {
			if err := validateQQTarget(ctx, tx, network, n); err != nil {
				return err
			}
		}
		var owner, state string
		if err := tx.QueryRow(ctx, `SELECT member_id,state FROM qq_reminders WHERE id=$1 AND network_id=$2 FOR UPDATE`, id, network).Scan(&owner, &state); err != nil {
			return notFound(err)
		}
		if owner != member {
			if err := s.RequireAdmin(ctx, member); err != nil {
				return err
			}
		}
		if state != "watching" {
			return ErrConflict
		}
		if n == nil {
			_, err := tx.Exec(ctx, `UPDATE qq_reminders SET state='canceled',finished_at=now() WHERE id=$1`, id)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE qq_reminders SET group_id=$2,recipient=$3,private_copy=$4 WHERE id=$1`, id, n.GroupID, n.Recipient, n.PrivateCopy)
		return err
	})
}
func cpuQQFingerprint(cpu protocol.CPU) map[string]any {
	v := map[string]any{"name": cpu.Name, "storage": cpu.Storage, "coprocessors": cpu.Coprocessors}
	if cpu.Output != nil {
		v["output"] = map[string]any{"kind": cpu.Output.Kind, "name": cpu.Output.Name, "damage": cpu.Output.Damage, "label": cpu.Output.Label, "has_tag": cpu.Output.HasTag}
	}
	return v
}
func cpuQQPayload(cpu protocol.CPU) map[string]any {
	v := map[string]any{"cpu": cpu.Name, "title": "CPU 任务结束提醒"}
	if cpu.Output != nil {
		v["item"] = cpu.Output.Label
		v["registry"] = cpu.Output.Name
		v["quantity"] = cpu.Output.Quantity
		v["unit"] = cpu.Output.Kind
	}
	return v
}
func updateQQCPUWatches(ctx context.Context, tx pgx.Tx, network string, p protocol.Poll) error {
	if p.CPUs == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id,cpu_name,session_id,cpu_fingerprint FROM qq_reminders WHERE network_id=$1 AND entry_id IS NULL AND state='watching' FOR UPDATE`, network)
	if err != nil {
		return err
	}
	type watch struct {
		id, name, session string
		fingerprint       []byte
	}
	var watches []watch
	for rows.Next() {
		var v watch
		if err = rows.Scan(&v.id, &v.name, &v.session, &v.fingerprint); err != nil {
			rows.Close()
			return err
		}
		watches = append(watches, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, w := range watches {
		reason := ""
		matches := []protocol.CPU{}
		for _, cpu := range *p.CPUs {
			if cpu.Name == w.name {
				matches = append(matches, cpu)
			}
		}
		if w.session != p.SessionID {
			reason = "OC 已重连，无法确认原任务结果"
		} else if p.CPUsTruncated {
			continue
		} else if len(matches) != 1 {
			reason = "CPU 消失或名称不再唯一，无法确认原任务结果"
		} else if !matches[0].Busy {
			reason = "CPU 已空闲；游戏内任务无法区分成功与取消"
		} else {
			var original map[string]any
			if err = json.Unmarshal(w.fingerprint, &original); err != nil {
				return err
			}
			current, marshalErr := json.Marshal(cpuQQFingerprint(matches[0]))
			if marshalErr != nil {
				return marshalErr
			}
			var next map[string]any
			if err = json.Unmarshal(current, &next); err != nil {
				return err
			}
			a, _ := json.Marshal(original)
			b, _ := json.Marshal(next)
			if string(a) != string(b) {
				reason = "CPU 产物或配置已变化，无法确认原任务结果"
			}
		}
		if reason == "" {
			continue
		}
		if _, err = tx.Exec(ctx, `SELECT qq_enqueue(n.server_group,r.network_id,r.member_id,'reminder:'||r.id||':end','cpu_end',r.group_id,r.recipient,r.private_copy,r.payload||jsonb_build_object('state','unconfirmed','reason',$2::text,'at',now())) FROM qq_reminders r JOIN networks n ON n.id=r.network_id WHERE r.id=$1`, w.id, reason); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE qq_reminders SET state='uncertain',finished_at=now() WHERE id=$1`, w.id); err != nil {
			return err
		}
	}
	return nil
}
