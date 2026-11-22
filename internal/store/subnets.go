package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type SubnetPlugin struct {
	Enabled  bool             `json:"enabled"`
	Revision int64            `json:"revision"`
	Plugin   plugins.Manifest `json:"plugin"`
}
type SubnetInput struct {
	Name     string `json:"name"`
	Address  string `json:"component_address"`
	Location string `json:"location"`
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval_seconds"`
	History  int    `json:"history_seconds"`
	Stale    int    `json:"stale_seconds"`
	Revision int64  `json:"revision"`
}
type Subnet struct {
	ID          string             `json:"id"`
	Collector   string             `json:"collector_id"`
	Name        string             `json:"name"`
	Address     string             `json:"component_address"`
	Location    string             `json:"location"`
	Enabled     bool               `json:"enabled"`
	Interval    int                `json:"interval_seconds"`
	History     int                `json:"history_seconds"`
	Stale       int                `json:"stale_seconds"`
	Revision    int64              `json:"revision"`
	Online      bool               `json:"online"`
	Seen        *time.Time         `json:"last_seen,omitempty"`
	Components  []MonitorComponent `json:"components"`
	Count       int                `json:"item_count"`
	CycleMillis int64              `json:"cycle_millis"`
}
type SubnetCatalogItem struct {
	ID string `json:"id"`
	protocol.Definition
	Catalog *protocol.CatalogItem `json:"catalog,omitempty"`
}
type SubnetItem struct {
	SubnetCatalogItem
	Quantity *protocol.Quantity `json:"quantity"`
	Observed *time.Time         `json:"observed_at"`
	Error    string             `json:"error,omitempty"`
	Known    bool               `json:"known"`
	Epoch    int64              `json:"epoch"`
}
type SubnetRuleInput struct {
	ItemID       string                   `json:"item_id"`
	Kind         string                   `json:"kind"`
	Threshold    protocol.Quantity        `json:"threshold"`
	Recover      protocol.Quantity        `json:"recover"`
	Duration     int                      `json:"duration_seconds"`
	Enabled      bool                     `json:"enabled"`
	Revision     int64                    `json:"revision"`
	Notification *protocol.QQNotification `json:"notification"`
}
type SubnetRule struct {
	SubnetRuleInput
	ID       string `json:"id"`
	MemberID string `json:"member_id"`
	Active   bool   `json:"active"`
}
type SubnetAlert struct {
	ID       string            `json:"id"`
	ItemID   string            `json:"item_id"`
	Kind     string            `json:"kind"`
	State    string            `json:"state"`
	Quantity protocol.Quantity `json:"quantity"`
	At       time.Time         `json:"at"`
	Resolved *time.Time        `json:"resolved_at,omitempty"`
}
type SubnetView struct {
	Subnet    Subnet                `json:"subnet"`
	Items     []SubnetItem          `json:"items"`
	Rules     []SubnetRule          `json:"rules"`
	Alerts    []SubnetAlert         `json:"alerts"`
	Discovery SubnetDiscoveryStatus `json:"discovery"`
}

