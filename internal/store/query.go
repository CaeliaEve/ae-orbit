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
	"time"
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
	if q.Grouped {
		return s.GroupedItems(ctx, userID, networkID, q)
	}
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
	if q.Kind != "" && q.Kind != "item" && q.Kind != "fluid" && q.Kind != "essentia" {
		return page, ErrInvalid
	}
	if q.Consistency == "" {
		q.Consistency = "strict"
	}
	if q.Limit < 1 || q.Limit > 200 || len(q.Search) > 128 || !utf8.ValidString(q.Search) || (q.Sort != "name" && q.Sort != "quantity") {
		return page, ErrInvalid
	}
	if (q.Consistency != "strict" && q.Consistency != "live" && q.Consistency != "window") || (q.Consistency == "live" && q.Sort != "name") || (q.Anchor != "" && (q.Consistency != "window" || q.Cursor != "")) {
		return page, ErrInvalid
	}
	if (q.Pagination != "" && q.Pagination != "pages") || (q.Pagination == "pages" && q.Consistency != "window") || q.Page < 0 || ((q.Page > 0 || q.LastPage) && (q.Pagination != "pages" || q.Anchor != "" || q.Cursor != "")) {
		return page, ErrInvalid
	}
	if q.PageSize < 0 || q.PageSize > q.Limit || (q.PageSize > 0 && (q.Pagination != "pages" || q.Anchor != "" || q.Cursor != "")) {
		return page, ErrInvalid
	}
	pageSize := q.Limit
	if q.PageSize > 0 {
		pageSize = q.PageSize
		page.PageSize = pageSize
	}
	var cursor pageCursor
	var err error
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
	var enabledAt *time.Time
	if err = tx.QueryRow(ctx, "SELECT (SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1)", networkID).Scan(&enabledAt); err != nil {
		return page, err
	}
	plain := enabledAt != nil
	if plain && q.Kind == "" {
		q.Kind = "item"
	}
	resource := q.Kind == "fluid" || q.Kind == "essentia"
	if q.View == "" {
		if plain {
			q.View = "stock"
		} else {
			q.View = "observed"
		}
	}
	if q.View != "stock" && q.View != "all" && q.View != "observed" || (!plain && q.View == "all") {
		return page, ErrInvalid
	}
	if plain {
		page.Scope = "nesql_plain"
	}
	// Window browsing keeps its keyset boundary across membership/quantity changes.
	// Existing snapshot clients retain their revision checks.
	if q.View == "stock" && q.Consistency != "window" {
		q.Consistency = "strict"
	}
	queryBytes, _ := json.Marshal([]any{networkID, q.Search, q.Sort, q.Craftable, q.Consistency, q.View, enabledAt, q.Kind})
	fingerprint := sha256.Sum256(queryBytes)
	queryKey := hex.EncodeToString(fingerprint[:])
	boundary := q.Cursor
	if q.Anchor != "" {
		boundary = q.Anchor
	}
	if boundary != "" {
		cursor, err = s.decodeCursor(boundary)
		if err != nil || cursor.Query != queryKey {
			return page, ErrInvalid
		}
	}
	if q.Cursor != "" {
		if q.Consistency == "strict" && cursor.Revision != page.Revision {
			return page, ErrViewChanged
		}
		if q.Consistency == "live" && ((!plain && cursor.CatalogRevision != catalogRevision) || (q.Craftable != nil && cursor.CraftableRevision != craftableRevision)) {
			return page, ErrViewChanged
		}
	}
	args := []any{networkID, session}
	conditions := []string{"d.network_id=$1"}
	if q.Kind != "" {
		args = append(args, q.Kind)
		conditions = append(conditions, fmt.Sprintf("d.kind=$%d", len(args)))
	}
	label := "d.label"
	indexed := "d.search_label"
	join := "JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id"
	if plain {
		label = "c.label"
		indexed = "c.search_text"
		candidates := "item_catalog_candidates"
		if resource {
			candidates = "resource_catalog_candidates"
		}
		join = "JOIN " + candidates + " c ON c.network_id=d.network_id AND c.item_id=d.id LEFT JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id AND i.observed_at>="
		args = append(args, enabledAt)
		join += fmt.Sprintf("$%d", len(args))
		conditions = append(conditions, "d.exact")
		if resource {
			conditions = append(conditions, "d.nbt=''")
		}
		if q.View != "all" {
			conditions = append(conditions, "i.item_id IS NOT NULL")
		}
	}
	if q.View == "stock" {
		conditions = append(conditions, "i.quantity>0")
	}
	param := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	search, err := searchConditions(q.Search, label, indexed, param)
	if err != nil {
		return page, err
	}
	conditions = append(conditions, search...)
	if q.Craftable != nil {
		conditions = append(conditions, "i.craftable="+param(*q.Craftable))
	}
	order := "lower(" + label + "),d.id"
	if q.Sort == "quantity" {
		order = "coalesce(i.quantity,0) DESC,d.id"
	}
	if q.Anchor != "" {
		// Follow the first visible item when its quantity/rank changes. If it
		// leaves this view, use the old boundary to fill the vacated window.
		anchorArgs := append(append([]any{}, args...), int64(cursor.ID))
		err = tx.QueryRow(ctx, "SELECT lower("+label+"),coalesce(i.quantity,0) FROM item_definitions d "+join+" WHERE $2::text IS NOT NULL AND "+strings.Join(conditions, " AND ")+fmt.Sprintf(" AND d.id=$%d", len(anchorArgs)), anchorArgs...).Scan(&cursor.Name, &cursor.Quantity)
		if err != nil && err != pgx.ErrNoRows {
			return page, err
		}
	}
	var offset int64
	if q.Pagination == "pages" {
		// Count and read within the same snapshot. Page jumps only read server
		// observations and never request a game-side inventory enumeration.
		countArgs := append([]any{}, args...)
		countParam := func(value any) string {
			countArgs = append(countArgs, value)
			return fmt.Sprintf("$%d", len(countArgs))
		}
		rank := "false"
		if boundary != "" {
			comparison := "<"
			if q.Anchor == "" {
				comparison = "<="
			}
			if q.Sort == "quantity" {
				count, id := countParam(int64(cursor.Quantity)), countParam(int64(cursor.ID))
				rank = "(coalesce(i.quantity,0)>" + count + " OR (coalesce(i.quantity,0)=" + count + " AND d.id" + comparison + id + "))"
			} else {
				rank = "(lower(" + label + "),d.id)" + comparison + "(" + countParam(cursor.Name) + "," + countParam(int64(cursor.ID)) + ")"
			}
		}
		var total, before int64
		err = tx.QueryRow(ctx, "SELECT count(*),count(*) FILTER (WHERE "+rank+") FROM item_definitions d "+join+" WHERE $2::text IS NOT NULL AND "+strings.Join(conditions, " AND "), countArgs...).Scan(&total, &before)
		if err != nil {
			return page, err
		}
		value := protocol.Quantity(total)
		page.Total = &value
		page.PageCount = int((total + int64(pageSize) - 1) / int64(pageSize))
		if page.PageCount < 1 {
			page.PageCount = 1
		}
		page.PageNumber = int(before/int64(pageSize)) + 1
		if q.Page > 0 {
			page.PageNumber = q.Page
		}
		if q.LastPage {
			page.PageNumber = page.PageCount
		}
		if page.PageNumber > page.PageCount {
			page.PageNumber = page.PageCount
		}
		if q.Page > 0 || q.LastPage {
			offset = int64(page.PageNumber-1) * int64(pageSize)
		}
	}
	if boundary != "" {
		comparison := ">"
		if q.Anchor != "" {
			comparison = ">="
		}
		if q.Sort == "quantity" {
			count := param(int64(cursor.Quantity))
			id := param(int64(cursor.ID))
			conditions = append(conditions, "(coalesce(i.quantity,0)<"+count+" OR (coalesce(i.quantity,0)="+count+" AND d.id"+comparison+id+"))")
		} else {
			conditions = append(conditions, "(lower("+label+"),d.id)"+comparison+"("+param(cursor.Name)+","+param(int64(cursor.ID))+")")
		}
	}
	limit := param(q.Limit + 1)
	offsetSQL := ""
	if offset > 0 {
		offsetSQL = " OFFSET " + param(offset)
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,`+label+`,d.exact,d.nbt,coalesce(i.quantity,0),coalesce(i.craftable,false),coalesce(i.observed_at,'epoch'::timestamptz),i.craftable_observed_at,`+quantityCurrentSessionSQL+`,`+recipeCurrentSessionSQL+`,lower(`+label+`),i.item_id IS NOT NULL
        FROM item_definitions d `+join+` WHERE `+strings.Join(conditions, " AND ")+" ORDER BY "+order+" LIMIT "+limit+offsetSQL, args...)
	if err != nil {
		return page, err
	}
	names := []string{}
	for rows.Next() {
		var item protocol.Item
		var name string
		var known bool
		if err = rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Damage, &item.Label, &item.Exact, &item.NBT, &item.Quantity, &item.Craftable, &item.ObservedAt, &item.CraftableObservedAt, &item.CurrentSession, &item.CraftableCurrentSession, &name, &known); err != nil {
			rows.Close()
			return page, err
		}
		if plain {
			item.QuantityKnown = &known
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
	if len(page.Items) > 0 && q.Consistency == "window" {
		first := page.Items[0]
		page.AnchorCursor = s.encodeCursor(pageCursor{Query: queryKey, Revision: page.Revision, ID: first.ID, Name: names[0], Quantity: first.Quantity, CatalogRevision: catalogRevision, CraftableRevision: craftableRevision})
		if q.PageSize > 0 {
			for index := 0; index < len(page.Items); index += pageSize {
				item := page.Items[index]
				page.PageAnchors = append(page.PageAnchors, s.encodeCursor(pageCursor{Query: queryKey, Revision: page.Revision, ID: item.ID, Name: names[index], Quantity: item.Quantity, CatalogRevision: catalogRevision, CraftableRevision: craftableRevision}))
			}
		}
	}
	return page, tx.Commit(ctx)
}

type InventoryStatistics struct {
	Variants          *VariantStatistics   `json:"variants,omitempty"`
	Resources         []ResourceStatistics `json:"resources,omitempty"`
	Scope             string               `json:"scope,omitempty"`
	Candidates        string               `json:"candidates,omitempty"`
	Checked           string               `json:"checked,omitempty"`
	Zero              string               `json:"zero,omitempty"`
	Pending           string               `json:"pending,omitempty"`
	OldestObservation *time.Time           `json:"oldest_observation,omitempty"`
	LastObservation   *time.Time           `json:"last_observation,omitempty"`
	Entries           string               `json:"entries"`
	Craftable         string               `json:"craftable"`
	TotalQuantity     string               `json:"total_quantity"`
}

func (s *Store) InventoryStats(ctx context.Context, userID, networkID string) (InventoryStatistics, error) {
	var result InventoryStatistics
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return result, err
	}
	var plain bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM network_inventory_profiles WHERE network_id=$1)", networkID).Scan(&plain); err != nil {
		return result, err
	}
	if plain {
		result.Scope = "nesql_plain"
		err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE i.quantity>0)::text,
			count(*) FILTER(WHERE i.quantity>0 AND i.craftable)::text,coalesce(sum(i.quantity),0)::text,
			count(*)::text,count(i.item_id)::text,count(*) FILTER(WHERE i.quantity=0)::text,
			count(*) FILTER(WHERE i.item_id IS NULL)::text,min(i.observed_at),max(i.observed_at)
			FROM plain_catalog_candidates c JOIN network_inventory_profiles p ON p.network_id=c.network_id
			LEFT JOIN inventory i ON i.network_id=c.network_id AND i.item_id=c.item_id AND i.observed_at>=p.enabled_at
			WHERE c.network_id=$1`, networkID).Scan(&result.Entries, &result.Craftable, &result.TotalQuantity,
			&result.Candidates, &result.Checked, &result.Zero, &result.Pending, &result.OldestObservation, &result.LastObservation)
		if err == nil {
			result.Resources, err = s.resourceStats(ctx, networkID)
		}
		if err == nil {
			result.Variants, err = s.variantStats(ctx, networkID)
		}
		return result, err
	}
	err := s.pool.QueryRow(ctx, `SELECT count(*)::text,count(*) FILTER(WHERE craftable)::text,coalesce(sum(quantity),0)::text FROM inventory WHERE network_id=$1`, networkID).Scan(&result.Entries, &result.Craftable, &result.TotalQuantity)
	return result, err
}
