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
	QuantityRefresh  bool     `json:"quantity_refresh,omitempty"`
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
	BatchBytes                   int  `json:"batch_bytes"`
	BatchItems                   int  `json:"batch_items"`
	ResponseBytes                int  `json:"response_bytes"`
	Commands                     int  `json:"commands"`
	CPUs                         int  `json:"cpus"`
	PollMillis                   int  `json:"poll_millis"`
	DiagnosticSamples            int  `json:"diagnostic_samples,omitempty"`
	SyncTelemetry                bool `json:"sync_telemetry,omitempty"`
	QuantityRefresh              bool `json:"quantity_refresh,omitempty"`
	ReadSlots                    int  `json:"read_slots,omitempty"`
	ReadBytes                    int  `json:"read_bytes,omitempty"`
	BackgroundRecipeInconclusive bool `json:"background_recipe_inconclusive,omitempty"`
}

type Welcome struct {
	Protocol     int      `json:"protocol"`
	NetworkID    string   `json:"network_id"`
	SessionID    string   `json:"session_id"`
	InventoryAck Quantity `json:"inventory_ack"`
	PollAck      Quantity `json:"poll_ack"`
	Limits       Limits   `json:"limits"`
	DeviceID     string   `json:"device_id,omitempty"`
	Role         string   `json:"role,omitempty"`
}

type Update struct {
	ID                 Quantity    `json:"id,omitempty"`
	Item               *Definition `json:"item,omitempty"`
	Quantity           Quantity    `json:"quantity"`
	Observation        Quantity    `json:"observation"`
	Craftable          *bool       `json:"craftable"`
	Verification       string      `json:"verification,omitempty"`
	Refresh            string      `json:"refresh,omitempty"`
	AssignmentID       string      `json:"assignment_id,omitempty"`
	RecipeInconclusive bool        `json:"recipe_inconclusive,omitempty"`
	legacyReceipts     bool
}

func (u *Update) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID                 Quantity        `json:"id,omitempty"`
		Item               *Definition     `json:"item,omitempty"`
		Quantity           *Quantity       `json:"quantity"`
		Observation        Quantity        `json:"observation"`
		Craftable          json.RawMessage `json:"craftable"`
		Verification       json.RawMessage `json:"verification,omitempty"`
		Refresh            json.RawMessage `json:"refresh,omitempty"`
		AssignmentID       json.RawMessage `json:"assignment_id,omitempty"`
		RecipeInconclusive json.RawMessage `json:"recipe_inconclusive,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.Quantity == nil || len(wire.Craftable) == 0 {
		return fmt.Errorf("inventory quantity and craftable must be explicit")
	}
	recipeInconclusive := len(wire.RecipeInconclusive) > 0
	if recipeInconclusive && !bytes.Equal(bytes.TrimSpace(wire.RecipeInconclusive), []byte("true")) {
		return fmt.Errorf("recipe_inconclusive must be true when present")
	}
	var verification, refresh, assignmentID string
	for _, field := range []struct {
		raw    json.RawMessage
		target *string
	}{{wire.Verification, &verification}, {wire.Refresh, &refresh}, {wire.AssignmentID, &assignmentID}} {
		if len(field.raw) > 0 {
			if err := json.Unmarshal(field.raw, field.target); err != nil {
				return err
			}
		}
	}
	if len(wire.AssignmentID) > 0 && !tokenOK(assignmentID) {
		return fmt.Errorf("invalid assignment ID")
	}
	var craftable *bool
	if err := json.Unmarshal(wire.Craftable, &craftable); err != nil {
		return err
	}
	*u = Update{ID: wire.ID, Item: wire.Item, Quantity: *wire.Quantity, Observation: wire.Observation, Craftable: craftable, Verification: verification, Refresh: refresh, AssignmentID: assignmentID, RecipeInconclusive: recipeInconclusive, legacyReceipts: len(wire.Verification) > 0 || len(wire.Refresh) > 0}
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
	ID                      Quantity   `json:"id"`
	CanonicalID             Quantity   `json:"canonical_id,omitempty"`
	Kind                    string     `json:"kind"`
	Name                    string     `json:"name"`
	Damage                  int        `json:"damage"`
	Label                   string     `json:"label"`
	Exact                   bool       `json:"exact"`
	Quantity                Quantity   `json:"quantity"`
	Craftable               bool       `json:"craftable"`
	ObservedAt              time.Time  `json:"observed_at"`
	CraftableObservedAt     *time.Time `json:"craftable_observed_at,omitempty"`
	CurrentSession          bool       `json:"current_session"`
	CraftableCurrentSession bool       `json:"craftable_current_session"`
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
	Items      []Item        `json:"items"`
	MissingIDs []Quantity    `json:"missing_ids"`
	Revision   Quantity      `json:"revision"`
	Refreshes  []ItemRefresh `json:"refreshes,omitempty"`
}

type ItemRefreshInput struct {
	IDs []Quantity `json:"ids"`
}

type ItemRefresh struct {
	ID          Quantity   `json:"id"`
	RequestID   string     `json:"request_id"`
	State       string     `json:"state"`
	RequestedAt time.Time  `json:"requested_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
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
	FreeMemory   Quantity            `json:"free_memory"`
	Diagnostics  []Diagnostic        `json:"diagnostics"`
	Inventory    *InventoryTelemetry `json:"inventory,omitempty"`
	ReadFailures *[]ReadFailure      `json:"read_failures,omitempty"`
	Sync         *SyncTelemetry      `json:"sync,omitempty"`
}

