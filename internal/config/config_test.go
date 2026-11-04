package config

import "testing"

func TestConfigRejectsUnsafeOrUnboundedSettings(t *testing.T) {
	base := map[string]string{"AE_DATABASE_URL": "postgres://postgres@localhost/test", "AE_ADMIN_TOKEN": "abcdefghijklmnopqrstuvwxyz0123456789"}
	lookup := func(k string) string { return base[k] }
	c, err := Load(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" || c.Store.MaxConnections < 1 {
		t.Fatal(c.Listen)
	}
	for _, test := range []struct{ key, value string }{{"AE_ADMIN_TOKEN", "short"}, {"AE_DB_CONNECTIONS", "0"}, {"AE_BATCH_BYTES", "-1"}, {"AE_PUBLIC_URL", "http://public.example"}, {"AE_MAX_CONCURRENT", "999999"}} {
		old := base[test.key]
		base[test.key] = test.value
		if _, err = Load(lookup); err == nil {
			t.Errorf("accepted %s", test.key)
		}
		base[test.key] = old
	}
}
