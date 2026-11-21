// Package plugins defines reviewed read-only integrations shipped with AE Orbit.
package plugins

import (
	"fmt"
	"math/big"
	"regexp"
)

const LSC = "gtnh.lsc"
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
	return []Manifest{{LSC, "蓝波顿超级电容库", "1.0.0", "储能、容量、长期趋势与站内告警；支持多座电容库和独立监控 OC。", true, "LSC", []string{"getStoredEUString", "getEUCapacityString"}, 10}}
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
	LowPercent         int  `json:"low_percent"`
	RecoverPercent     int  `json:"recover_percent"`
	LowDurationSeconds int  `json:"low_duration_seconds"`
	OfflineSeconds     int  `json:"offline_seconds"`
	FallingMinutes     int  `json:"falling_minutes"`
	RawRetentionDays   int  `json:"raw_retention_days"`
	Alerts             bool `json:"alerts"`
}

func Defaults() Settings { return Settings{10, 20, 25, 30, 60, 15, 30, true} }
func (s Settings) Valid() bool {
	return s.IntervalSeconds >= 10 && s.IntervalSeconds <= 3600 && s.LowPercent >= 1 && s.LowPercent <= 95 && s.RecoverPercent > s.LowPercent && s.RecoverPercent <= 100 && s.LowDurationSeconds >= 10 && s.LowDurationSeconds <= 3600 && s.OfflineSeconds >= s.IntervalSeconds*3 && s.OfflineSeconds <= 10800 && s.FallingMinutes >= 0 && s.FallingMinutes <= 1440 && (s.RawRetentionDays == 0 || s.RawRetentionDays >= 7 && s.RawRetentionDays <= 3650)
}
