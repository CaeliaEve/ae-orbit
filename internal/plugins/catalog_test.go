package plugins

import "testing"

func TestEnergyPreservesBeyondInt64(t *testing.T) {
	stored := "1234567890123456789012345678901234567890"
	capacity := "2469135780246913578024691357802469135780"
	if ValidateEnergy(stored, capacity) != nil || Percent(stored, capacity) != "50.00" {
		t.Fatal("large energy lost precision")
	}
	for _, x := range []string{"-1", "01", "1e30", "1.5", "", " 10"} {
		if _, err := Energy(x); err == nil {
			t.Fatal("invalid energy", x)
		}
	}
	if ValidateEnergy("0", "0") == nil || ValidateEnergy("2", "1") == nil {
		t.Fatal("invalid capacity accepted")
	}
	if !Defaults().Valid() {
		t.Fatal("invalid default settings")
	}
}
func TestFastSamplingKeepsIndependentHistoryCadence(t *testing.T) {
	s := Defaults()
	s.IntervalSeconds = 1
	if !s.Valid() || s.HistoryCadence() != 10 {
		t.Fatal(s)
	}
	s.HistorySeconds = 5
	if !s.Valid() || s.HistoryCadence() != 5 {
		t.Fatal(s)
	}
	s.HistorySeconds = 1
	if s.Valid() {
		t.Fatal("one-second raw persistence enabled")
	}
	s.HistorySeconds = 0
	if !s.Valid() || s.HistoryCadence() != 10 {
		t.Fatal("old configuration incompatible")
	}
	s.IntervalSeconds = 30
	if s.HistoryCadence() != 30 {
		t.Fatal("history faster than acquisition")
	}
	s.IntervalSeconds = 0
	if s.Valid() {
		t.Fatal("invalid interval")
	}
}
func TestLowEnergyCanNotifyImmediatelyWithHysteresis(t *testing.T) {
	s := Defaults()
	s.LowPercent = 35
	s.RecoverPercent = 40
	s.LowDurationSeconds = 0
	if !s.Valid() || s.FallingMinutes != 0 {
		t.Fatal(s)
	}
	s.RecoverPercent = 35
	if s.Valid() {
		t.Fatal("invalid recovery boundary")
	}
}
