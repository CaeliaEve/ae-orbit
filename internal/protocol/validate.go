package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
)

func textOK(s string, min, max int) bool {
	return len(s) >= min && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func (d Definition) Key() string {
	identity := d.NBT
	if d.NBT != "" {
		compressed, err := base64.StdEncoding.Strict().DecodeString(d.NBT)
		if err == nil {
			if canonical, err := nbt.Digest(compressed); err == nil {
				identity = "nbt-sha256:" + hex.EncodeToString(canonical[:])
			}
		}
	}
	// Retain distinct legacy keys for unsupported saved tags. Validate rejects
	// new unsupported definitions; migration never guesses their contents.
	encoded, _ := json.Marshal([]any{d.Kind, d.Name, d.Damage, identity})
	h := sha256.Sum256(encoded)
	return hex.EncodeToString(h[:])
}

func (d Definition) Validate() error {
	if d.Kind != "item" && d.Kind != "fluid" {
		return fmt.Errorf("unsupported resource kind")
	}
	if !textOK(d.Name, 1, 256) || !textOK(d.Label, 0, 512) || d.Damage < 0 || d.Damage > 2147483647 {
		return fmt.Errorf("invalid item definition")
	}
	if !d.Exact {
		return fmt.Errorf("item identity is not exact")
	}
	if len(d.NBT) > 16384 {
		return fmt.Errorf("NBT exceeds identity budget")
	}
	if d.NBT != "" {
		decoded, err := base64.StdEncoding.Strict().DecodeString(d.NBT)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != d.NBT {
			return fmt.Errorf("NBT must be canonical base64")
		}
		if _, err = nbt.Digest(decoded); err != nil {
			return err
		}
	}
	return nil
}

func (b InventoryBatch) Validate(maxItems int) error {
	if !textOK(b.SessionID, 1, 128) || b.Sequence < 1 || len(b.Updates) > maxItems {
		return fmt.Errorf("invalid inventory envelope")
	}
	for _, u := range b.Updates {
		if u.Quantity < 0 || u.Observation < 1 || (u.ID > 0) == (u.Item != nil) {
			return fmt.Errorf("invalid inventory update")
		}
		if u.Verification != "" && (len(u.Verification) != 32 || strings.Trim(u.Verification, "0123456789abcdef") != "") {
			return fmt.Errorf("invalid inventory verification")
		}
		if u.Refresh != "" && (len(u.Refresh) != 32 || strings.Trim(u.Refresh, "0123456789abcdef") != "") {
			return fmt.Errorf("invalid inventory refresh receipt")
		}
		if u.Item != nil {
			if err := u.Item.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h Hello) Validate() error {
	if h.Protocol != 1 || !textOK(h.BootID, 1, 128) || (h.Source != "oc" && h.Source != "simulator") {
		return fmt.Errorf("invalid hello")
	}
	if h.MaxResponseBytes < 2048 || h.MaxResponseBytes > 131072 {
		return fmt.Errorf("response budget must be 2048..131072")
	}
	if h.Capabilities.Craft && (h.Capabilities.MaxCraftQuantity < 1 || !h.Capabilities.ExactQuantities) {
		return fmt.Errorf("crafting requires exact quantity capability")
	}
	if !textOK(h.Capabilities.Enumeration, 0, 64) {
		return fmt.Errorf("invalid enumeration capability")
	}
	return nil
}

func (p Poll) Validate(maxCPUs, maxReports int) error {
	if !textOK(p.SessionID, 1, 128) || p.Sequence < 1 || len(p.Reports) > maxReports {
		return fmt.Errorf("invalid poll envelope")
	}
	if (p.RecheckSlots != nil && (*p.RecheckSlots < 0 || *p.RecheckSlots > 16)) ||
		(p.RecheckBytes != nil && (*p.RecheckBytes < 0 || *p.RecheckBytes > 16384)) {
		return fmt.Errorf("invalid recheck capacity")
	}
	if (p.RefreshSlots != nil && (*p.RefreshSlots < 0 || *p.RefreshSlots > 8)) ||
		(p.RefreshBytes != nil && (*p.RefreshBytes < 0 || *p.RefreshBytes > 16384)) {
		return fmt.Errorf("invalid refresh capacity")
	}
	for _, r := range p.Reports {
		if !textOK(r.OrderID, 1, 128) || !textOK(r.AttemptID, 1, 128) || !textOK(r.Reason, 0, 1024) || !ReportState(r.State) {
			return fmt.Errorf("invalid order report")
		}
	}
	if p.CPUs != nil {
		if len(*p.CPUs) > maxCPUs {
			return fmt.Errorf("CPU snapshot exceeds budget")
		}
		seen := map[string]bool{}
		for _, c := range *p.CPUs {
			if !textOK(c.ID, 1, 128) || !textOK(c.Name, 0, 256) || c.Coprocessors < 0 || c.Storage < 0 || !textOK(c.OutputUnavailable, 0, 256) || seen[c.ID] {
				return fmt.Errorf("invalid CPU")
			}
			seen[c.ID] = true
			if c.Output != nil && (!textOK(c.Output.Name, 0, 256) || !textOK(c.Output.Label, 0, 512) || c.Output.Quantity < 0) {
				return fmt.Errorf("invalid CPU output")
			}
		}
	}
	if p.Health != nil {
		if p.Health.FreeMemory < 0 || len(p.Health.Diagnostics) > 16 {
			return fmt.Errorf("invalid device health")
		}
		for _, d := range p.Health.Diagnostics {
			if !textOK(d.Code, 1, 64) || d.Count < 0 {
				return fmt.Errorf("invalid diagnostic")
			}
		}
		if v := p.Health.Inventory; v != nil && (v.Batches < 0 || v.Updates < 0 || v.Bytes < 0 || v.CreditsUsed < 0 || v.CollectMillis < 0) {
			return fmt.Errorf("invalid inventory telemetry")
		}
		if p.Health.Sync != nil {
			if err := p.Health.Sync.Validate(); err != nil {
				return err
			}
		}
		if samples := p.Health.ReadFailures; samples != nil {
			if len(*samples) > MaxReadFailureSamples {
				return fmt.Errorf("too many read failure samples")
			}
			for _, sample := range *samples {
				if err := sample.Validate(); err != nil {
					return err
				}
			}
			var encoded bytes.Buffer
			encoder := json.NewEncoder(&encoded)
			// OC emits ordinary JSON, not an HTML script. Expanding <>& here
			// would reject otherwise byte-bounded exception messages.
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(samples); err != nil || encoded.Len()-1 > MaxReadFailureBytes {
				return fmt.Errorf("read failure samples exceed byte budget")
			}
		}
	}
	return nil
}

func (d ReadFailure) Validate() error {
	if (d.Code != "exact_lookup_failed" && d.Code != "recipe_lookup_failed") ||
		(d.Source != "event" && d.Source != "recheck" && d.Source != "priority" && d.Source != "refresh") ||
		!textOK(d.Name, 1, 256) || !textOK(d.Message, 1, 160) ||
		strings.ContainsFunc(d.Name, unicode.IsControl) || strings.ContainsFunc(d.Message, unicode.IsControl) ||
		d.Damage < 0 || d.Damage > 2147483647 || d.NBTBytes < 0 || d.NBTBytes > 16384 ||
		(d.ItemID != nil && *d.ItemID < 1) || d.Occurrences < 1 || d.LastSeenMillis < 0 {
		return fmt.Errorf("invalid read failure sample")
	}
	if d.NBTAdler32 != "" && (len(d.NBTAdler32) != 8 || strings.Trim(d.NBTAdler32, "0123456789abcdef") != "") {
		return fmt.Errorf("invalid diagnostic NBT checksum")
	}
	return nil
}

func (o OrderInput) Validate() error {
	if o.ItemID < 1 || o.Quantity < 1 || !textOK(o.IdempotencyKey, 8, 128) || o.TTLSeconds < 0 || o.TTLSeconds > 86400 {
		return fmt.Errorf("invalid order")
	}
	return nil
}

func ReportState(state string) bool {
	switch state {
	case "computing", "running", "done", "failed", "canceled", "needs_review":
		return true
	}
	return false
}

func Terminal(state string) bool {
	return state == "done" || state == "failed" || state == "canceled" || state == "expired"
}

func CanTransition(from, to string) bool {
	if from == to {
		return true
	}
	if Terminal(from) || from == "needs_review" || from == "queued" {
		return false
	}
	if from != "assigned" && from != "computing" && from != "running" {
		return false
	}
	if to == "needs_review" || to == "failed" || to == "canceled" || to == "done" {
		return true
	}
	return (from == "assigned" && (to == "computing" || to == "running")) || (from == "computing" && to == "running")
}