func (s *Store) SubnetPlugin(ctx context.Context, member, group string) (SubnetPlugin, error) {
	var v SubnetPlugin
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return v, err
	}
	for _, p := range plugins.Catalog() {
		if p.ID == plugins.Subnet {
			v.Plugin = p
		}
	}
	err := s.pool.QueryRow(ctx, `SELECT enabled,revision FROM subnet_plugins WHERE server_group=$1`, group).Scan(&v.Enabled, &v.Revision)
	if err == pgx.ErrNoRows {
		err = nil
	}
	return v, err
}
func (s *Store) SetSubnetPlugin(ctx context.Context, member, group string, enabled bool, revision int64) error {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	if revision < 0 {
		return ErrInvalid
	}
	if enabled && s.catalog == nil {
		return ErrCapability
	}
	var records [][]any
	if enabled {
		var count int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM subnet_catalog`).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			candidates := s.catalog.PlainCandidates()
			for _, d := range s.catalog.ResourceCandidates() {
				if d.Kind == "fluid" {
					candidates = append(candidates, d)
				}
			}
			variants, err := s.catalog.VariantCandidates()
			if err != nil {
				return err
			}
			candidates = append(candidates, variants...)
			records = make([][]any, 0, len(candidates))
			seen := map[string]bool{}
			for _, d := range candidates {
				if d.Kind != "item" && d.Kind != "fluid" || len(d.NBT) > 5464 {
					continue
				}
				h := sha256.Sum256([]byte(d.Key()))
				id := hex.EncodeToString(h[:16])
				if seen[id] {
					continue
				}
				seen[id] = true
				label := s.catalog.DefinitionLabel(d)
				records = append(records, []any{id, d.Kind, d.Name, d.Damage, d.NBT, label, searchLabel(label)})
			}
		}
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT name FROM server_groups WHERE name=$1 FOR UPDATE`, group); err != nil {
			return err
		}
		var current int64
		err := tx.QueryRow(ctx, `SELECT revision FROM subnet_plugins WHERE server_group=$1 FOR UPDATE`, group).Scan(&current)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if current != revision {
			return ErrConflict
		}
		if enabled {
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(614238429)`); err != nil {
				return err
			}
			var catalogCount int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM subnet_catalog`).Scan(&catalogCount); err != nil {
				return err
			}
			if catalogCount == 0 {
				if _, err = tx.CopyFrom(ctx, pgx.Identifier{"subnet_catalog"}, []string{"id", "kind", "name", "damage", "nbt", "label", "search_label"}, pgx.CopyFromRows(records)); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `WITH ordered AS (SELECT id,row_number() OVER(ORDER BY CASE WHEN kind='fluid' THEN 0 WHEN nbt='' THEN 1 ELSE 2 END,id) position FROM subnet_catalog) UPDATE subnet_catalog d SET scan_order=o.position FROM ordered o WHERE o.id=d.id`); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,$2) ON CONFLICT(server_group) DO UPDATE SET enabled=excluded.enabled,revision=subnet_plugins.revision+1`, group, enabled)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE subnet_id IN(SELECT id FROM monitored_subnets WHERE server_group=$1)`, group); err != nil {
			return err
		}
		if !enabled {
			_, err = tx.Exec(ctx, `UPDATE subnet_alerts SET state='resolved',resolved_at=now(),resolve_reason='plugin_disabled' WHERE rule_id IN(SELECT r.id FROM subnet_rules r JOIN monitored_subnets n ON n.id=r.subnet_id WHERE n.server_group=$1) AND state='active'`, group)
		}
		return err
	})
}
func (s *Store) SearchSubnetCatalog(ctx context.Context, member, group, kind, q string) ([]SubnetCatalogItem, error) {
	out := []SubnetCatalogItem{}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	if len(q) > 256 || kind != "item" && kind != "fluid" {
		return nil, ErrInvalid
	}
	args := []any{kind}
	conditions, err := searchConditions(q, "d.label", "d.search_label", func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) })
	if err != nil {
		return nil, err
	}
	sql := `SELECT d.id,d.kind,d.name,d.damage,d.nbt,d.label FROM subnet_catalog d WHERE d.kind=$1`
	if len(conditions) > 0 {
		sql += " AND " + strings.Join(conditions, " AND ")
	}
	sql += ` ORDER BY d.label,d.id LIMIT 50`
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v SubnetCatalogItem
		if err = rows.Scan(&v.ID, &v.Kind, &v.Name, &v.Damage, &v.NBT, &v.Label); err != nil {
			return nil, err
		}
		v.Exact = true
		s.subnetCatalogMeta(&v)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) subnetCatalogMeta(v *SubnetCatalogItem) {
	if s.catalog == nil {
		return
	}
	match, state, ok := s.catalog.LookupResource(v.Kind, v.Name, v.Damage, v.NBT)
	if !ok && v.NBT != "" {
		match, state, ok = s.catalog.LookupResource(v.Kind, v.Name, v.Damage, "")
	}
	if !ok {
		return
	}
	p := match.Public(state)
	p.LocalizedName = v.Label
	if state == "available" {
		p.ImageURL = "/api/v1/subnet-catalog/" + v.ID + "/image"
	}
	v.Catalog = &p
}
func validSubnet(in SubnetInput) bool {
	return strings.TrimSpace(in.Name) != "" && monitorText(in.Name, 128) && monitorText(in.Location, 128) && (in.Address == "" || componentAddress.MatchString(in.Address)) && in.Interval >= 1 && in.Interval <= 3600 && in.History >= 5 && in.History <= 3600 && in.Stale >= max(10, in.Interval*3) && in.Stale <= 10800 && in.Revision >= 0
}
func (s *Store) CreateSubnet(ctx context.Context, member, group string, in SubnetInput) (string, string, error) {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return "", "", err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return "", "", err
	}
	if !validSubnet(in) || in.Revision != 0 {
		return "", "", ErrInvalid
	}
	id, collector, token := newID(), newID(), newToken("ac_sub_")
	h := hashToken(token)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT name FROM server_groups WHERE name=$1 FOR UPDATE`, group); err != nil {
			return err
		}
		var enabled bool
		var count int
		if err := tx.QueryRow(ctx, `SELECT enabled FROM subnet_plugins WHERE server_group=$1`, group).Scan(&enabled); err != nil {
			return notFound(err)
		}
		if !enabled {
			return ErrCapability
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitored_subnets WHERE server_group=$1`, group).Scan(&count); err != nil {
			return err
		}
		if count >= 16 {
			return ErrCapacity
		}
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_collectors(id,server_group,name,token_hash,purpose) VALUES($1,$2,$3,$4,'subnet')`, collector, group, in.Name, h[:]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO monitored_subnets(id,server_group,collector_id,name,component_address,location,enabled,interval_seconds,history_seconds,stale_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, group, collector, in.Name, in.Address, in.Location, in.Enabled, in.Interval, in.History, in.Stale)
		return err
	})
	return id, token, err
}
func (s *Store) Subnets(ctx context.Context, member, group string) ([]Subnet, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	return s.subnets(ctx, group)
}
func (s *Store) subnets(ctx context.Context, group string) ([]Subnet, error) {
	rows, err := s.pool.Query(ctx, `SELECT n.id,n.collector_id,n.name,n.component_address,n.location,n.enabled,n.interval_seconds,n.history_seconds,n.stale_seconds,n.revision,coalesce(c.last_seen>now()-n.stale_seconds*interval '1 second' AND NOT c.revoked,false),c.last_seen,c.components,(SELECT count(*) FROM subnet_items i WHERE i.subnet_id=n.id AND i.enabled),n.cycle_millis FROM monitored_subnets n JOIN monitor_collectors c ON c.id=n.collector_id WHERE n.server_group=$1 ORDER BY n.created_at,n.id`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subnet{}
	for rows.Next() {
		var v Subnet
		var raw []byte
		if err = rows.Scan(&v.ID, &v.Collector, &v.Name, &v.Address, &v.Location, &v.Enabled, &v.Interval, &v.History, &v.Stale, &v.Revision, &v.Online, &v.Seen, &raw, &v.Count, &v.CycleMillis); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v.Components); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateSubnet(ctx context.Context, member, group, id string, in SubnetInput) error {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	if !validSubnet(in) {
		return ErrInvalid
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE monitored_subnets SET name=$3,component_address=$4,location=$5,enabled=$6,interval_seconds=$7,history_seconds=$8,stale_seconds=$9,revision=revision+1 WHERE id=$1 AND server_group=$2 AND revision=$10`, id, group, in.Name, in.Address, in.Location, in.Enabled, in.Interval, in.History, in.Stale, in.Revision)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_alerts SET state='resolved',resolved_at=now(),resolve_reason='binding_changed' WHERE rule_id IN(SELECT id FROM subnet_rules WHERE subnet_id=$1) AND state='active'`, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE subnet_id=$1`, id)
		return err
	})
}
func (s *Store) SubnetView(ctx context.Context, member, group, id string) (SubnetView, error) {
	v := SubnetView{Items: []SubnetItem{}, Rules: []SubnetRule{}}
	ns, err := s.Subnets(ctx, member, group)
	if err != nil {
		return v, err
	}
	found := false
	for _, n := range ns {
		if n.ID == id {
			v.Subnet = n
			found = true
		}
	}
	if !found {
		return v, ErrNotFound
	}
	v.Discovery, err = s.subnetDiscoveryStatus(ctx, id)
	if err != nil {
		return v, err
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.nbt,d.label,l.quantity,l.observed_at,coalesce(l.error,''),coalesce(l.epoch,1),coalesce(l.error='' AND l.observed_at>now()-n.stale_seconds*interval '1 second' AND l.revision=n.revision AND l.session_id=c.current_session AND c.last_seen>now()-n.stale_seconds*interval '1 second' AND NOT c.revoked AND n.enabled AND p.enabled,false) FROM subnet_items i JOIN subnet_catalog d ON d.id=i.item_id JOIN monitored_subnets n ON n.id=i.subnet_id JOIN subnet_plugins p ON p.server_group=n.server_group JOIN monitor_collectors c ON c.id=n.collector_id LEFT JOIN subnet_latest l USING(subnet_id,item_id) WHERE i.subnet_id=$1 AND i.enabled ORDER BY d.label,d.id`, id)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var x SubnetItem
		if err = rows.Scan(&x.ID, &x.Kind, &x.Name, &x.Damage, &x.NBT, &x.Label, &x.Quantity, &x.Observed, &x.Error, &x.Epoch, &x.Known); err != nil {
			rows.Close()
			return v, err
		}
		x.Exact = true
		s.subnetCatalogMeta(&x.SubnetCatalogItem)
		v.Items = append(v.Items, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return v, err
	}
	rows, err = s.pool.Query(ctx, `SELECT r.id,r.item_id,r.kind,r.threshold,r.recover,r.duration_seconds,r.enabled,r.revision,r.notification,r.member_id,EXISTS(SELECT 1 FROM subnet_alerts a WHERE a.rule_id=r.id AND a.state='active') FROM subnet_rules r WHERE r.subnet_id=$1 AND (r.member_id=$2 OR EXISTS(SELECT 1 FROM members WHERE id=$2 AND is_admin)) ORDER BY r.id`, id, member)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SubnetRule
		var raw []byte
		if err = rows.Scan(&r.ID, &r.ItemID, &r.Kind, &r.Threshold, &r.Recover, &r.Duration, &r.Enabled, &r.Revision, &raw, &r.MemberID, &r.Active); err != nil {
			return v, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &r.Notification); err != nil {
				return v, err
			}
		}
		v.Rules = append(v.Rules, r)
	}
	if err = rows.Err(); err != nil {
		return v, err
	}
	rows.Close()
	v.Alerts = []SubnetAlert{}
	rows, err = s.pool.Query(ctx, `SELECT a.id,r.item_id,r.kind,a.state,a.quantity,a.triggered_at,a.resolved_at FROM subnet_alerts a JOIN subnet_rules r ON r.id=a.rule_id WHERE r.subnet_id=$1 AND r.member_id=$2 ORDER BY a.triggered_at DESC,a.id DESC LIMIT 100`, id, member)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var a SubnetAlert
		if err = rows.Scan(&a.ID, &a.ItemID, &a.Kind, &a.State, &a.Quantity, &a.At, &a.Resolved); err != nil {
			return v, err
		}
		v.Alerts = append(v.Alerts, a)
	}
	return v, rows.Err()
}
func (s *Store) AddSubnetItem(ctx context.Context, member, group, subnet, item string) error {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	if !monitorID.MatchString(item) {
		return ErrInvalid
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var count int
		var active bool
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM subnet_items WHERE subnet_id=n.id AND enabled),EXISTS(SELECT 1 FROM subnet_items WHERE subnet_id=n.id AND item_id=$3 AND enabled) FROM monitored_subnets n WHERE n.id=$1 AND n.server_group=$2 FOR UPDATE`, subnet, group, item).Scan(&count, &active); err != nil {
			return notFound(err)
		}
		if active {
			return nil
		}
		if count >= 64 {
			return ErrCapacity
		}
		tag, err := tx.Exec(ctx, `INSERT INTO subnet_items(subnet_id,item_id) SELECT $1,id FROM subnet_catalog WHERE id=$2 ON CONFLICT(subnet_id,item_id) DO UPDATE SET enabled=true,next_read_at=now(),assignment_until=NULL`, subnet, item)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var found bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subnet_items WHERE subnet_id=$1 AND item_id=$2)`, subnet, item).Scan(&found); err != nil {
				return err
			}
			if !found {
				return ErrNotFound
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_items SET next_read_at=now() WHERE subnet_id=$1 AND item_id=$2`, subnet, item); err != nil {
			return err
		}
		return nil
	})
}
func validSubnetRule(in SubnetRuleInput) bool {
	if !monitorID.MatchString(in.ItemID) || in.Threshold < 0 || in.Recover < 0 || in.Duration < 0 || in.Duration > 3600 || in.Revision < 0 {
		return false
	}
	return in.Kind == "low" && in.Recover > in.Threshold || in.Kind == "high" && in.Recover < in.Threshold || in.Kind == "zero" && in.Threshold == 0 && in.Recover > 0
}
func (s *Store) SetSubnetRule(ctx context.Context, member, group, subnet, id string, in SubnetRuleInput) (string, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return "", err
	}
	if !validSubnetRule(in) {
		return "", ErrInvalid
	}
	if id == "" {
		id = newID()
		if in.Revision != 0 {
			return "", ErrInvalid
		}
	} else if !monitorID.MatchString(id) {
		return "", ErrInvalid
	}
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM monitored_subnets n JOIN subnet_items i ON i.subnet_id=n.id WHERE n.id=$1 AND n.server_group=$2 AND i.item_id=$3 AND i.enabled)`, subnet, group, in.ItemID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		if in.Notification != nil {
			if in.Notification.Validate() != nil {
				return ErrInvalid
			}
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qq_connectors c JOIN qq_groups g ON g.server_group=c.server_group WHERE c.server_group=$1 AND c.enabled AND c.catalog_ready AND g.enabled AND g.group_id=$2 AND (NOT $3 OR c.allow_private))`, group, in.Notification.GroupID, in.Notification.PrivateCopy).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrForbidden
			}
		}
		var current int64
		var owner string
		err := tx.QueryRow(ctx, `SELECT revision,member_id FROM subnet_rules WHERE id=$1 AND subnet_id=$2 FOR UPDATE`, id, subnet).Scan(&current, &owner)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if current != in.Revision {
			return ErrConflict
		}
		if owner != "" && owner != member {
			return ErrForbidden
		}
		raw, _ := json.Marshal(in.Notification)
		if current == 0 {
			tag, err := tx.Exec(ctx, `INSERT INTO subnet_rules(id,subnet_id,item_id,member_id,kind,threshold,recover,duration_seconds,enabled,notification) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(subnet_id,item_id,member_id,kind) DO NOTHING`, id, subnet, in.ItemID, member, in.Kind, int64(in.Threshold), int64(in.Recover), in.Duration, in.Enabled, raw)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrConflict
			}
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_rules SET item_id=$2,kind=$3,threshold=$4,recover=$5,duration_seconds=$6,enabled=$7,notification=$8,revision=revision+1,since=NULL WHERE id=$1`, id, in.ItemID, in.Kind, int64(in.Threshold), int64(in.Recover), in.Duration, in.Enabled, raw); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE subnet_alerts SET state='resolved',resolved_at=now(),resolve_reason='rule_changed' WHERE rule_id=$1 AND state='active'`, id)
		return err
	})
	return id, err
}
func (s *Store) RemoveSubnetRule(ctx context.Context, member, group, subnet, id string) error {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE subnet_rules SET enabled=false,revision=revision+1,since=NULL WHERE id=$1 AND subnet_id=$2 AND member_id=$3 AND EXISTS(SELECT 1 FROM monitored_subnets WHERE id=$2 AND server_group=$4)`, id, subnet, member, group)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `UPDATE subnet_alerts SET state='resolved',resolved_at=now(),resolve_reason='rule_disabled' WHERE rule_id=$1 AND state='active'`, id)
		return err
	})
}

func (s *Store) RemoveSubnetItem(ctx context.Context, member, group, subnet, item string) error {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE subnet_items i SET enabled=false,assignment_until=NULL FROM monitored_subnets n WHERE n.id=i.subnet_id AND n.server_group=$1 AND i.subnet_id=$2 AND i.item_id=$3`, group, subnet, item)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		if _, err = tx.Exec(ctx, `UPDATE subnet_rules SET enabled=false,since=NULL,revision=revision+1 WHERE subnet_id=$1 AND item_id=$2`, subnet, item); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE subnet_alerts SET state='resolved',resolved_at=now(),resolve_reason='item_removed' WHERE rule_id IN(SELECT id FROM subnet_rules WHERE subnet_id=$1 AND item_id=$2) AND state='active'`, subnet, item)
		return err
	})
}
func (s *Store) ResetSubnetToken(ctx context.Context, member, group, subnet string) (string, error) {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return "", err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return "", err
	}
	token := newToken("ac_sub_")
	h := hashToken(token)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var collector string
		if err := tx.QueryRow(ctx, `SELECT collector_id FROM monitored_subnets WHERE id=$1 AND server_group=$2`, subnet, group).Scan(&collector); err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE monitor_collectors SET token_hash=$2,current_session='',last_seen=NULL WHERE id=$1 AND purpose='subnet'`, collector, h[:]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE monitor_sessions SET retired=true WHERE collector_id=$1`, collector); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE subnet_rules SET since=NULL WHERE subnet_id=$1`, subnet)
		return err
	})
	return token, err
}

type SubnetHistoryPoint struct {
	At       time.Time         `json:"at"`
	Quantity protocol.Quantity `json:"quantity"`
	Epoch    int64             `json:"epoch"`
}

func (s *Store) SubnetHistory(ctx context.Context, member, group, subnet, item string) ([]SubnetHistoryPoint, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT h.observed_at,h.quantity,h.epoch FROM subnet_history h JOIN monitored_subnets n ON n.id=h.subnet_id WHERE h.subnet_id=$1 AND h.item_id=$2 AND n.server_group=$3 ORDER BY h.observed_at DESC,h.id DESC LIMIT 1000`, subnet, item, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubnetHistoryPoint{}
	for rows.Next() {
		var p SubnetHistoryPoint
		if err = rows.Scan(&p.At, &p.Quantity, &p.Epoch); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
