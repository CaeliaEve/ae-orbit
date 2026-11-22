// Package plugins defines reviewed read-only integrations shipped with AE Orbit.
package plugins

import (
	"fmt"
	"math/big"
	"regexp"
)

const LSC = "gtnh.lsc"
const QQ = "aeorbit.qq"
const Subnet = "aeorbit.subnet"
const MaxObjects = 16

type Manifest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Description     string   `json:"description"`
	ReadOnly        bool     `json:"read_only"`
	Component       string   `json:"component"`
	Methods         []string `json:"methods"`
	IntervalSeconds int      `json:"interval_seconds"`
}

func Catalog() []Manifest {
	return []Manifest{
		{LSC, "蓝波顿超级电容库", "1.1.0", "储能、容量、长期趋势与站内告警；支持每秒实时采集与独立历史间隔。", true, "LSC", []string{"getStoredEUString", "getEUCapacityString"}, 10},
		{QQ, "QQ 联动", "1.0.0", "通过 MaiBot 接收蓝波顿低电量告警和 CPU 任务提醒，支持群内 @ 与私聊。", true, "", []string{}, 0},
		{Subnet, "子 ME 网络库存监控", "1.0.0", "自选物品与流体、数量阈值、库存趋势与 QQ 预警。每个子网一台专用 OC，精确查询。", true, "me_interface", []string{"getItemInNetwork", "getFluidInNetwork"}, 1},
	}
}
func Supported(id string) bool { return id == LSC }

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,127})$`)

func Energy(value string) (*big.Int, error) {
	if !decimal.MatchString(value) {
		return nil, fmt.Errorf("energy must be a canonical nonnegative decimal of at most 128 digits")
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf("invalid energy")
	}
	return n, nil
}
func ValidateEnergy(stored, capacity string) error {
	a, err := Energy(stored)
	if err != nil {
		return err
	}
	b, err := Energy(capacity)
	if err != nil {
		return err
	}
	if b.Sign() == 0 || a.Cmp(b) > 0 {
		return fmt.Errorf("unformed or inconsistent capacitor capacity")
	}
	return nil
}
func Percent(stored, capacity string) string {
	a, e := Energy(stored)
	if e != nil {
		return ""
	}
	b, e := Energy(capacity)
	if e != nil || b.Sign() == 0 {
		return ""
	}
	return new(big.Rat).SetFrac(new(big.Int).Mul(a, big.NewInt(100)), b).FloatString(2)
}

type Settings struct {
	IntervalSeconds    int  `json:"interval_seconds"`
	HistorySeconds     int  `json:"history_interval_seconds"`
	LowPercent         int  `json:"low_percent"`
	RecoverPercent     int  `json:"recover_percent"`
	LowDurationSeconds int  `json:"low_duration_seconds"`
	OfflineSeconds     int  `json:"offline_seconds"`
	FallingMinutes     int  `json:"falling_minutes"`
	RawRetentionDays   int  `json:"raw_retention_days"`
	Alerts             bool `json:"alerts"`
}

func Defaults() Settings {
	return Settings{IntervalSeconds: 10, HistorySeconds: 10, LowPercent: 20, RecoverPercent: 25, LowDurationSeconds: 30, OfflineSeconds: 60, FallingMinutes: 0, RawRetentionDays: 30, Alerts: true}
}

// Omitted by pre-1.1 clients: preserve the ten-second history cadence.
func (s Settings) HistoryCadence() int {
	n := s.HistorySeconds
	if n == 0 {
		n = 10
	}
	if n < s.IntervalSeconds {
		n = s.IntervalSeconds
	}
	return n
}
func (s Settings) Valid() bool {
	return s.IntervalSeconds >= 1 && s.IntervalSeconds <= 3600 && (s.HistorySeconds == 0 || s.HistorySeconds >= 5 && s.HistorySeconds <= 3600) && s.LowPercent >= 1 && s.LowPercent <= 95 && s.RecoverPercent > s.LowPercent && s.RecoverPercent <= 100 && s.LowDurationSeconds >= 0 && s.LowDurationSeconds <= 3600 && s.OfflineSeconds >= max(30, s.IntervalSeconds*3) && s.OfflineSeconds <= 10800 && s.FallingMinutes >= 0 && s.FallingMinutes <= 1440 && (s.RawRetentionDays == 0 || s.RawRetentionDays >= 7 && s.RawRetentionDays <= 3650)
}
