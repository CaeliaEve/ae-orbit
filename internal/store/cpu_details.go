package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type CPUDetailInput struct {
	SessionID string `json:"session_id"`
	CPUID     string `json:"cpu_id"`
	ViewerID  string `json:"viewer_id"`
}
type CPUDetailPart struct {
	Kind       string                   `json:"kind"`
	Items      []protocol.CPUDetailItem `json:"items"`
	Total      int                      `json:"total"`
	Truncated  bool                     `json:"truncated"`
	Error      string                   `json:"error,omitempty"`
	ObservedAt *time.Time               `json:"observed_at,omitempty"`
	ReportedAt *time.Time               `json:"reported_at,omitempty"`
}
type CPUDetailView struct {
	SessionID      string          `json:"session_id"`
	CPUID          string          `json:"cpu_id"`
	CPU            protocol.CPU    `json:"cpu"`
	Parts          []CPUDetailPart `json:"parts"`
	Status         string          `json:"status"`
	RefreshSeconds int             `json:"refresh_seconds"`
}

func (s *Store) RequestCPUDetails(ctx context.Context, member, network string, in CPUDetailInput) (CPUDetailView, error) {
	var view CPUDetailView
	if err := s.RequireAccess(ctx, member, network, false); err != nil {
		return view, err
	}
	if !monitorID.MatchString(in.ViewerID) || len(in.SessionID) < 1 || len(in.SessionID) > 128 || len(in.CPUID) < 1 || len(in.CPUID) > 128 {
		return view, ErrInvalid
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		// Match the current primary boot. A stale browser cannot activate an old CPU.
		var current string
		var caps protocol.Capabilities
		var raw []byte
		var online bool
		if err := tx.QueryRow(ctx, `SELECT n.current_session,b.capabilities,coalesce(d.last_seen>now()-interval '90 seconds',false) AND NOT d.revoked AND NOT b.retired AND b.protocol=2 AND n.bridge_mode='cooperative' AND d.current_session=b.id FROM networks n JOIN bridge_sessions b ON b.id=n.current_session JOIN devices d ON d.id=b.device_id WHERE n.id=$1 FOR UPDATE OF n`, network).Scan(&current, &raw, &online); err != nil {
			return notFound(err)
		}
		if err := json.Unmarshal(raw, &caps); err != nil {
			return err
		}
		if current != in.SessionID {
			return ErrViewChanged
		}
		if !online || !caps.CPUDetails {
			return ErrCapability
		}
		var existingSession, existingCPU string
		err := tx.QueryRow(ctx, `SELECT session_id,cpu_id FROM cpu_detail_demands WHERE network_id=$1 FOR UPDATE`, network).Scan(&existingSession, &existingCPU)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		same := err == nil && existingSession == current && existingCPU == in.CPUID
		if !same {
			var occupied bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cpu_detail_viewers v JOIN members m ON m.id=v.member_id WHERE v.network_id=$1 AND v.expires_at>now() AND NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM network_grants g WHERE g.network_id=v.network_id AND g.member_id=m.id)))`, network).Scan(&occupied); err != nil {
				return err
			}
			if occupied && existingSession == current {
				return ErrCapacity
			}
			var cpus []protocol.CPU
			var snapshot []byte
			var truncated bool
			if err := tx.QueryRow(ctx, `SELECT cpus,truncated FROM cpu_snapshots WHERE network_id=$1 AND session_id=$2 AND observed_at>now()-interval '90 seconds'`, network, current).Scan(&snapshot, &truncated); err != nil {
				return notFound(err)
			}
			if err := json.Unmarshal(snapshot, &cpus); err != nil {
				return err
			}
			var target *protocol.CPU
			for i := range cpus {
				if cpus[i].ID == in.CPUID {
					target = &cpus[i]
					break
				}
			}
			if target == nil {
				return ErrViewChanged
			}
			if !target.Busy {
				return ErrConflict
			}
			encoded, _ := json.Marshal(target)
			// Never transfer CPU ownership using a mutable enumeration index.
			if _, err := tx.Exec(ctx, `DELETE FROM cpu_detail_viewers WHERE network_id=$1`, network); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM cpu_detail_snapshots WHERE network_id=$1`, network); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO cpu_detail_demands(network_id,session_id,cpu_id,cpu_name,cpu) VALUES($1,$2,$3,$4,$5) ON CONFLICT(network_id) DO UPDATE SET session_id=excluded.session_id,cpu_id=excluded.cpu_id,cpu_name=excluded.cpu_name,cpu=excluded.cpu,category=0,next_read_at=now(),assignment_id='',assignment_kind='',assignment_expires='-infinity',last_result='',updated_at=now()`, network, current, in.CPUID, target.Name, encoded); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM cpu_detail_viewers WHERE network_id=$1 AND expires_at<=now()`, network); err != nil {
			return err
		}
		var full bool
		if err := tx.QueryRow(ctx, `SELECT count(*)>=128 AND NOT EXISTS(SELECT 1 FROM cpu_detail_viewers WHERE network_id=$1 AND member_id=$2 AND viewer_id=$3) FROM cpu_detail_viewers WHERE network_id=$1`, network, member, in.ViewerID).Scan(&full); err != nil {
			return err
		}
		if full {
			return ErrCapacity
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cpu_detail_viewers(network_id,member_id,viewer_id,expires_at) VALUES($1,$2,$3,now()+interval '15 seconds') ON CONFLICT(network_id,member_id,viewer_id) DO UPDATE SET expires_at=excluded.expires_at`, network, member, in.ViewerID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return view, err
	}
	return s.CPUDetails(ctx, member, network, in.SessionID, in.CPUID)
}
func (s *Store) CloseCPUDetails(ctx context.Context, member, network, viewer string) error {
	if err := s.RequireAccess(ctx, member, network, false); err != nil {
		return err
	}
	if !monitorID.MatchString(viewer) {
		return ErrInvalid
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM cpu_detail_viewers WHERE network_id=$1 AND member_id=$2 AND viewer_id=$3`, network, member, viewer)
	return err
}
func (s *Store) CPUDetails(ctx context.Context, member, network, session, cpu string) (CPUDetailView, error) {
	view := CPUDetailView{Parts: []CPUDetailPart{}, RefreshSeconds: 30, Status: "collecting"}
	if err := s.RequireAccess(ctx, member, network, false); err != nil {
		return view, err
	}
	var raw []byte
	var current bool
	err := s.pool.QueryRow(ctx, `SELECT q.session_id,q.cpu_id,q.cpu,q.session_id=n.current_session FROM cpu_detail_demands q JOIN networks n ON n.id=q.network_id WHERE q.network_id=$1 AND q.session_id=$2 AND q.cpu_id=$3`, network, session, cpu).Scan(&view.SessionID, &view.CPUID, &raw, &current)
	if err != nil {
		return view, notFound(err)
	}
	if err = json.Unmarshal(raw, &view.CPU); err != nil {
		return view, err
	}
	if !current {
		view.Status = "stale"
	}
	rows, err := s.pool.Query(ctx, `SELECT kind,items,total,truncated,error,observed_at,reported_at FROM cpu_detail_snapshots WHERE network_id=$1 AND session_id=$2 AND cpu_id=$3 ORDER BY kind`, network, session, cpu)
	if err != nil {
		return view, err
	}
	defer rows.Close()
	for rows.Next() {
		var part CPUDetailPart
		var encoded []byte
		if err = rows.Scan(&part.Kind, &encoded, &part.Total, &part.Truncated, &part.Error, &part.ObservedAt, &part.ReportedAt); err != nil {
			return view, err
		}
		if err = json.Unmarshal(encoded, &part.Items); err != nil {
			return view, err
		}
		view.Parts = append(view.Parts, part)
	}
	if err = rows.Err(); err != nil {
		return view, err
	}
	if current && len(view.Parts) == 3 {
		view.Status = "ready"
	}
	for _, part := range view.Parts {
		if part.Error == "target_changed" || part.Error == "target_expired" {
			view.Status = "target_changed"
			break
		}
	}
	return view, nil
}
func (s *Store) cpuDetailAssignment(ctx context.Context, tx pgx.Tx, d Device, session string) (*protocol.CPUDetailAssignment, error) {
	var cpu string
	var category int
	err := tx.QueryRow(ctx, `SELECT q.cpu_id,q.category FROM cpu_detail_demands q WHERE q.network_id=$1 AND q.session_id=$2 AND q.next_read_at<=now() AND q.assignment_expires<=now() AND EXISTS(SELECT 1 FROM cpu_detail_viewers v JOIN members m ON m.id=v.member_id WHERE v.network_id=q.network_id AND v.expires_at>now() AND NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM network_grants g WHERE g.network_id=v.network_id AND g.member_id=m.id))) FOR UPDATE OF q`, d.NetworkID, session).Scan(&cpu, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	assignment := &protocol.CPUDetailAssignment{AssignmentID: newID(), CPUID: cpu, Kind: []string{"active", "pending", "stored"}[category%3], TTLMillis: 8000}
	_, err = tx.Exec(ctx, `UPDATE cpu_detail_demands SET assignment_id=$2,assignment_kind=$3,assignment_expires=now()+interval '8 seconds',next_read_at=now()+interval '10 seconds' WHERE network_id=$1`, d.NetworkID, assignment.AssignmentID, assignment.Kind)
	return assignment, err
}
func (s *Store) acceptCPUDetail(ctx context.Context, tx pgx.Tx, d Device, session string, r *protocol.CPUDetailResult) error {
	if r == nil {
		return nil
	}
	if err := r.Validate(); err != nil {
		return errors.Join(ErrInvalid, err)
	}
	var matches bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cpu_detail_demands q WHERE q.network_id=$1 AND q.session_id=$2 AND q.cpu_id=$3 AND q.assignment_id=$4 AND q.assignment_kind=$5 AND q.last_result<>$4 AND q.assignment_expires>now() AND EXISTS(SELECT 1 FROM cpu_detail_viewers v JOIN members m ON m.id=v.member_id WHERE v.network_id=q.network_id AND v.expires_at>now() AND NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM network_grants g WHERE g.network_id=v.network_id AND g.member_id=m.id))))`, d.NetworkID, session, r.CPUID, r.AssignmentID, r.Kind).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return nil
	}
	encoded, _ := json.Marshal(r.Items)
	if r.Items == nil {
		encoded = []byte("[]")
	}
	if r.Error != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO cpu_detail_snapshots(network_id,session_id,cpu_id,kind,error) VALUES($1,$2,$3,$4,$5) ON CONFLICT(network_id,kind) DO UPDATE SET error=excluded.error,reported_at=now()`, d.NetworkID, session, r.CPUID, r.Kind, r.Error); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `INSERT INTO cpu_detail_snapshots(network_id,session_id,cpu_id,kind,items,total,truncated,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()) ON CONFLICT(network_id,kind) DO UPDATE SET items=excluded.items,total=excluded.total,truncated=excluded.truncated,error='',observed_at=now(),reported_at=now()`, d.NetworkID, session, r.CPUID, r.Kind, encoded, r.Total, r.Truncated); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE cpu_detail_demands SET last_result=$2,assignment_expires='-infinity',category=(category+1)%3,next_read_at=CASE WHEN $3 IN ('target_changed','target_expired') THEN 'infinity'::timestamptz ELSE next_read_at END,updated_at=now() WHERE network_id=$1`, d.NetworkID, r.AssignmentID, r.Error)
	return err
}
