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
