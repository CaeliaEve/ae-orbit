package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
	"strings"
	"unicode/utf8"
)

// Group only persisted, authorized observations. Groups are presentation rows;
// requests/orders always retain an individual canonical item id.
func (s *Store) GroupedItems(ctx context.Context, user, network string, q protocol.ItemQuery) (protocol.ItemPage, error) {
	result := protocol.ItemPage{Items: []protocol.Item{}, Scope: "nesql_plain"}
	if err := s.RequireAccess(ctx, user, network, false); err != nil {
		return result, err
	}
	if q.Kind != "" && q.Kind != "item" || q.Limit < 0 || q.Limit > 200 || len(q.Search) > 128 || !utf8.ValidString(q.Search) || q.Page < 0 || q.Cursor != "" || q.PageSize < 0 || q.PageSize > q.Limit {
		return result, ErrInvalid
	}
	if q.Sort == "" {
		q.Sort = "name"
	}
	if q.Sort != "name" && q.Sort != "quantity" {
		return result, ErrInvalid
	}
	if q.View == "" {
		q.View = "stock"
	}
	if q.View != "stock" && q.View != "all" {
		return result, ErrInvalid
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	size := q.Limit
	if q.PageSize > 0 {
		size = q.PageSize
	}
	if size < 1 {
		return result, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	// A catalogue page is small. Parallel hash workers amplify shared-memory
	// use on the production database; this budget is local to the read-only tx.
	if _, err = tx.Exec(ctx, "SET LOCAL max_parallel_workers_per_gather=0; SET LOCAL work_mem='8MB'"); err != nil {
		return result, err
	}
	var session string
	if err = tx.QueryRow(ctx, "SELECT revision,current_session FROM networks WHERE id=$1", network).Scan(&result.Revision, &session); err != nil {
		return result, notFound(err)
	}
	var enabled bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM network_inventory_profiles WHERE network_id=$1)", network).Scan(&enabled); err != nil {
		return result, err
	}
	if !enabled {
		q.Grouped = false
		q.View = ""
		return s.Items(ctx, user, network, q)
	}
	data, _ := json.Marshal([]any{network, q.Search, q.Sort, q.Craftable, q.View, "grouped-v1"})
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
	var anchor pageCursor
	if q.Anchor != "" {
		anchor, err = s.decodeCursor(q.Anchor)
		if err != nil || anchor.Query != key {
			return result, ErrInvalid
		}
	}
	args := []any{network, session}
	param := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	conditions := []string{"true"}
	search, err := searchConditions(q.Search, "label", "search_text", param)
	if err != nil {
		return result, err
	}
	conditions = append(conditions, search...)
	if q.Craftable != nil {
		conditions = append(conditions, "craftable="+param(*q.Craftable))
	}
	matchWhere := strings.Join(conditions, " AND ")
	// Only bases with observed stock can appear in the stock view. Retain all
	// their siblings so candidate counts and search still describe the whole group.
	stockCTE, stockJoin := "", ""
	if q.View == "stock" {
		stockCTE = `stock_bases AS MATERIALIZED (
 SELECT DISTINCT d.name,d.damage FROM inventory i
 JOIN item_definitions d ON d.network_id=i.network_id AND d.id=i.item_id
 JOIN network_inventory_profiles p ON p.network_id=i.network_id
 WHERE i.network_id=$1 AND i.quantity>0 AND i.observed_at>=p.enabled_at AND d.kind='item'),`
		stockJoin = " JOIN stock_bases b ON b.name=d.name AND b.damage=d.damage "
	}
	cte := `WITH ` + stockCTE + `live_sources AS MATERIALIZED (
 SELECT source.id,owner.role FROM bridge_sessions source
 JOIN devices owner ON owner.id=source.device_id AND owner.current_session=source.id AND NOT owner.revoked
 JOIN networks n ON n.id=source.network_id
 WHERE source.network_id=$1 AND NOT source.retired
 AND source.protocol=CASE n.bridge_mode WHEN 'single' THEN 1 ELSE 2 END
 AND (n.bridge_mode<>'single' OR source.id=$2)),
 variants AS (
 SELECT d.id,d.name,d.damage,(d.nbt='') untagged,c.label,c.search_text,coalesce(i.quantity,0) quantity,i.item_id IS NOT NULL known,
 coalesce(i.craftable,false) craftable,i.observed_at,i.craftable_observed_at,
 qty.id IS NOT NULL current_session,recipe.id IS NOT NULL recipe_current
 FROM item_catalog_candidates c JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id ` + stockJoin + `
 JOIN network_inventory_profiles p ON p.network_id=c.network_id
 LEFT JOIN inventory i ON i.network_id=c.network_id AND i.item_id=c.item_id AND i.observed_at>=p.enabled_at
 LEFT JOIN live_sources qty ON qty.id=i.session_id
 LEFT JOIN live_sources recipe ON recipe.id=i.craftable_session_id AND recipe.role='primary'
 WHERE c.network_id=$1 AND d.kind='item' AND d.exact),
 grouped AS MATERIALIZED (SELECT min(id) id,name,damage,
 coalesce(min(label) FILTER(WHERE untagged),min(label)) label,count(*) candidates,count(*) FILTER(WHERE known) checked,
 count(*) FILTER(WHERE known AND quantity>0) stock,coalesce(sum(quantity) FILTER(WHERE known),0)::text quantity,
 coalesce(max(observed_at),'epoch'::timestamptz) observed_at,max(craftable_observed_at) recipe_at,
 bool_and(current_session) FILTER(WHERE known) current_session,
 bool_or(craftable) craftable,bool_and(recipe_current) FILTER(WHERE craftable) recipe_current
 FROM variants d GROUP BY name,damage HAVING bool_or(` + matchWhere + `))`
	where := "true"
	if q.View == "stock" {
		where = "stock>0"
	}
	order := "lower(label),id"
	if q.Sort == "quantity" {
		order = "quantity::numeric DESC,id"
	}
	// Count, anchor rank and selected page share the same materialized groups.
	// Previously every page aggregated the entire catalogue two or three times.
	sizeParam := param(int64(size))
	pageParam := param(int64(max(0, q.Page-1)))
	lastParam := param(q.LastPage)
	anchorParam := param(int64(anchor.ID))
	limitParam := param(q.Limit)
	statement := cte + `, ranked AS MATERIALIZED (
 SELECT *,row_number() OVER(ORDER BY ` + order + `)-1 position FROM grouped WHERE ` + where + `),
 summary AS (SELECT count(*) total FROM ranked),
 bounds AS (SELECT total,greatest(1,(total+` + sizeParam + `-1)/` + sizeParam + `) page_count,
 least(greatest(0,(total-1)/` + sizeParam + `),
 CASE WHEN ` + lastParam + ` THEN greatest(0,(total-1)/` + sizeParam + `)
 ELSE coalesce((SELECT position/` + sizeParam + ` FROM ranked WHERE id=` + anchorParam + `),` + pageParam + `) END) page
 FROM summary)
 SELECT bounds.total,bounds.page_count,bounds.page+1,
 coalesce(p.id,0),coalesce(p.name,''),coalesce(p.damage,0),coalesce(p.label,''),coalesce(p.candidates,0),coalesce(p.checked,0),
 coalesce(p.stock,0),coalesce(p.quantity,'0'),coalesce(p.observed_at,'epoch'::timestamptz),p.recipe_at,
 coalesce(p.current_session,false),coalesce(p.craftable,false),coalesce(p.recipe_current,false)
 FROM bounds LEFT JOIN LATERAL (
 SELECT * FROM ranked WHERE position>=bounds.page*` + sizeParam + ` ORDER BY position LIMIT ` + limitParam + `)p ON true
 ORDER BY p.position`
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		item := protocol.Item{Kind: "item", Exact: true, Group: &protocol.ItemGroup{}}
		var total protocol.Quantity
		if err = rows.Scan(&total, &result.PageCount, &result.PageNumber, &item.ID, &item.Name, &item.Damage, &item.Label, &item.Group.Candidates, &item.Group.Checked, &item.Group.Stock, &item.Group.Quantity, &item.ObservedAt, &item.CraftableObservedAt, &item.CurrentSession, &item.Craftable, &item.CraftableCurrentSession); err != nil {
			rows.Close()
			return result, err
		}
		result.Total = &total
		if item.ID == 0 {
			continue
		}
		known := item.Group.Checked > 0
		item.QuantityKnown = &known
		result.Items = append(result.Items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	for index, item := range result.Items {
		cursor := s.encodeCursor(pageCursor{Query: key, Revision: result.Revision, ID: item.ID})
		if index == 0 {
			result.AnchorCursor = cursor
		}
		if index%size == 0 {
			result.PageAnchors = append(result.PageAnchors, cursor)
		}
	}
	if result.PageNumber < result.PageCount {
		result.NextCursor = "group-page"
	}
	result.PageSize = size
	return result, tx.Commit(ctx)
}

func (s *Store) ItemVariants(ctx context.Context, user, network string, base protocol.Quantity, after protocol.Quantity, all bool) (protocol.ItemPage, error) {
	result := protocol.ItemPage{Items: []protocol.Item{}}
	if err := s.RequireAccess(ctx, user, network, false); err != nil {
		return result, err
	}
	if base < 1 || after < 0 {
		return result, ErrInvalid
	}
	var name string
	var damage int
	if err := s.pool.QueryRow(ctx, "SELECT name,damage FROM item_definitions d WHERE network_id=$1 AND id=$2 AND "+catalogScopeSQL, network, int64(base)).Scan(&name, &damage); err != nil {
		return result, notFound(err)
	}
	// Items already enforce catalogue scope; dedicated base constraints avoid
	// scanning unrelated variants and retain exact ids for selection.
	rows, err := s.pool.Query(ctx, `SELECT d.id FROM item_definitions d JOIN item_catalog_candidates c ON c.network_id=d.network_id AND c.item_id=d.id
 LEFT JOIN network_inventory_profiles p ON p.network_id=d.network_id LEFT JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id AND i.observed_at>=p.enabled_at
 WHERE d.network_id=$1 AND d.name=$2 AND d.damage=$3 AND d.id>$4 AND ($5 OR i.quantity>0) ORDER BY d.id LIMIT 101`, network, name, damage, int64(after), all)
	if err != nil {
		return result, err
	}
	ids := []protocol.Quantity{}
	for rows.Next() {
		var id protocol.Quantity
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(ids) > 100 {
		ids = ids[:100]
		result.NextCursor = fmt.Sprint(ids[99])
	}
	if len(ids) > 0 {
		lookup, err := s.ItemsByID(ctx, user, network, ids)
		if err != nil {
			return result, err
		}
		result.Items = lookup.Items
		result.Revision = lookup.Revision
	}
	return result, nil
}
