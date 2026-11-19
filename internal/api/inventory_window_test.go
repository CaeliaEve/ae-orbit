package api_test

import (
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"net/url"
	"testing"
)

func TestHTTPInventoryNumberedPagesLoopAndCount(t *testing.T) {
	for _, sort := range []string{"name", "quantity"} {
		t.Run(sort, func(t *testing.T) {
			f, w, _, ids := browsingFixture(t)
			base := browsePath(f) + "?consistency=window&pagination=pages&sort=" + sort + "&limit=2"
			first := decode[protocol.ItemPage](t, send(t, f, "GET", base, adminToken, ""), 200)
			if first.Total == nil || *first.Total != 3 || first.PageNumber != 1 || first.PageCount != 2 || len(first.Items) != 2 {
				t.Fatal(first)
			}
			last := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&page=last", adminToken, ""), 200)
			if last.PageNumber != 2 || len(last.Items) != 1 {
				t.Fatal(last)
			}
			again := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&page=1", adminToken, ""), 200)
			if again.Items[0].ID != first.Items[0].ID {
				t.Fatal(again)
			}
			clamped := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&page=999999", adminToken, ""), 200)
			if clamped.Items[0].ID != last.Items[0].ID {
				t.Fatal(clamped)
			}
			continued := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&cursor="+url.QueryEscape(first.NextCursor), adminToken, ""), 200)
			if continued.PageNumber != 2 || continued.Items[0].ID != last.Items[0].ID {
				t.Fatal(continued)
			}
			anchor := last.AnchorCursor
			uploadBrowse(t, f, w, 2, []protocol.Update{{ID: ids[1], Quantity: 9007199254740993, Observation: 11}})
			refreshed := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&anchor="+url.QueryEscape(anchor), adminToken, ""), 200)
			if refreshed.Items[0].ID != last.Items[0].ID || refreshed.Total == nil || *refreshed.Total != 3 {
				t.Fatal(refreshed)
			}
			filtered := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&q=Beta&page=last", adminToken, ""), 200)
			if *filtered.Total != 1 || filtered.PageCount != 1 || filtered.Items[0].ID != ids[1] {
				t.Fatal(filtered)
			}
			empty := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&q=missing&page=last", adminToken, ""), 200)
			if *empty.Total != 0 || empty.PageCount != 1 || len(empty.Items) != 0 {
				t.Fatal(empty)
			}
			for _, suffix := range []string{"&page=0", "&page=-1", "&page=1&page=2", "&page=nan", "&page=1&anchor=" + url.QueryEscape(anchor), "&page=1&cursor=" + url.QueryEscape(first.NextCursor), "&pagination=other"} {
				if r := send(t, f, "GET", base+suffix, adminToken, ""); r.StatusCode != 400 {
					t.Fatal(suffix, r.StatusCode)
				}
			}
			for _, query := range []string{"?page=last", "?pagination=pages", "?pagination=pages&consistency=live"} {
				if r := send(t, f, "GET", browsePath(f)+query, adminToken, ""); r.StatusCode != 400 {
					t.Fatal(query, r.StatusCode)
				}
			}
		})
	}
}

func TestHTTPInventoryWindowKeepsAnchorAndFilters(t *testing.T) {
	for _, sort := range []string{"name", "quantity"} {
		t.Run(sort, func(t *testing.T) {
			f, w, _, ids := browsingFixture(t)
			base := browsePath(f) + "?consistency=window&sort=" + sort + "&limit=1"
			first := decode[protocol.ItemPage](t, send(t, f, "GET", base, adminToken, ""), 200)
			second := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&cursor="+url.QueryEscape(first.NextCursor), adminToken, ""), 200)
			if second.AnchorCursor == "" || second.Items[0].ID != ids[1] {
				t.Fatal(second)
			}
			uploadBrowse(t, f, w, 2, []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "test:new", Label: "Aardvark", Exact: true}, Quantity: 999, Observation: 11}, {ID: ids[1], Quantity: 9007199254740993, Observation: 11}})
			refresh := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&anchor="+url.QueryEscape(second.AnchorCursor), adminToken, ""), 200)
			if len(refresh.Items) != 1 || refresh.Items[0].ID != ids[1] || refresh.Items[0].Quantity != 9007199254740993 {
				t.Fatal("anchor jumped", refresh)
			}
			for _, suffix := range []string{"&q=Other", "&cursor=" + url.QueryEscape(first.NextCursor), "&anchor=x"} {
				if r := send(t, f, "GET", base+"&anchor="+url.QueryEscape(second.AnchorCursor)+suffix, adminToken, ""); r.StatusCode != 400 {
					t.Fatal("invalid anchor accepted", suffix, r.StatusCode)
				}
			}
		})
	}
}

func TestHTTPInventoryPinyinAndModSearch(t *testing.T) {
	f, w, _, _ := browsingFixture(t)
	updates := []protocol.Update{
		{Item: &protocol.Definition{Kind: "item", Name: "gregtech:steel_ingot", Label: "钢锭", Exact: true}, Quantity: 42, Observation: 11},
		{Item: &protocol.Definition{Kind: "item", Name: "other:steel_ingot", Label: "钢锭", Exact: true}, Quantity: 77, Observation: 11},
		{Item: &protocol.Definition{Kind: "item", Name: "gregtech:aluminium_plate", Label: "铝板", Exact: true}, Quantity: 19, Observation: 11},
	}
	ack := uploadBrowse(t, f, w, 2, updates)
	for _, query := range []string{"@gregtech 钢", "@GREGTECH gangding", "@gregtech gd", "gang ding @gregtech", "gregtech:steel_ingot"} {
		page := decode[protocol.ItemPage](t, send(t, f, "GET", browsePath(f)+"?q="+url.QueryEscape(query), adminToken, ""), 200)
		if len(page.Items) != 1 || page.Items[0].Name != "gregtech:steel_ingot" {
			t.Fatal(query, page)
		}
	}
	for _, query := range []string{"@gregtech lvban", "@gregtech lüban", "@gregtech lu:ban", "@gregtech lb"} {
		page := decode[protocol.ItemPage](t, send(t, f, "GET", browsePath(f)+"?q="+url.QueryEscape(query), adminToken, ""), 200)
		if len(page.Items) != 1 || page.Items[0].Name != "gregtech:aluminium_plate" {
			t.Fatal(query, page)
		}
	}
	updates[0].Item.Label = "钢板"
	updates[0].Observation = 12
	uploadBrowse(t, f, w, 3, updates[:1])
	page := decode[protocol.ItemPage](t, send(t, f, "GET", browsePath(f)+"?q="+url.QueryEscape("@gregtech gangban"), adminToken, ""), 200)
	if len(page.Items) != 1 || page.Items[0].ID != ack.Bindings[0].ID {
		t.Fatal("renamed search index stale", page)
	}
}
