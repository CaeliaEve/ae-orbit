package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/jackc/pgx/v5"
)

type MonitorCollector struct {
	ID         string             `json:"id"`
	Group      string             `json:"server_group"`
	Name       string             `json:"name"`
	Revoked    bool               `json:"revoked"`
	Online     bool               `json:"online"`
	LastSeen   *time.Time         `json:"last_seen,omitempty"`
	FreeMemory int64              `json:"free_memory"`
	Components []MonitorComponent `json:"components"`
	Modules    []string           `json:"modules"`
}
type MonitorComponent struct {
	Address   string `json:"address"`
	Type      string `json:"type"`
	Supported bool   `json:"supported"`
}
type MonitorAuth struct {
	ID, Group string
	Purpose   string
	Hash      []byte
}
type PluginInstallation struct {
	Plugin   plugins.Manifest `json:"plugin"`
	Enabled  bool             `json:"enabled"`
	Revision int64            `json:"revision"`
	Settings plugins.Settings `json:"settings"`
}

// Flow values are observed EU per game tick, not energy or real-time EU/s.
type MonitorFlow struct {
	Input         string `json:"input_eut"`
	Output        string `json:"output_eut"`
	Loss          string `json:"loss_eut"`
	AverageInput  string `json:"avg_input_eut"`
	AverageOutput string `json:"avg_output_eut"`
	WindowSeconds int    `json:"window_seconds"`
	Source        string `json:"source"`
}

func (f *MonitorFlow) valid() bool {
	if f == nil {
		return true
	}
	if f.Source != "lsc_sensor_v1" || f.WindowSeconds != 5 {
		return false
	}
	for _, v := range []string{f.Input, f.Output, f.Loss, f.AverageInput, f.AverageOutput} {
		if _, err := plugins.Energy(v); err != nil {
			return false
		}
	}
	return true
}

type MonitorObject struct {
	SamplingInterval  int          `json:"sample_interval_seconds"`
	RequestedInterval int          `json:"requested_interval_seconds"`
	HistoryInterval   int          `json:"history_interval_seconds"`
	Flow              *MonitorFlow `json:"flow,omitempty"`
	FlowError         string       `json:"flow_error,omitempty"`
	ID                string       `json:"id"`
	PluginID          string       `json:"plugin_id"`
	CollectorID       string       `json:"collector_id"`
	Address           string       `json:"component_address"`
	Name              string       `json:"name"`
	Location          string       `json:"location"`
	Enabled           bool         `json:"enabled"`
	Revision          int64        `json:"revision"`
	Status            string       `json:"status"`
	Stored            string       `json:"stored_eu,omitempty"`
	Capacity          string       `json:"capacity_eu,omitempty"`
	Percent           string       `json:"percent,omitempty"`
	ObservedAt        *time.Time   `json:"observed_at,omitempty"`
	Error             string       `json:"error,omitempty"`
	Epoch             int64        `json:"epoch"`
}
type MonitorObjectInput struct {
	PluginID    string `json:"plugin_id"`
	CollectorID string `json:"collector_id"`
	Address     string `json:"component_address"`
	Name        string `json:"name"`
	Location    string `json:"location"`
	Enabled     bool   `json:"enabled"`
	Revision    int64  `json:"revision"`
}
type MonitorHello struct {
	MinInterval int                `json:"min_interval_seconds,omitempty"`
	BootID      string             `json:"boot_id"`
	Modules     []string           `json:"modules"`
	Components  []MonitorComponent `json:"components"`
}
type MonitorAssignment struct {
	ID                   string `json:"id"`
	PluginID             string `json:"plugin_id"`
	Address              string `json:"component_address"`
	Revision             int64  `json:"revision"`
	InstallationRevision int64  `json:"installation_revision"`
	Interval             int    `json:"interval_seconds"`
}
type MonitorWelcome struct {
	Session     string              `json:"session_id"`
	Sequence    int64               `json:"sequence"`
	PollSeconds int                 `json:"poll_seconds"`
	Objects     []MonitorAssignment `json:"objects"`
}
type MonitorSample struct {
	Flow                 *MonitorFlow `json:"flow,omitempty"`
	FlowError            string       `json:"flow_error,omitempty"`
	ObjectID             string       `json:"object_id"`
	Revision             int64        `json:"revision"`
	InstallationRevision int64        `json:"installation_revision"`
	SampleMillis         int64        `json:"sample_millis"`
	Stored               string       `json:"stored_eu,omitempty"`
	Capacity             string       `json:"capacity_eu,omitempty"`
	Error                string       `json:"error,omitempty"`
}
type MonitorPoll struct {
	Session    string              `json:"session_id"`
	Sequence   int64               `json:"sequence"`
	FreeMemory int64               `json:"free_memory"`
	Samples    []MonitorSample     `json:"samples"`
	Components *[]MonitorComponent `json:"components,omitempty"`
}

