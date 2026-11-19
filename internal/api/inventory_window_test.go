package api_test

import (
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"net/url"
	"testing"
)

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
