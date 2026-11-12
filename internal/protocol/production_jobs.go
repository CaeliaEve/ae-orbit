package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxProductionJobEntries = 500

type ProductionJobControlInput struct {
	OperationKey           string   `json:"operation_key"`
	ExpectedControlVersion Quantity `json:"expected_control_version"`
}

type ProductionJobEntryInput struct {
	EntryKey string   `json:"entry_key"`
	ItemID   Quantity `json:"item_id"`
	Quantity Quantity `json:"quantity"`
}

type ProductionJobInput struct {
	RequestKey string                    `json:"request_key"`
	Name       string                    `json:"name,omitempty"`
	TTLSeconds int                       `json:"ttl_seconds"`
	Entries    []ProductionJobEntryInput `json:"entries"`
}

// encoding/json repairs malformed UTF-8, which must not normalize accepted intent.
func (in *ProductionJobInput) UnmarshalJSON(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid production job UTF-8")
	}
	type manifest ProductionJobInput
	var value manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*in = ProductionJobInput(value)
	return nil
}

func ProductionJobKeyValid(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func (in ProductionJobInput) Normalize() (ProductionJobInput, error) {
	if !ProductionJobKeyValid(in.RequestKey) || len(in.Name) > 128 || !utf8.ValidString(in.Name) ||
		in.TTLSeconds < 0 || in.TTLSeconds > 86400 || len(in.Entries) < 1 || len(in.Entries) > MaxProductionJobEntries {
		return ProductionJobInput{}, fmt.Errorf("invalid production job manifest")
	}
	for _, c := range in.Name {
		if unicode.IsControl(c) {
			return ProductionJobInput{}, fmt.Errorf("invalid production job name")
		}
	}
	seen := make(map[string]bool, len(in.Entries))
	for _, entry := range in.Entries {
		if !ProductionJobKeyValid(entry.EntryKey) || seen[entry.EntryKey] || entry.ItemID < 1 || entry.Quantity < 1 {
			return ProductionJobInput{}, fmt.Errorf("invalid production job entry")
		}
		seen[entry.EntryKey] = true
	}
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 3600
	}
	in.Entries = append([]ProductionJobEntryInput(nil), in.Entries...)
	return in, nil
}

func (in ProductionJobInput) Validate() error {
	_, err := in.Normalize()
	return err
}

// ManifestDigest hashes normalized requested identities, never resolved aliases.
func (in ProductionJobInput) ManifestDigest() string {
	payload, _ := json.Marshal(struct {
		Version int                       `json:"version"`
		Name    string                    `json:"name"`
		TTL     int                       `json:"ttl_seconds"`
		Entries []ProductionJobEntryInput `json:"entries"`
	}{1, in.Name, in.TTLSeconds, in.Entries})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

type ProductionJobCounts struct {
	Pending     Quantity `json:"pending"`
	Rejected    Quantity `json:"rejected"`
	Queued      Quantity `json:"queued"`
	Assigned    Quantity `json:"assigned"`
	Computing   Quantity `json:"computing"`
	Running     Quantity `json:"running"`
	NeedsReview Quantity `json:"needs_review"`
	Done        Quantity `json:"done"`
	Failed      Quantity `json:"failed"`
	Canceled    Quantity `json:"canceled"`
	Expired     Quantity `json:"expired"`
}

type ProductionJobSummary struct {
	ID             string              `json:"id"`
	NetworkID      string              `json:"network_id"`
	OwnerID        string              `json:"owner_id"`
	Name           string              `json:"name"`
	ManifestDigest string              `json:"manifest_digest"`
	Control        string              `json:"control"`
	Reason         string              `json:"reason,omitempty"`
	Result         string              `json:"result,omitempty"`
	Version        Quantity            `json:"version"`
	ControlVersion Quantity            `json:"control_version"`
	Total          Quantity            `json:"total"`
	Counts         ProductionJobCounts `json:"counts"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	SettledAt      *time.Time          `json:"settled_at,omitempty"`
}

type ProductionJobEntry struct {
	ID              string     `json:"id"`
	EntryKey        string     `json:"entry_key"`
	Ordinal         int        `json:"ordinal"`
	RequestedItemID Quantity   `json:"requested_item_id"`
	ItemID          Quantity   `json:"item_id"`
	Quantity        Quantity   `json:"quantity"`
	State           string     `json:"state"`
	Reason          string     `json:"reason,omitempty"`
	OrderID         string     `json:"order_id,omitempty"`
	AttemptID       string     `json:"attempt_id,omitempty"`
	TerminalAt      *time.Time `json:"terminal_at,omitempty"`
}

type ProductionJobDetail struct {
	Summary ProductionJobSummary `json:"summary"`
	Entries []ProductionJobEntry `json:"entries"`
}

type ProductionJobReceipt struct {
	JobID          string `json:"job_id"`
	ManifestDigest string `json:"manifest_digest"`
	Archived       bool   `json:"archived"`
}

type ProductionJobAcceptance struct {
	Receipt  ProductionJobReceipt `json:"receipt"`
	Job      *ProductionJobDetail `json:"job,omitempty"`
	Replayed bool                 `json:"-"`
}

type ProductionJobSnapshot struct {
	Jobs              []ProductionJobSummary `json:"jobs"`
	Watermark         Quantity               `json:"watermark"`
	UnsettledCount    Quantity               `json:"unsettled_count"`
	SettledCount      Quantity               `json:"settled_count"`
	DetailedCount     Quantity               `json:"detailed_count"`
	ReceiptCount      Quantity               `json:"receipt_count"`
	EntryReceiptCount Quantity               `json:"entry_receipt_count"`
	Limits            map[string]int         `json:"limits"`
}

type ProductionJobHistoryPage struct {
	Jobs       []ProductionJobSummary `json:"jobs"`
	NextCursor string                 `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
}

type ProductionJobChange struct {
	Revision Quantity              `json:"revision"`
	JobID    string                `json:"job_id"`
	Version  Quantity              `json:"version"`
	Summary  *ProductionJobSummary `json:"summary,omitempty"`
	Removed  bool                  `json:"removed"`
}

type ProductionJobChangePage struct {
	Changes   []ProductionJobChange `json:"changes"`
	NextAfter Quantity              `json:"next_after"`
	HasMore   bool                  `json:"has_more"`
	Watermark Quantity              `json:"watermark"`
	Reset     bool                  `json:"reset"`
}
