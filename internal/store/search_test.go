package store

import (
	"strings"
	"testing"
)

func TestSearchLabelForms(t *testing.T) {
	if searchLabel("") == "" {
		t.Fatal("empty label would never finish backfill")
	}
	for _, test := range []struct {
		label string
		forms []string
	}{
		{"钢锭", []string{"gangding", "gd"}},
		{"铝板", []string{"lvban", "lb"}},
		{"HV 钢锭", []string{"hv", "hvgangding", "hvgd"}},
	} {
		value := searchLabel(test.label)
		for _, form := range test.forms {
			if !strings.Contains(value, form) {
				t.Fatalf("%q missing %q: %q", test.label, form, value)
			}
		}
	}
	if normalizeSearch("LÜBAN LU:BAN LVBAN") != "lvban lvban lvban" {
		t.Fatal("umlaut normalization")
	}
}

func TestSearchTokensAndLiterals(t *testing.T) {
	params := []any{}
	add := func(value any) string { params = append(params, value); return "?" }
	conditions, err := searchConditions("@GregTech gd 50%_", "c.label", "c.search_text", add)
	if err != nil || len(conditions) != 3 || params[0] != "gregtech" || params[1] != "%gd%" || params[2] != `%50\%\_%` {
		t.Fatal(conditions, params, err)
	}
	for _, query := range []string{"@", "@mod:thing", "@bad%", strings.Repeat("word ", 17)} {
		if _, err := searchConditions(query, "d.label", "d.search_label", add); err == nil {
			t.Fatal("invalid query accepted", query)
		}
	}
}
