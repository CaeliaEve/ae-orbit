// Package simulator implements a bounded synthetic device speaking the public bridge protocol.
package simulator

import (
	"container/list"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

type Config struct {
	URL          string
	Token        string
	Items        int
	BatchItems   int
	CacheEntries int
}
type Statistics struct {
	CatalogSent  int   `json:"catalog_sent"`
	UploadedRows int64 `json:"uploaded_rows"`
	Batches      int64 `json:"batches"`
	Cycles       int64 `json:"cycles"`
	Completed    int64 `json:"completed"`
	CacheEntries int   `json:"cache_entries"`
}
type cachedID struct {
	index int
	id    protocol.Quantity
}
type job struct {
	command protocol.Command
	stage   int
}
type pendingInventory struct {
	body    []byte
	indices []int
	rows    int
	catalog int
}

// Runner has one owner; Step and Stats are called by the same control loop.
type Runner struct {
	config       Config
	http         *http.Client
	welcome      protocol.Welcome
	stats        Statistics
	inventorySeq protocol.Quantity
	pollSeq      protocol.Quantity
	observation  protocol.Quantity
	cursor       int
	refresh      int
	cache        map[int]*list.Element
	lru          *list.List
	jobs         map[string]*job
	pending      *pendingInventory
	pendingPoll  *protocol.Poll
	nextPoll     time.Duration
}

func New(c Config) (*Runner, error) {
	c.URL = strings.TrimRight(c.URL, "/")
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("invalid service origin")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, fmt.Errorf("HTTPS is required outside loopback")
	}
	if len(c.Token) < 32 || len(c.Token) > 256 || c.Items < 1 || c.Items > 10000000 || c.BatchItems < 1 || c.BatchItems > 512 || c.CacheEntries < 1 || c.CacheEntries > 65536 {
		return nil, fmt.Errorf("invalid simulator budgets")
	}
	return &Runner{config: c, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cache: map[int]*list.Element{}, lru: list.New(), jobs: map[string]*job{}}, nil
}
func (r *Runner) Connect(ctx context.Context) error {
	if r.welcome.SessionID != "" {
		return fmt.Errorf("runner is already connected")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	h := protocol.Hello{Protocol: 1, BootID: "sim-" + hex.EncodeToString(random[:]), Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, Craft: true, CPUOutput: true, ExactQuantities: true, MaxCraftQuantity: math.MaxInt64, Enumeration: "simulated"}}
	if err := r.call(ctx, "/bridge/v1/hello", h, &r.welcome); err != nil {
		return err
	}
	if r.welcome.Protocol != 1 || r.welcome.SessionID == "" || r.welcome.Limits.BatchItems < 1 || r.welcome.Limits.BatchBytes < 512 {
		return fmt.Errorf("invalid welcome")
	}
	r.nextPoll = time.Duration(r.welcome.Limits.PollMillis) * time.Millisecond
	return nil
}
func (r *Runner) Stats() Statistics       { s := r.stats; s.CacheEntries = len(r.cache); return s }
func (r *Runner) NextPoll() time.Duration { return r.nextPoll }

func (r *Runner) Step(ctx context.Context) error {
	if r.welcome.SessionID == "" {
		return fmt.Errorf("connect first")
	}
	if r.pending != nil {
		if err := r.flush(ctx); err != nil {
			return err
		}
	}
	if r.pendingPoll == nil {
		reports := []protocol.Report{}
		ids := make([]string, 0, len(r.jobs))
		for id := range r.jobs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		cpus := []protocol.CPU{{ID: "sim-cpu-1", Name: "Simulated CPU", Storage: 1048576, Coprocessors: 4}}
		for _, id := range ids {
			j := r.jobs[id]
			state := []string{"computing", "running", "done"}[min(j.stage, 2)]
			reason := ""
			if j.command.Quantity > math.MaxInt64-j.command.CurrentQuantity {
				state = "failed"
				reason = "simulated output exceeds int64"
			}
			reports = append(reports, protocol.Report{OrderID: j.command.OrderID, AttemptID: id, State: state, Reason: reason})
		}
		if len(ids) > 0 {
			j := r.jobs[ids[0]]
			cpus[0].Busy = true
			cpus[0].Output = &protocol.Stack{Name: j.command.Item.Name, Label: j.command.Item.Label, Quantity: j.command.Quantity}
		}
		r.pendingPoll = &protocol.Poll{SessionID: r.welcome.SessionID, Sequence: r.pollSeq + 1, Reports: reports, CPUs: &cpus, Health: &protocol.Health{Diagnostics: []protocol.Diagnostic{}}}
	}
	var ack protocol.PollAck
	if err := r.call(ctx, "/bridge/v1/poll", r.pendingPoll, &ack); err != nil {
		return err
	}
	if ack.Ack != r.pendingPoll.Sequence || len(ack.Commands) > 16 || ack.ScanBudget < 0 || ack.ScanBudget > 512 {
		return fmt.Errorf("invalid poll confirmation")
	}
	for _, report := range r.pendingPoll.Reports {
		if j, ok := r.jobs[report.AttemptID]; ok {
			if protocol.Terminal(report.State) {
				delete(r.jobs, report.AttemptID)
				if report.State == "done" {
					r.stats.Completed++
				}
			} else {
				j.stage++
			}
		}
	}
	r.pollSeq = ack.Ack
	r.pendingPoll = nil
	r.nextPoll = time.Duration(ack.PollMillis) * time.Millisecond
	for _, command := range ack.Commands {
		if _, exists := r.jobs[command.AttemptID]; !exists {
			if len(r.jobs) >= 16 {
				return fmt.Errorf("active simulation window exceeded")
			}
			r.jobs[command.AttemptID] = &job{command: command}
		}
	}
	r.stats.Cycles++
	remaining := ack.ScanBudget
	for remaining > 0 {
		count, err := r.prepare(min(remaining, r.config.BatchItems, r.welcome.Limits.BatchItems))
		if err != nil {
			return err
		}
		if err = r.flush(ctx); err != nil {
			return err
		}
		remaining -= count
	}
	return nil
}

