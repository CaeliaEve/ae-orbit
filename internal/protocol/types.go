// Package protocol defines the versioned boundary shared by the service and devices.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Quantity carries exact nonnegative int64 values across JSON, including IDs.
type Quantity int64

func (q Quantity) String() string { return strconv.FormatInt(int64(q), 10) }
func (q Quantity) MarshalJSON() ([]byte, error) {
	if q < 0 {
		return nil, fmt.Errorf("negative quantity")
	}
	return []byte(`"` + q.String() + `"`), nil
}
func (q *Quantity) UnmarshalJSON(data []byte) error {
	if len(data) < 3 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("quantity must be a decimal string")
	}
	value := string(data[1 : len(data)-1])
	if len(value) > 1 && value[0] == '0' {
		return fmt.Errorf("noncanonical quantity")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return fmt.Errorf("invalid quantity")
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("quantity out of range")
	}
	*q = Quantity(n)
	return nil
}

type Definition struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Damage int    `json:"damage"`
	NBT    string `json:"nbt,omitempty"`
	Label  string `json:"label"`
	Exact  bool   `json:"exact"`
}

type Capabilities struct {
	Inventory        bool     `json:"inventory"`
	Craft            bool     `json:"craft"`
	CPUOutput        bool     `json:"cpu_output"`
	ExactQuantities  bool     `json:"exact_quantities"`
	MaxCraftQuantity Quantity `json:"max_craft_quantity"`
	Enumeration      string   `json:"enumeration"`
}

type Hello struct {
	Protocol         int          `json:"protocol"`
	BootID           string       `json:"boot_id"`
	Source           string       `json:"source"`
	Capabilities     Capabilities `json:"capabilities"`
	MaxResponseBytes int          `json:"max_response_bytes"`
}

type Limits struct {
	BatchBytes    int `json:"batch_bytes"`
	BatchItems    int `json:"batch_items"`
	ResponseBytes int `json:"response_bytes"`
	Commands      int `json:"commands"`
	CPUs          int `json:"cpus"`
	PollMillis    int `json:"poll_millis"`
}

type Welcome struct {
	Protocol     int      `json:"protocol"`
	NetworkID    string   `json:"network_id"`
	SessionID    string   `json:"session_id"`
	InventoryAck Quantity `json:"inventory_ack"`
	PollAck      Quantity `json:"poll_ack"`
	Limits       Limits   `json:"limits"`
}

type Update struct {
	ID           Quantity    `json:"id,omitempty"`
	Item         *Definition `json:"item,omitempty"`
	Quantity     Quantity    `json:"quantity"`
	Observation  Quantity    `json:"observation"`
	Craftable    *bool       `json:"craftable"`
	Verification string      `json:"verification,omitempty"`
}

func (u *Update) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID           Quantity        `json:"id,omitempty"`
		Item         *Definition     `json:"item,omitempty"`
		Quantity     *Quantity       `json:"quantity"`
		Observation  Quantity        `json:"observation"`
		Craftable    json.RawMessage `json:"craftable"`
		Verification string          `json:"verification,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.Quantity == nil || len(wire.Craftable) == 0 {
		return fmt.Errorf("inventory quantity and craftable must be explicit")
	}
	var craftable *bool
	if err := json.Unmarshal(wire.Craftable, &craftable); err != nil {
		return err
	}
	*u = Update{ID: wire.ID, Item: wire.Item, Quantity: *wire.Quantity, Observation: wire.Observation, Craftable: craftable, Verification: wire.Verification}
	return nil
}

type InventoryBatch struct {
	SessionID     string   `json:"session_id"`
	Sequence      Quantity `json:"sequence"`
	Updates       []Update `json:"updates"`
	ScanCompleted bool     `json:"scan_completed,omitempty"`
}

type Binding struct {
	Index int      `json:"index"`
	ID    Quantity `json:"id"`
}

type InventoryAck struct {
	Ack      Quantity  `json:"ack"`
	Revision Quantity  `json:"revision"`
	Bindings []Binding `json:"bindings"`
}

type Item struct {
	ID                  Quantity   `json:"id"`
	Kind                string     `json:"kind"`
	Name                string     `json:"name"`
	Damage              int        `json:"damage"`
	Label               string     `json:"label"`
	Exact               bool       `json:"exact"`
	Quantity            Quantity   `json:"quantity"`
	Craftable           bool       `json:"craftable"`
	ObservedAt          time.Time  `json:"observed_at"`
	CraftableObservedAt *time.Time `json:"craftable_observed_at,omitempty"`
	CurrentSession      bool       `json:"current_session"`
}

type ItemPage struct {
	Items      []Item   `json:"items"`
	Revision   Quantity `json:"revision"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type ItemQuery struct {
	Search      string
	Sort        string
	Craftable   *bool
	Limit       int
	Cursor      string
	Consistency string
}