var monitorID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var componentAddress = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// Monitoring transactions never dispatch or reconcile AE production jobs.
func (s *Store) monitorTransaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func monitorText(s string, max int) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}
func (s *Store) RequireGroupAccess(ctx context.Context, member, group string) error {
	var yes bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM server_groups g JOIN members m ON m.id=$1 WHERE g.name=$2 AND NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM networks n JOIN network_grants a ON a.network_id=n.id WHERE n.server_group=g.name AND a.member_id=m.id)))`, member, group).Scan(&yes)
	if err != nil {
		return err
	}
	if !yes {
		return ErrForbidden
	}
	return nil
}
func (s *Store) MonitoringGroups(ctx context.Context, member string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.name FROM server_groups g JOIN members m ON m.id=$1 WHERE NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM networks n JOIN network_grants a ON a.network_id=n.id WHERE n.server_group=g.name AND a.member_id=m.id)) ORDER BY g.name`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var x string
		if err = rows.Scan(&x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) MonitoringPlugins(ctx context.Context, member, group string) ([]PluginInstallation, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	out := []PluginInstallation{}
	for _, p := range plugins.Catalog() {
		if p.ID != plugins.LSC {
			continue
		}
		v := PluginInstallation{Plugin: p, Settings: plugins.Defaults()}
		var raw []byte
		err := s.pool.QueryRow(ctx, `SELECT enabled,revision,settings FROM plugin_installations WHERE server_group=$1 AND plugin_id=$2`, group, p.ID).Scan(&v.Enabled, &v.Revision, &raw)
		if err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &v.Settings); err != nil {
				return nil, err
			}
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) SetMonitoringPlugin(ctx context.Context, member, group, id string, enabled bool, settings plugins.Settings, revision int64) error {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	if !plugins.Supported(id) || !settings.Valid() || revision < 0 {
		return ErrInvalid
	}
	if settings.HistorySeconds == 0 {
		settings.HistorySeconds = 10
	}
	raw, _ := json.Marshal(settings)
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		// Group lock also serializes first installation and collector capacity.
		if _, err := tx.Exec(ctx, `SELECT name FROM server_groups WHERE name=$1 FOR UPDATE`, group); err != nil {
			return err
		}
		var current int64
		err := tx.QueryRow(ctx, `SELECT revision FROM plugin_installations WHERE server_group=$1 AND plugin_id=$2 FOR UPDATE`, group, id).Scan(&current)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if current != revision {
			return ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO plugin_installations(server_group,plugin_id,enabled,settings) VALUES($1,$2,$3,$4) ON CONFLICT(server_group,plugin_id) DO UPDATE SET enabled=excluded.enabled,settings=excluded.settings,revision=plugin_installations.revision+1,updated_at=now()`, group, id, enabled, raw)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id IN(SELECT id FROM plugin_objects WHERE server_group=$1 AND plugin_id=$2)`, group, id)
		if err != nil {
			return err
		}
		if !enabled || !settings.Alerts {
			_, err = tx.Exec(ctx, `UPDATE monitor_alerts SET state='resolved',resolved_at=now(),resolve_reason='monitoring_disabled' WHERE state='active' AND object_id IN(SELECT id FROM plugin_objects WHERE server_group=$1 AND plugin_id=$2)`, group, id)
			if err != nil {
				return err
			}
		}
		return monitorAudit(ctx, tx, group, member, "plugin_configured", id)
	})
}
func monitorAudit(ctx context.Context, tx pgx.Tx, group, member, action, target string) error {
	_, err := tx.Exec(ctx, `INSERT INTO plugin_audit(server_group,member_id,action,target_id) VALUES($1,$2,$3,$4)`, group, member, action, target)
	return err
}
func (s *Store) CreateMonitorCollector(ctx context.Context, member, group, name string) (MonitorCollector, string, error) {
	c := MonitorCollector{ID: newID(), Group: group, Name: strings.TrimSpace(name), Components: []MonitorComponent{}, Modules: []string{}}
	if err := s.RequireAdmin(ctx, member); err != nil {
		return c, "", err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return c, "", err
	}
	if c.Name == "" || !monitorText(c.Name, 128) {
		return c, "", ErrInvalid
	}
	token := newToken("ac_mon_")
	hash := hashToken(token)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT name FROM server_groups WHERE name=$1 FOR UPDATE`, group); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitor_collectors WHERE server_group=$1 AND NOT revoked AND purpose='lsc'`, group).Scan(&count); err != nil {
			return err
		}
		if count >= 16 {
			return ErrCapacity
		}
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_collectors(id,server_group,name,token_hash) VALUES($1,$2,$3,$4)`, c.ID, group, c.Name, hash[:]); err != nil {
			return err
		}
		return monitorAudit(ctx, tx, group, member, "collector_created", c.ID)
	})
	if err != nil {
		return c, "", err
	}
	return c, token, nil
}
func (s *Store) MonitorCollectors(ctx context.Context, member, group string) ([]MonitorCollector, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name,revoked,last_seen,free_memory,components,modules,coalesce(last_seen>now()-interval '60 seconds' AND NOT revoked,false) FROM monitor_collectors WHERE server_group=$1 AND purpose='lsc' ORDER BY created_at,id`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorCollector{}
	for rows.Next() {
		var c MonitorCollector
		var a, b []byte
		c.Group = group
		if err = rows.Scan(&c.ID, &c.Name, &c.Revoked, &c.LastSeen, &c.FreeMemory, &a, &b, &c.Online); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(a, &c.Components); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &c.Modules); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) ChangeMonitorToken(ctx context.Context, member, group, id string, revoke bool) (string, error) {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return "", err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return "", err
	}
	token := newToken("ac_mon_")
	h := hashToken(token)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE monitor_collectors SET token_hash=$3,revoked=$4,current_session='',last_seen=NULL WHERE server_group=$1 AND id=$2 AND purpose='lsc'`, group, id, h[:], revoke)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE monitor_sessions SET retired=true WHERE collector_id=$1`, id)
		if err != nil {
			return err
		}
		return monitorAudit(ctx, tx, group, member, "collector_credentials_changed", id)
	})
	if revoke {
		token = ""
	}
	return token, err
}
func (s *Store) AuthMonitor(ctx context.Context, token string) (MonitorAuth, error) {
	var a MonitorAuth
	if len(token) < 32 || len(token) > 256 {
		return a, ErrUnauthorized
	}
	h := hashToken(token)
	a.Hash = h[:]
	err := s.pool.QueryRow(ctx, `SELECT id,server_group,purpose FROM monitor_collectors WHERE token_hash=$1 AND NOT revoked`, h[:]).Scan(&a.ID, &a.Group, &a.Purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthorized
	}
	return a, err
}
func (s *Store) MonitorObjects(ctx context.Context, member, group string) ([]MonitorObject, error) {
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id,o.plugin_id,o.collector_id,o.component_address,o.name,o.location,o.enabled,o.revision,coalesce(l.stored_eu::text,''),coalesce(l.capacity_eu::text,''),l.observed_at,coalesce(l.error,''),coalesce(l.epoch,1),l.flow,coalesce(l.flow_error,''),CASE WHEN NOT p.enabled OR NOT o.enabled THEN 'paused' WHEN c.revoked THEN 'revoked' WHEN c.last_seen IS NULL THEN 'waiting_collector' WHEN c.last_seen<now()-((p.settings->>'offline_seconds')::int)*interval '1 second' THEN 'offline' WHEN NOT (c.modules ? o.plugin_id) THEN 'upgrade_required' WHEN l.object_id IS NULL THEN 'waiting_sample' WHEN l.error<>'' THEN 'read_error' WHEN l.observed_at IS NULL OR l.observed_at<now()-((p.settings->>'offline_seconds')::int)*interval '1 second' THEN 'stale' ELSE 'online' END,greatest((p.settings->>'interval_seconds')::int,coalesce(b.min_interval_seconds,10)),(p.settings->>'interval_seconds')::int,greatest(coalesce((p.settings->>'history_interval_seconds')::int,10),(p.settings->>'interval_seconds')::int)
 FROM plugin_objects o JOIN plugin_installations p USING(server_group,plugin_id) JOIN monitor_collectors c ON c.id=o.collector_id LEFT JOIN monitor_sessions b ON b.id=c.current_session LEFT JOIN monitor_latest l ON l.object_id=o.id WHERE o.server_group=$1 ORDER BY o.created_at,o.id`, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorObject{}
	for rows.Next() {
		var o MonitorObject
		var flow []byte
		if err = rows.Scan(&o.ID, &o.PluginID, &o.CollectorID, &o.Address, &o.Name, &o.Location, &o.Enabled, &o.Revision, &o.Stored, &o.Capacity, &o.ObservedAt, &o.Error, &o.Epoch, &flow, &o.FlowError, &o.Status, &o.SamplingInterval, &o.RequestedInterval, &o.HistoryInterval); err != nil {
			return nil, err
		}
		if len(flow) > 0 {
			if err = json.Unmarshal(flow, &o.Flow); err != nil {
				return nil, err
			}
		}
		o.Percent = plugins.Percent(o.Stored, o.Capacity)
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) SetMonitorObject(ctx context.Context, member, group, id string, in MonitorObjectInput) (string, error) {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return "", err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return "", err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Location = strings.TrimSpace(in.Location)
	if !plugins.Supported(in.PluginID) || !monitorID.MatchString(in.CollectorID) || !componentAddress.MatchString(in.Address) || in.Name == "" || !monitorText(in.Name, 128) || !monitorText(in.Location, 128) || in.Revision < 0 {
		return "", ErrInvalid
	}
	create := id == ""
	if create {
		id = newID()
	}
	if !monitorID.MatchString(id) {
		return "", ErrInvalid
	}
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var modules, components []byte
		err := tx.QueryRow(ctx, `SELECT modules,components FROM monitor_collectors WHERE id=$1 AND server_group=$2 AND NOT revoked AND purpose='lsc' FOR UPDATE`, in.CollectorID, group).Scan(&modules, &components)
		if err != nil {
			return notFound(err)
		}
		var ms []string
		var cs []MonitorComponent
		if json.Unmarshal(modules, &ms) != nil || json.Unmarshal(components, &cs) != nil {
			return ErrCapability
		}
		supported := false
		for _, m := range ms {
			if m == in.PluginID {
				supported = true
			}
		}
		found := false
		for _, c := range cs {
			if c.Address == in.Address && c.Type == "LSC" && c.Supported {
				found = true
			}
		}
		if !supported || !found {
			return ErrCapability
		}
		if create {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM plugin_objects WHERE collector_id=$1`, in.CollectorID).Scan(&count); err != nil {
				return err
			}
			if count >= plugins.MaxObjects {
				return ErrCapacity
			}
			_, err = tx.Exec(ctx, `INSERT INTO plugin_objects(id,server_group,plugin_id,collector_id,component_address,name,location,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, group, in.PluginID, in.CollectorID, in.Address, in.Name, in.Location, in.Enabled)
		} else {
			var oldCollector, oldAddress string
			var rev int64
			err = tx.QueryRow(ctx, `SELECT collector_id,component_address,revision FROM plugin_objects WHERE id=$1 AND server_group=$2 FOR UPDATE`, id, group).Scan(&oldCollector, &oldAddress, &rev)
			if err != nil {
				return notFound(err)
			}
			if rev != in.Revision {
				return ErrConflict
			}
			if oldCollector != in.CollectorID || oldAddress != in.Address {
				return ErrInvalid
			}
			_, err = tx.Exec(ctx, `UPDATE plugin_objects SET name=$3,location=$4,enabled=$5,revision=revision+1 WHERE id=$1 AND server_group=$2`, id, group, in.Name, in.Location, in.Enabled)
		}
		if err != nil {
			return err
		}
		if !in.Enabled {
			if _, err = tx.Exec(ctx, `DELETE FROM monitor_conditions WHERE object_id=$1`, id); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE monitor_alerts SET state='resolved',resolved_at=now(),resolve_reason='monitoring_disabled' WHERE object_id=$1 AND state='active'`, id); err != nil {
				return err
			}
		}
		return monitorAudit(ctx, tx, group, member, "object_configured", id)
	})
	return id, err
}