func (r *Runner) definition(index int) *protocol.Definition {
	return &protocol.Definition{Kind: "item", Name: fmt.Sprintf("sim:component_%08d", index), Label: fmt.Sprintf("Simulated component %08d", index), Exact: true}
}
func (r *Runner) prepare(count int) (int, error) {
	b := protocol.InventoryBatch{SessionID: r.welcome.SessionID, Sequence: r.inventorySeq + 1, Updates: []protocol.Update{}}
	indices := []int{}
	size := 256
	catalog := 0
	for len(b.Updates) < count {
		initial := r.cursor < r.config.Items
		index := r.refresh
		if initial {
			index = r.cursor
		}
		r.observation++
		u := protocol.Update{Quantity: protocol.Quantity(1000000 + int64(index) + r.stats.Cycles), Observation: r.observation, Craftable: index%3 == 0}
		if e := r.cache[index]; e != nil {
			u.ID = e.Value.(cachedID).id
			r.lru.MoveToFront(e)
		} else {
			u.Item = r.definition(index)
		}
		encoded, _ := json.Marshal(u)
		if size+len(encoded)+1 > r.welcome.Limits.BatchBytes {
			if len(b.Updates) == 0 {
				return 0, fmt.Errorf("item exceeds batch budget")
			}
			break
		}
		size += len(encoded) + 1
		b.Updates = append(b.Updates, u)
		indices = append(indices, index)
		if initial {
			r.cursor++
			catalog++
			if r.cursor == r.config.Items {
				b.ScanCompleted = true
				break
			}
		} else {
			r.refresh = (r.refresh + 1) % r.config.Items
			if r.refresh == 0 {
				b.ScanCompleted = true
			}
		}
	}
	body, err := json.Marshal(b)
	if err != nil {
		return 0, err
	}
	r.pending = &pendingInventory{body: body, indices: indices, rows: len(b.Updates), catalog: catalog}
	return len(b.Updates), nil
}

func (r *Runner) flush(ctx context.Context) error {
	if r.pending == nil {
		return nil
	}
	var ack protocol.InventoryAck
	if err := r.send(ctx, "/bridge/v1/inventory", r.pending.body, &ack); err != nil {
		return err
	}
	if ack.Ack != r.inventorySeq+1 {
		return fmt.Errorf("invalid inventory confirmation")
	}
	for _, binding := range ack.Bindings {
		if binding.Index < 1 || binding.Index > len(r.pending.indices) || binding.ID < 1 {
			return fmt.Errorf("invalid short-ID binding")
		}
		r.remember(r.pending.indices[binding.Index-1], binding.ID)
	}
	r.inventorySeq = ack.Ack
	r.stats.CatalogSent += r.pending.catalog
	r.stats.UploadedRows += int64(r.pending.rows)
	r.stats.Batches++
	r.pending = nil
	return nil
}
func (r *Runner) remember(index int, id protocol.Quantity) {
	if e := r.cache[index]; e != nil {
		e.Value = cachedID{index, id}
		r.lru.MoveToFront(e)
		return
	}
	if len(r.cache) >= r.config.CacheEntries {
		e := r.lru.Back()
		delete(r.cache, e.Value.(cachedID).index)
		r.lru.Remove(e)
	}
	r.cache[index] = r.lru.PushFront(cachedID{index, id})
}
