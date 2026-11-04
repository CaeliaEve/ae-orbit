package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

type Config struct {
	Listen      string
	DatabaseURL string
	AdminToken  string
	PublicURL   string
	Store       store.Options
	HTTP        api.Options
}

func Load(env func(string) string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8080", DatabaseURL: env("AE_DATABASE_URL"), AdminToken: env("AE_ADMIN_TOKEN"), PublicURL: "http://127.0.0.1:8080", Store: store.DefaultOptions(), HTTP: api.DefaultOptions()}
	if value := env("AE_LISTEN"); value != "" {
		c.Listen = value
	}
	if value := env("AE_PUBLIC_URL"); value != "" {
		c.PublicURL = value
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return c, fmt.Errorf("AE_DATABASE_URL must be a PostgreSQL URL")
	}
	if len(c.AdminToken) < 32 || len(c.AdminToken) > 256 {
		return c, fmt.Errorf("AE_ADMIN_TOKEN must contain 32..256 characters")
	}
	u, err = url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return c, fmt.Errorf("AE_PUBLIC_URL must be an origin without a path")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return c, fmt.Errorf("AE_PUBLIC_URL requires HTTPS except on loopback")
	}
	c.HTTP.Origin = c.PublicURL
	c.HTTP.SecureCookies = u.Scheme == "https"
	if _, _, err = net.SplitHostPort(c.Listen); err != nil {
		return c, fmt.Errorf("AE_LISTEN must be host:port")
	}
	settings := []struct {
		name     string
		min, max int
		set      func(int)
	}{
		{"AE_DB_CONNECTIONS", 1, 128, func(n int) { c.Store.MaxConnections = int32(n) }},
		{"AE_BATCH_BYTES", 2048, 131072, func(n int) { c.Store.Limits.BatchBytes = n }},
		{"AE_BATCH_ITEMS", 1, 512, func(n int) { c.Store.Limits.BatchItems = n }},
		{"AE_RESPONSE_BYTES", 2048, 131072, func(n int) { c.Store.Limits.ResponseBytes = n }},
		{"AE_GROUP_PLANNING", 1, 16, func(n int) { c.Store.GroupPlanning = n }},
		{"AE_SCAN_ITEMS", 1, 512, func(n int) { c.Store.ScanItems = n }},
		{"AE_ACTIVE_WINDOW", 1, 16, func(n int) { c.Store.ActiveWindow = n }},
		{"AE_MAX_QUEUED", 1, 4096, func(n int) { c.Store.MaxQueuedOrders = n }},
		{"AE_MAX_CONCURRENT", 1, 512, func(n int) { c.HTTP.MaxConcurrent = n }},
		{"AE_MAX_STREAMS", 1, 128, func(n int) { c.HTTP.MaxStreams = n }},
		{"AE_OFFLINE_SECONDS", 15, 3600, func(n int) { c.Store.OfflineAfter = time.Duration(n) * time.Second }},
		{"AE_RECHECK_SECONDS", 5, 86400, func(n int) { c.Store.RecheckAfter = time.Duration(n) * time.Second }},
		{"AE_POLL_MILLIS", 100, 30000, func(n int) { c.Store.Limits.PollMillis = n }},
	}
	for _, setting := range settings {
		if value := env(setting.name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < setting.min || n > setting.max {
				return c, fmt.Errorf("%s must be in %d..%d", setting.name, setting.min, setting.max)
			}
			setting.set(n)
		}
	}
	return c, nil
}
