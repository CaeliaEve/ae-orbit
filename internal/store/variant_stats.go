package store

import "context"

type VariantStatistics struct {
	Candidates    string `json:"candidates"`
	Checked       string `json:"checked"`
	Entries       string `json:"entries"`
	TotalQuantity string `json:"total_quantity"`
}

func (s *Store) variantStats(ctx context.Context, network string) (*VariantStatistics, error) {
	result := &VariantStatistics{}
	err := s.pool.QueryRow(ctx, `SELECT count(*)::text,count(i.item_id)::text,count(*) FILTER(WHERE i.quantity>0)::text,coalesce(sum(i.quantity),0)::text
 FROM variant_catalog_candidates c JOIN network_inventory_profiles p ON p.network_id=c.network_id
 LEFT JOIN inventory i ON i.network_id=c.network_id AND i.item_id=c.item_id AND i.observed_at>=p.enabled_at
 WHERE c.network_id=$1 AND NOT EXISTS(SELECT 1 FROM plain_catalog_candidates plain WHERE plain.network_id=c.network_id AND plain.item_id=c.item_id)`, network).Scan(&result.Candidates, &result.Checked, &result.Entries, &result.TotalQuantity)
	return result, err
}