const MaxReadFailureSamples = 4
const MaxReadFailureBytes = 2048

// ReadFailure is a bounded diagnostic hint, never an authoritative identity.
// Omitted health snapshots retain the last details; an empty slice clears them.
type ReadFailure struct {
	Code           string    `json:"code"`
	Source         string    `json:"source"`
	ItemID         *Quantity `json:"item_id,omitempty"`
	Name           string    `json:"name"`
	Damage         int       `json:"damage"`
	NBTBytes       int       `json:"nbt_bytes"`
	NBTAdler32     string    `json:"nbt_adler32,omitempty"`
	Message        string    `json:"message"`
	Occurrences    Quantity  `json:"occurrences"`
	LastSeenMillis Quantity  `json:"last_seen_millis"`
	Truncated      bool      `json:"truncated,omitempty"`
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
	SessionID        string     `json:"session_id"`
	Sequence         Quantity   `json:"sequence"`
	Reports          []Report   `json:"reports"`
	CPUs             *[]CPU     `json:"cpus,omitempty"`
	CPUsTruncated    bool       `json:"cpus_truncated,omitempty"`
	Health           *Health    `json:"health,omitempty"`
	RecheckSlots     *int       `json:"recheck_slots,omitempty"`
	RecheckBytes     *int       `json:"recheck_bytes,omitempty"`
	RefreshSlots     *int       `json:"refresh_slots,omitempty"`
	RefreshBytes     *int       `json:"refresh_bytes,omitempty"`
	ReadSlots        *int       `json:"read_slots,omitempty"`
	ReadBytes        *int       `json:"read_bytes,omitempty"`
	Hints            []ReadHint `json:"hints,omitempty"`
	legacyReadFields bool
	v2ReadFields     bool
}

func (p *Poll) UnmarshalJSON(data []byte) error {
	type plain Poll
	var wire plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"recheck_slots", "recheck_bytes", "refresh_slots", "refresh_bytes"} {
		if _, ok := fields[key]; ok {
			wire.legacyReadFields = true
		}
	}
	for _, key := range []string{"read_slots", "read_bytes", "hints"} {
		if _, ok := fields[key]; ok {
			wire.v2ReadFields = true
		}
	}
	*p = Poll(wire)
	return nil
}

