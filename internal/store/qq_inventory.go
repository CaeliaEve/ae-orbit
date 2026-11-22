package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type SubnetKeywordsInput struct {
	Keywords []string `json:"keywords"`
}
type QQInventoryRequest struct {
	GroupID    string `json:"group_id"`
	RequestKey string `json:"request_key"`
	Keyword    string `json:"keyword"`
}
type QQInventoryItem struct {
	SubnetID string             `json:"subnet_id"`
	Subnet   string             `json:"subnet"`
	ItemID   string             `json:"item_id"`
	Label    string             `json:"label"`
	Kind     string             `json:"kind"`
	NBT      bool               `json:"nbt"`
	Quantity *protocol.Quantity `json:"quantity"`
	Observed *time.Time         `json:"observed_at"`
	Known    bool               `json:"known"`
}
type QQInventoryView struct {
	Keyword        string            `json:"keyword"`
	Items          []QQInventoryItem `json:"items"`
	Total          int               `json:"total"`
	SendingEnabled bool              `json:"sending_enabled"`
}

func normalizeSubnetKeyword(raw string) (string, error) {
	if !utf8.ValidString(raw) || len(raw) > 256 {
		return "", ErrInvalid
	}
	key := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	if utf8.RuneCountInString(key) < 1 || utf8.RuneCountInString(key) > 32 {
		return "", ErrInvalid
	}
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != ' ' && r != '-' && r != '_' {
			return "", ErrInvalid
		}
	}
	// Leave the existing power commands available even when a keyword is set.
	if key == "电量" || key == "蓝波顿" || key == "蓝博顿" || key == "ae电量" || key == "库存" {
		return "", ErrInvalid
	}
	return key, nil
}
func (s *Store) SetSubnetKeywords(ctx context.Context, member, group, subnet, item string, in SubnetKeywordsInput) error {
	if err := s.RequireAdmin(ctx, member); err != nil {
		return err
	}
	if err := s.RequireGroupAccess(ctx, member, group); err != nil {
		return err
	}
	if !monitorID.MatchString(item) || len(in.Keywords) > 8 {
		return ErrInvalid
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, raw := range in.Keywords {
		key, err := normalizeSubnetKeyword(raw)
		if err != nil {
			return err
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var exists bool
		// Serialize replacements and item removal on the same subnet row.
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subnet_items i WHERE i.subnet_id=n.id AND i.item_id=$3 AND i.enabled) FROM monitored_subnets n WHERE n.id=$1 AND n.server_group=$2 FOR UPDATE`, subnet, group, item).Scan(&exists); err != nil {
			return notFound(err)
		}
		if !exists {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM subnet_qq_keywords WHERE subnet_id=$1 AND item_id=$2`, subnet, item); err != nil {
			return err
		}
		for _, key := range keys {
			if _, err := tx.Exec(ctx, `INSERT INTO subnet_qq_keywords(server_group,subnet_id,item_id,keyword,created_by) VALUES($1,$2,$3,$4,$5)`, group, subnet, item, key, member); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) QQInventoryKeywords(ctx context.Context, a QQAuth) ([]string, error) {
	keys := []string{}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT k.keyword FROM subnet_qq_keywords k JOIN subnet_items i ON i.subnet_id=k.subnet_id AND i.item_id=k.item_id JOIN subnet_plugins p USING(server_group) JOIN qq_connectors c USING(server_group) WHERE k.server_group=$1 AND i.enabled AND p.enabled AND c.enabled AND c.catalog_ready ORDER BY k.keyword`, a.Group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
func qqInventoryView(ctx context.Context, tx pgx.Tx, a QQAuth, gid, key string) (QQInventoryView, error) {
	v := QQInventoryView{Keyword: key, Items: []QQInventoryItem{}}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT c.sending_enabled,c.enabled AND c.catalog_ready AND g.enabled FROM qq_connectors c JOIN qq_groups g ON g.server_group=c.server_group WHERE c.server_group=$1 AND g.group_id=$2`, a.Group, gid).Scan(&v.SendingEnabled, &allowed); err != nil {
		return v, notFound(err)
	}
	if !allowed {
		return v, ErrForbidden
	}
	rows, err := tx.Query(ctx, `SELECT n.id,n.name,d.id,d.label,d.kind,d.nbt<>'',l.quantity,l.observed_at,coalesce(l.quantity IS NOT NULL AND l.error='' AND l.observed_at>now()-n.stale_seconds*interval '1 second' AND l.revision=n.revision AND l.session_id=c.current_session AND c.last_seen>now()-n.stale_seconds*interval '1 second' AND NOT c.revoked AND n.enabled AND p.enabled,false),count(*) OVER() FROM subnet_qq_keywords k JOIN subnet_items i ON i.subnet_id=k.subnet_id AND i.item_id=k.item_id JOIN subnet_catalog d ON d.id=i.item_id JOIN monitored_subnets n ON n.id=i.subnet_id AND n.server_group=k.server_group JOIN subnet_plugins p ON p.server_group=n.server_group JOIN monitor_collectors c ON c.id=n.collector_id LEFT JOIN subnet_latest l ON l.subnet_id=i.subnet_id AND l.item_id=i.item_id WHERE k.server_group=$1 AND k.keyword=$2 AND i.enabled AND p.enabled ORDER BY n.name,n.id,d.label,d.id LIMIT 20`, a.Group, key)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var i QQInventoryItem
		if err = rows.Scan(&i.SubnetID, &i.Subnet, &i.ItemID, &i.Label, &i.Kind, &i.NBT, &i.Quantity, &i.Observed, &i.Known, &v.Total); err != nil {
			return v, err
		}
		v.Items = append(v.Items, i)
	}
	return v, rows.Err()
}
func (s *Store) QQInventory(ctx context.Context, a QQAuth, gid, keyword string) (QQInventoryView, error) {
	var v QQInventoryView
	key, err := normalizeSubnetKeyword(keyword)
	if err != nil || !protocol.QQNumberValid(gid) {
		return v, ErrInvalid
	}
	err = s.monitorTransaction(ctx, func(tx pgx.Tx) error { var e error; v, e = qqInventoryView(ctx, tx, a, gid, key); return e })
	return v, err
}
func (s *Store) QQRequestInventory(ctx context.Context, a QQAuth, in QQInventoryRequest) (bool, error) {
	queued := false
	key, err := normalizeSubnetKeyword(in.Keyword)
	if err != nil || !protocol.QQNumberValid(in.GroupID) || !protocol.ProductionJobKeyValid(in.RequestKey) {
		return false, ErrInvalid
	}
	err = s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT group_id FROM qq_groups WHERE server_group=$1 AND group_id=$2 FOR UPDATE`, a.Group, in.GroupID); err != nil {
			return err
		}
		v, err := qqInventoryView(ctx, tx, a, in.GroupID, key)
		if err != nil {
			return err
		}
		if len(v.Items) == 0 {
			return nil
		}
		payload, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT qq_enqueue($1,NULL,NULL,$2,'inventory_query',$3,'',false,$4)`, a.Group, "inventory:"+in.RequestKey, in.GroupID, payload)
		queued = err == nil
		return err
	})
	return queued, err
}
