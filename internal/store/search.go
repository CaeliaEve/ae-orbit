package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/mozillazg/go-pinyin"
)

func normalizeSearch(value string) string {
	return strings.NewReplacer("ü", "v", "u:", "v").Replace(strings.ToLower(value))
}

// Compact full pinyin and first-letter forms supplement the original label.
// ASCII in mixed labels stays searchable; use letters, not linguistic initials.
func searchLabel(label string) string {
	hasChinese := false
	for _, r := range label {
		if unicode.Is(unicode.Han, r) {
			hasChinese = true
			break
		}
	}
	// Plain ASCII already matches label/name directly. Avoid indexing duplicate
	// registry strings for every metadata variant in a large export.
	if !hasChinese {
		return " "
	}
	args := pinyin.NewArgs()
	args.Fallback = func(r rune, _ pinyin.Args) []string { return []string{string(r)} }
	parts := pinyin.LazyPinyin(label, args)
	var full, initials strings.Builder
	for _, part := range parts {
		for _, r := range normalizeSearch(part) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				full.WriteRune(r)
			}
		}
		if part != "" {
			r := []rune(normalizeSearch(part))[0]
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				initials.WriteRune(r)
			}
		}
	}
	return full.String() + " " + initials.String()
}

func searchConditions(value, label, indexed string, param func(any) string) ([]string, error) {
	terms := strings.Fields(normalizeSearch(value))
	if len(terms) > 16 {
		return nil, ErrInvalid
	}
	conditions := []string{}
	for _, term := range terms {
		if strings.HasPrefix(term, "@") {
			mod := strings.TrimPrefix(term, "@")
			if mod == "" {
				return nil, ErrInvalid
			}
			for _, r := range mod {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
					return nil, ErrInvalid
				}
			}
			conditions = append(conditions, "split_part(lower(d.name),':',1)="+param(mod))
			continue
		}
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
		p := param(pattern)
		conditions = append(conditions, "(lower("+label+") LIKE "+p+" OR lower(d.name) LIKE "+p+" OR "+indexed+" LIKE "+p+")")
	}
	return conditions, nil
}

// Backfill existing databases in bounded batches; never re-enable the inventory
// profile or reset its real observations. This also handles legacy labels.
func backfillSearch(ctx context.Context, tx pgx.Tx) error {
	for _, table := range []string{"item_definitions", "plain_catalog_candidates"} {
		column, id := "search_label", "id"
		if table == "plain_catalog_candidates" {
			column, id = "search_text", "item_id"
		}
		for {
			rows, err := tx.Query(ctx, "SELECT network_id,"+id+",label FROM "+table+" WHERE "+column+"='' LIMIT 1000")
			if err != nil {
				return err
			}
			type searchRow struct {
				Network string `json:"network"`
				ID      int64  `json:"id"`
				Search  string `json:"search"`
			}
			data := []searchRow{}
			for rows.Next() {
				var network, label string
				var item int64
				if err = rows.Scan(&network, &item, &label); err != nil {
					rows.Close()
					return err
				}
				data = append(data, searchRow{network, item, searchLabel(label)})
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
			if len(data) == 0 {
				break
			}
			encoded, err := json.Marshal(data)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "UPDATE "+table+" d SET "+column+"=x.search FROM jsonb_to_recordset($1::jsonb) AS x(network text,id bigint,search text) WHERE d.network_id=x.network AND d."+id+"=x.id", encoded); err != nil {
				return fmt.Errorf("backfill inventory search: %w", err)
			}
		}
	}
	return nil
}