type ReadHint struct {
	ID   Quantity    `json:"id,omitempty"`
	Item *Definition `json:"item,omitempty"`
}

type ReadAssignment struct {
	ID           Quantity   `json:"id"`
	Item         Definition `json:"item"`
	AssignmentID string     `json:"assignment_id"`
	Kind         string     `json:"kind"`
	Purpose      string     `json:"purpose"`
	TTLMillis    int        `json:"ttl_millis"`
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
	Ack            Quantity          `json:"ack"`
	Commands       []Command         `json:"commands"`
	Verify         []Verification    `json:"verify"`
	PollMillis     int               `json:"poll_millis"`
	ScanBudget     int               `json:"scan_budget"`
	Refresh        []QuantityRefresh `json:"refresh,omitempty"`
	Reads          []ReadAssignment  `json:"reads,omitempty"`
	GrantID        string            `json:"grant_id,omitempty"`
	GrantTTLMillis int               `json:"grant_ttl_millis,omitempty"`
}

func (p PollAck) MarshalJSON() ([]byte, error) {
	type plain PollAck
	// A nonnil Reads slice identifies v2, including empty cached responses.
	if p.Reads == nil {
		return json.Marshal(plain(p))
	}
	commands := p.Commands
	if commands == nil {
		commands = []Command{}
	}
	return json.Marshal(struct {
		Ack            Quantity         `json:"ack"`
		Commands       []Command        `json:"commands"`
		PollMillis     int              `json:"poll_millis"`
		ScanBudget     int              `json:"scan_budget"`
		Reads          []ReadAssignment `json:"reads"`
		GrantID        string           `json:"grant_id,omitempty"`
		GrantTTLMillis int              `json:"grant_ttl_millis,omitempty"`
	}{p.Ack, commands, p.PollMillis, p.ScanBudget, p.Reads, p.GrantID, p.GrantTTLMillis})
}

type QuantityRefresh struct {
	ID        Quantity   `json:"id"`
	Item      Definition `json:"item"`
	Token     string     `json:"token"`
	TTLMillis int        `json:"ttl_millis"`
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
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	ServerGroup      string           `json:"server_group"`
	Source           string           `json:"source"`
	SessionID        string           `json:"session_id,omitempty"`
	Revision         Quantity         `json:"revision"`
	LastSeen         *time.Time       `json:"last_seen,omitempty"`
	LastInventory    *time.Time       `json:"last_inventory,omitempty"`
	LastScan         *time.Time       `json:"last_scan,omitempty"`
	FirstScan        *time.Time       `json:"first_scan,omitempty"`
	SessionStartedAt *time.Time       `json:"session_started_at,omitempty"`
	Capabilities     Capabilities     `json:"capabilities"`
	Health           json.RawMessage  `json:"health,omitempty"`
	Online           bool             `json:"online"`
	Quality          string           `json:"quality"`
	BridgeMode       string           `json:"bridge_mode"`
	Services         *NetworkServices `json:"services,omitempty"`
}

type NetworkServices struct {
	QuantityReaders int        `json:"quantity_readers"`
	Discovery       bool       `json:"discovery"`
	Recipes         bool       `json:"recipes"`
	Craft           bool       `json:"craft"`
	CPUObservedAt   *time.Time `json:"cpu_observed_at,omitempty"`
}

type DeviceView struct {
	ID            string          `json:"id"`
	NetworkID     string          `json:"network_id"`
	Name          string          `json:"name"`
	Role          string          `json:"role"`
	SessionID     string          `json:"session_id,omitempty"`
	Capabilities  Capabilities    `json:"capabilities"`
	Health        json.RawMessage `json:"health,omitempty"`
	LastSeen      *time.Time      `json:"last_seen,omitempty"`
	LastInventory *time.Time      `json:"last_inventory,omitempty"`
	Online        bool            `json:"online"`
	Revoked       bool            `json:"revoked"`
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
