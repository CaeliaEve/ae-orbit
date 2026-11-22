package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type QQCPUTargetInput struct {
	RequestKey string `json:"request_key"`
	SessionID  string `json:"session_id"`
	CPUID      string `json:"cpu_id"`
}
type QQCPUTarget struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Resolve the exact clicked observation, never a current enumeration index.
func qqCPUObservation(ctx context.Context, tx pgx.Tx, network, session, cpu string) (*protocol.CPU, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT cpu FROM qq_cpu_candidates WHERE network_id=$1 AND session_id=$2 AND cpu_id=$3 AND observed_at>now()-interval '90 seconds'`, network, session, cpu).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		var snapshot []byte
		if err = tx.QueryRow(ctx, `SELECT cpus FROM cpu_snapshots WHERE network_id=$1 AND session_id=$2 AND observed_at>now()-interval '30 seconds'`, network, session).Scan(&snapshot); err != nil {
			return nil, notFound(err)
		}
		var cpus []protocol.CPU
		if err = json.Unmarshal(snapshot, &cpus); err != nil {
			return nil, err
		}
		for _, c := range cpus {
			if c.ID == cpu {
				return &c, nil
			}
		}
		return nil, ErrViewChanged
	}
	if err != nil {
		return nil, err
	}
	var target protocol.CPU
	if err = json.Unmarshal(raw, &target); err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Store) PrepareQQCPUTarget(ctx context.Context, member, network string, in QQCPUTargetInput) (QQCPUTarget, error) {
	var target QQCPUTarget
	if err := s.RequireAccess(ctx, member, network, true); err != nil {
		return target, err
	}
	if !protocol.ProductionJobKeyValid(in.RequestKey) || len(in.SessionID) < 1 || len(in.SessionID) > 128 || len(in.CPUID) < 1 || len(in.CPUID) > 128 {
		return target, ErrInvalid
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var current string
		var raw []byte
		var online bool
		if err := tx.QueryRow(ctx, `SELECT n.current_session,b.capabilities,coalesce(d.last_seen>now()-interval '30 seconds',false) AND NOT d.revoked AND NOT b.retired AND b.protocol=2 AND d.current_session=b.id FROM networks n JOIN bridge_sessions b ON b.id=n.current_session JOIN devices d ON d.id=b.device_id WHERE n.id=$1 FOR UPDATE OF n`, network).Scan(&current, &raw, &online); err != nil {
			return notFound(err)
		}
		if current != in.SessionID {
			return ErrViewChanged
		}
		var caps protocol.Capabilities
		if err := json.Unmarshal(raw, &caps); err != nil {
			return err
		}
		if !online || !caps.CPUNotifications {
			return ErrCapability
		}
		err := tx.QueryRow(ctx, `SELECT id,status FROM qq_cpu_targets WHERE network_id=$1 AND member_id=$2 AND request_key=$3 AND session_id=$4 AND cpu_id=$5 AND expires_at>now()`, network, member, in.RequestKey, current, in.CPUID).Scan(&target.ID, &target.Status)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM qq_cpu_targets t WHERE t.network_id=$1 AND t.expires_at<=now() AND NOT EXISTS(SELECT 1 FROM qq_reminders r WHERE r.cpu_target_id=t.id)`, network); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM qq_cpu_targets WHERE network_id=$1 AND session_id=$2 AND expires_at>now()`, network, current).Scan(&count); err != nil {
			return err
		}
		if count >= 16 {
			return ErrCapacity
		}
		cpu, err := qqCPUObservation(ctx, tx, network, current, in.CPUID)
		if err != nil {
			return err
		}
		if !cpu.Busy {
			return ErrViewChanged
		}
		data, _ := json.Marshal(cpu)
		target = QQCPUTarget{ID: newID(), Status: "pending"}
		_, err = tx.Exec(ctx, `INSERT INTO qq_cpu_targets(id,network_id,member_id,request_key,session_id,cpu_id,cpu) VALUES($1,$2,$3,$4,$5,$6,$7)`, target.ID, network, member, in.RequestKey, current, in.CPUID, data)
		return err
	})
	return target, err
}

func (s *Store) QQCPUTarget(ctx context.Context, member, network, id string) (QQCPUTarget, error) {
	var target QQCPUTarget
	if err := s.RequireAccess(ctx, member, network, true); err != nil {
		return target, err
	}
	err := s.pool.QueryRow(ctx, `SELECT t.id,t.status FROM qq_cpu_targets t JOIN networks n ON n.id=t.network_id WHERE t.network_id=$1 AND t.member_id=$2 AND t.id=$3 AND t.session_id=n.current_session AND t.expires_at>now()`, network, member, id).Scan(&target.ID, &target.Status)
	return target, notFound(err)
}

func (s *Store) cpuWatchAssignments(ctx context.Context, tx pgx.Tx, network, session string) ([]protocol.CPUWatchAssignment, error) {
	rows, err := tx.Query(ctx, `SELECT t.id,t.cpu_id FROM qq_cpu_targets t JOIN members m ON m.id=t.member_id WHERE t.network_id=$1 AND t.session_id=$2 AND t.expires_at>now() AND t.status IN ('pending','busy') AND t.next_read_at<=now() AND NOT m.revoked AND (m.is_admin OR EXISTS(SELECT 1 FROM network_grants g WHERE g.network_id=t.network_id AND g.member_id=t.member_id)) ORDER BY t.next_read_at,t.id LIMIT 4 FOR UPDATE OF t`, network, session)
	if err != nil {
		return nil, err
	}
	var assignments []protocol.CPUWatchAssignment
	for rows.Next() {
		var a protocol.CPUWatchAssignment
		if err = rows.Scan(&a.TargetID, &a.CPUID); err != nil {
			rows.Close()
			return nil, err
		}
		a.TTLMillis = 120000
		assignments = append(assignments, a)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, a := range assignments {
		if _, err = tx.Exec(ctx, `UPDATE qq_cpu_targets SET next_read_at=now()+interval '10 seconds' WHERE id=$1`, a.TargetID); err != nil {
			return nil, err
		}
	}
	return assignments, nil
}

func acceptQQCPUWatches(ctx context.Context, tx pgx.Tx, network, session string, results []protocol.CPUWatchResult) error {
	// New boots invalidate old native references even when no roster changed.
	if _, err := tx.Exec(ctx, `UPDATE qq_cpu_targets SET status='missing' WHERE network_id=$1 AND session_id<>$2 AND status IN ('pending','busy')`, network, session); err != nil {
		return err
	}
	for _, r := range results {
		if _, err := tx.Exec(ctx, `UPDATE qq_cpu_targets SET status=$4,observed_at=now(),expires_at=CASE WHEN EXISTS(SELECT 1 FROM qq_reminders q WHERE q.cpu_target_id=qq_cpu_targets.id AND q.state='watching') THEN now()+interval '2 minutes' ELSE expires_at END WHERE network_id=$1 AND session_id=$2 AND id=$3 AND expires_at>now() AND status IN ('pending','busy')`, network, session, r.TargetID, r.Status); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT r.id,t.status,t.session_id,t.expires_at FROM qq_reminders r JOIN qq_cpu_targets t ON t.id=r.cpu_target_id WHERE r.network_id=$1 AND r.state='watching' AND (t.status NOT IN ('pending','busy') OR t.session_id<>$2 OR t.expires_at<=now()) FOR UPDATE OF r`, network, session)
	if err != nil {
		return err
	}
	type ended struct {
		id, status, session string
		expires             time.Time
	}
	var ends []ended
	for rows.Next() {
		var e ended
		if err = rows.Scan(&e.id, &e.status, &e.session, &e.expires); err != nil {
			rows.Close()
			return err
		}
		ends = append(ends, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, e := range ends {
		reason := "CPU 引用不可用，无法确认原任务结果"
		if e.session != session {
			reason = "OC 已重连，无法确认原任务结果"
		} else if time.Now().After(e.expires) {
			reason = "CPU 跟踪观测中断，无法确认原任务结果"
		} else if e.status == "idle" {
			reason = "CPU 已空闲；游戏内任务无法区分成功与取消"
		} else if e.status == "changed" {
			reason = "CPU 产物已变化，无法确认原任务结果"
		} else if e.status == "inactive" {
			reason = "CPU 已断开或失效，无法确认原任务结果"
		}
		if err = finishQQCPUWatch(ctx, tx, e.id, reason); err != nil {
			return err
		}
	}
	return nil
}

func finishQQCPUWatch(ctx context.Context, tx pgx.Tx, id, reason string) error {
	if _, err := tx.Exec(ctx, `SELECT qq_enqueue(n.server_group,r.network_id,r.member_id,'reminder:'||r.id||':end','cpu_end',r.group_id,r.recipient,r.private_copy,r.payload||jsonb_build_object('state','unconfirmed','reason',$2::text,'at',now())) FROM qq_reminders r JOIN networks n ON n.id=r.network_id WHERE r.id=$1`, id, reason); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE qq_reminders SET state='uncertain',finished_at=now() WHERE id=$1`, id)
	return err
}

func addPinnedQQReminder(ctx context.Context, tx pgx.Tx, member, network string, in QQReminderInput) (string, error) {
	var cpuJSON []byte
	var state string
	err := tx.QueryRow(ctx, `SELECT t.cpu,t.status FROM qq_cpu_targets t JOIN networks n ON n.id=t.network_id JOIN devices d ON d.network_id=n.id AND d.role='primary' AND d.current_session=n.current_session WHERE t.id=$1 AND t.network_id=$2 AND t.member_id=$3 AND t.session_id=$4 AND t.cpu_id=$5 AND t.session_id=n.current_session AND t.expires_at>now() AND t.observed_at>now()-interval '30 seconds' AND d.last_seen>now()-interval '30 seconds' AND NOT d.revoked FOR UPDATE OF t`, in.TargetID, network, member, in.SessionID, in.CPUID).Scan(&cpuJSON, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrViewChanged
	}
	if err != nil {
		return "", err
	}
	if state != "busy" {
		return "", ErrViewChanged
	}
	var cpu protocol.CPU
	if err = json.Unmarshal(cpuJSON, &cpu); err != nil {
		return "", err
	}
	var previousID, targetID string
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT id,cpu_target_id,jsonb_build_object('group_id',group_id,'recipient',recipient,'private_copy',private_copy) FROM qq_reminders WHERE network_id=$1 AND member_id=$2 AND request_key=$3`, network, member, in.RequestKey).Scan(&previousID, &targetID, &previous)
	if err == nil {
		var n protocol.QQNotification
		if err = json.Unmarshal(previous, &n); err != nil {
			return "", err
		}
		if targetID != in.TargetID || n != in.Notification {
			return "", ErrConflict
		}
		return previousID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM qq_reminders WHERE network_id=$1 AND member_id=$2 AND state='watching'`, network, member).Scan(&count); err != nil {
		return "", err
	}
	if count >= 32 {
		return "", ErrCapacity
	}
	var id string
	payload := cpuQQPayload(cpu)
	if cpu.Name == "" {
		payload["cpu"] = "未命名 CPU · " + cpu.ID
	}
	encoded, _ := json.Marshal(payload)
	err = tx.QueryRow(ctx, `INSERT INTO qq_reminders(id,network_id,member_id,request_key,cpu_name,session_id,cpu_target_id,group_id,recipient,private_copy,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(cpu_target_id,member_id) WHERE cpu_target_id IS NOT NULL AND state='watching' DO UPDATE SET group_id=excluded.group_id,recipient=excluded.recipient,private_copy=excluded.private_copy RETURNING id`, newID(), network, member, in.RequestKey, cpu.Name, in.SessionID, in.TargetID, in.Notification.GroupID, in.Notification.Recipient, in.Notification.PrivateCopy, encoded).Scan(&id)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE qq_cpu_targets SET expires_at=now()+interval '2 minutes' WHERE id=$1`, in.TargetID)
	return id, err
}