type ItemLookup struct {
	Items      []Item     `json:"items"`
	MissingIDs []Quantity `json:"missing_ids"`
	Revision   Quantity   `json:"revision"`
}

type Stack struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Quantity Quantity `json:"quantity"`
}

type CPU struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Busy              bool     `json:"busy"`
	Storage           Quantity `json:"storage"`
	Coprocessors      int      `json:"coprocessors"`
	Output            *Stack   `json:"output,omitempty"`
	OutputUnavailable string   `json:"output_unavailable,omitempty"`
}

type Report struct {
	OrderID   string `json:"order_id"`
	AttemptID string `json:"attempt_id"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type Diagnostic struct {
	Code  string   `json:"code"`
	Count Quantity `json:"count"`
}

type Health struct {
	FreeMemory  Quantity            `json:"free_memory"`
	Diagnostics []Diagnostic        `json:"diagnostics"`
	Inventory   *InventoryTelemetry `json:"inventory,omitempty"`
}

// InventoryTelemetry counts acknowledged payloads and collection work per boot.
// It does not count unique item identities or retransmitted network bytes.
type InventoryTelemetry struct {
	Batches       Quantity `json:"batches"`
	Updates       Quantity `json:"updates"`
	Bytes         Quantity `json:"bytes"`
	CreditsUsed   Quantity `json:"credits_used"`
	CollectMillis Quantity `json:"collect_millis"`
}

type Poll struct {
	SessionID     string   `json:"session_id"`
	Sequence      Quantity `json:"sequence"`
	Reports       []Report `json:"reports"`
	CPUs          *[]CPU   `json:"cpus,omitempty"`
	CPUsTruncated bool     `json:"cpus_truncated,omitempty"`
	Health        *Health  `json:"health,omitempty"`
	RecheckSlots  *int     `json:"recheck_slots,omitempty"`
	RecheckBytes  *int     `json:"recheck_bytes,omitempty"`
}

type Command struct {
	OrderID         string     `json:"order_id"`
	AttemptID       string     `json:"attempt_id"`
	ItemID          Quantity   `json:"item_id"`
	Item            Definition `json:"item"`
	Quantity        Quantity   `json:"quantity"`
	CurrentQuantity Quantity   `json:"current_quantity"`
}

type Verification struct {
	ID        Quantity   `json:"id"`
	Item      Definition `json:"item"`
	Craftable bool       `json:"craftable"`
	Token     string     `json:"token,omitempty"`
}

type PollAck struct {
	Ack        Quantity       `json:"ack"`
	Commands   []Command      `json:"commands"`
	Verify     []Verification `json:"verify"`
	PollMillis int            `json:"poll_millis"`
	ScanBudget int            `json:"scan_budget"`
}

type OrderInput struct {
	ItemID         Quantity `json:"item_id"`
	Quantity       Quantity `json:"quantity"`
	IdempotencyKey string   `json:"idempotency_key"`
	TTLSeconds     int      `json:"ttl_seconds,omitempty"`
}

type Order struct {
	ID        string    `json:"id"`
	NetworkID string    `json:"network_id"`
	UserID    string    `json:"user_id"`
	ItemID    Quantity  `json:"item_id"`
	Quantity  Quantity  `json:"quantity"`
	State     string    `json:"state"`
	AttemptID string    `json:"attempt_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Network struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ServerGroup   string          `json:"server_group"`
	Source        string          `json:"source"`
	SessionID     string          `json:"session_id,omitempty"`
	Revision      Quantity        `json:"revision"`
	LastSeen      *time.Time      `json:"last_seen,omitempty"`
	LastInventory *time.Time      `json:"last_inventory,omitempty"`
	LastScan      *time.Time      `json:"last_scan,omitempty"`
	Capabilities  Capabilities    `json:"capabilities"`
	Health        json.RawMessage `json:"health,omitempty"`
	Online        bool            `json:"online"`
	Quality       string          `json:"quality"`
}

type Member struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}

type Grant struct {
	NetworkID string `json:"network_id"`
	CanCraft  bool   `json:"can_craft"`
}

type Event struct {
	ID        Quantity  `json:"id"`
	NetworkID string    `json:"network_id"`
	Kind      string    `json:"kind"`
	Revision  Quantity  `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
}
