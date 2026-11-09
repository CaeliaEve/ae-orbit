package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type pageCursor struct {
	Query             string            `json:"query"`
	Revision          protocol.Quantity `json:"revision"`
	CatalogRevision   protocol.Quantity `json:"catalog_revision,omitempty"`
	CraftableRevision protocol.Quantity `json:"craftable_revision,omitempty"`
	ID                protocol.Quantity `json:"id"`
	Name              string            `json:"name"`
	Quantity          protocol.Quantity `json:"quantity"`
}

const quantityCurrentSessionSQL = `EXISTS(SELECT 1 FROM bridge_sessions source
	JOIN devices owner ON owner.id=source.device_id AND owner.current_session=source.id AND NOT owner.revoked
	JOIN networks source_network ON source_network.id=owner.network_id
	WHERE source.id=i.session_id AND source.network_id=i.network_id AND NOT source.retired
	AND source.protocol=CASE source_network.bridge_mode WHEN 'single' THEN 1 ELSE 2 END
	AND (source_network.bridge_mode<>'single' OR source.id=$2))`

const recipeCurrentSessionSQL = `EXISTS(SELECT 1 FROM bridge_sessions source
	JOIN devices owner ON owner.id=source.device_id AND owner.current_session=source.id AND NOT owner.revoked
	JOIN networks source_network ON source_network.id=owner.network_id
	WHERE source.id=i.craftable_session_id AND source.network_id=i.network_id AND NOT source.retired
	AND source.protocol=CASE source_network.bridge_mode WHEN 'single' THEN 1 ELSE 2 END
	AND owner.role='primary' AND (source_network.bridge_mode<>'single' OR source.id=$2))`

func (s *Store) encodeCursor(c pageCursor) string {
	data, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Store) decodeCursor(value string) (pageCursor, error) {
	var c pageCursor
	if len(value) > 4096 {
		return c, ErrInvalid
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return c, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, ErrInvalid
	}
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return c, ErrInvalid
	}
	if err = json.Unmarshal(data, &c); err != nil || c.ID < 1 {
		return c, ErrInvalid
	}
	return c, nil
}

func (s *Store) Items(ctx context.Context, userID, networkID string, q protocol.ItemQuery) (protocol.ItemPage, error) {
	page := protocol.ItemPage{Items: []protocol.Item{}}
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return page, err
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Sort == "" {
		q.Sort = "name"
	}
	if q.Consistency == "" {
		q.Consistency = "strict"
	}
	if q.Limit < 1 || q.Limit > 200 || len(q.Search) > 128 || !utf8.ValidString(q.Search) || (q.Sort != "name" && q.Sort != "quantity") {
		return page, ErrInvalid
	}
	if (q.Consistency != "strict" && q.Consistency != "live") || (q.Consistency == "live" && q.Sort != "name") {
		return page, ErrInvalid
	}
	queryBytes, _ := json.Marshal([]any{networkID, q.Search, q.Sort, q.Craftable, q.Consistency})
	fingerprint := sha256.Sum256(queryBytes)
	queryKey := hex.EncodeToString(fingerprint[:])
	var cursor pageCursor
	var err error
	if q.Cursor != "" {
		cursor, err = s.decodeCursor(q.Cursor)
		if err != nil || cursor.Query != queryKey {
			return page, ErrInvalid
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	var session string
	var catalogRevision, craftableRevision protocol.Quantity
	if err = tx.QueryRow(ctx, "SELECT revision,current_session,catalog_revision,craftable_revision FROM networks WHERE id=$1", networkID).Scan(&page.Revision, &session, &catalogRevision, &craftableRevision); err != nil {
		return page, notFound(err)
	}
	if q.Cursor != "" {
		if q.Consistency == "strict" && cursor.Revision != page.Revision {
			return page, ErrViewChanged
		}
		if q.Consistency == "live" && (cursor.CatalogRevision != catalogRevision || (q.Craftable != nil && cursor.CraftableRevision != craftableRevision)) {
			return page, ErrViewChanged
		}
	}
	args := []any{networkID, session}
	conditions := []string{"d.network_id=$1"}
	param := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	if q.Search != "" {
		value := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q.Search)
		if utf8.RuneCountInString(q.Search) < 3 {
			p := param(value + "%")
			conditions = append(conditions, "(lower(d.label) LIKE lower("+p+") OR lower(d.name) LIKE lower("+p+"))")
		} else {
			p := param("%" + value + "%")
			conditions = append(conditions, "lower(d.label || ' ' || d.name) LIKE lower("+p+")")
		}
	}
	if q.Craftable != nil {
		conditions = append(conditions, "i.craftable="+param(*q.Craftable))
	}
	order := "lower(d.label),d.id"
	if q.Sort == "quantity" {
		order = "i.quantity DESC,d.id"
	}
	if q.Cursor != "" {
		if q.Sort == "quantity" {
			count := param(int64(cursor.Quantity))
			id := param(int64(cursor.ID))
			conditions = append(conditions, "(i.quantity<"+count+" OR (i.quantity="+count+" AND d.id>"+id+"))")
		} else {
			conditions = append(conditions, "(lower(d.label),d.id)>("+param(cursor.Name)+","+param(int64(cursor.ID))+")")
		}
	}
	limit := param(q.Limit + 1)
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.label,d.exact,i.quantity,i.craftable,i.observed_at,i.craftable_observed_at,`+quantityCurrentSessionSQL+`,`+recipeCurrentSessionSQL+`,lower(d.label)
        FROM item_definitions d JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id WHERE `+strings.Join(conditions, " AND ")+" ORDER BY "+order+" LIMIT "+limit, args...)
	if err != nil {
		return page, err
	}
	names := []string{}
	for rows.Next() {
		var item protocol.Item
		var name string
		if err = rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Damage, &item.Label, &item.Exact, &item.Quantity, &item.Craftable, &item.ObservedAt, &item.CraftableObservedAt, &item.CurrentSession, &item.CraftableCurrentSession, &name); err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, item)
		names = append(names, name)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		next := pageCursor{Query: queryKey, Revision: page.Revision, ID: last.ID, Name: names[q.Limit-1], Quantity: last.Quantity}
		if q.Consistency == "live" {
			next.CatalogRevision, next.CraftableRevision = catalogRevision, craftableRevision
		}
		page.NextCursor = s.encodeCursor(next)
	}
	return page, tx.Commit(ctx)
}

type InventoryStatistics struct {
	Entries       string `json:"entries"`
	Craftable     string `json:"craftable"`
	TotalQuantity string `json:"total_quantity"`
}

func (s *Store) InventoryStats(ctx context.Context, userID, networkID string) (InventoryStatistics, error) {
	var result InventoryStatistics
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return result, err
	}
	err := s.pool.QueryRow(ctx, `SELECT count(*)::text,count(*) FILTER(WHERE craftable)::text,coalesce(sum(quantity),0)::text FROM inventory WHERE network_id=$1`, networkID).Scan(&result.Entries, &result.Craftable, &result.TotalQuantity)
	return result, err
}
