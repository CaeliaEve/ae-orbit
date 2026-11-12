package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func jobInput(item protocol.Quantity, request, source string) protocol.ProductionJobInput {
	return protocol.ProductionJobInput{RequestKey: request, Entries: []protocol.ProductionJobEntryInput{{EntryKey: source, ItemID: item, Quantity: 64}}}
}

func TestProductionJobAtomicAcceptanceAndReplay(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	in := jobInput(item, "job-create-001", "source-entry-001")
	accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Replayed || accepted.Job == nil || accepted.Job.Summary.Total != 1 || accepted.Job.Summary.Counts.Pending != 1 || accepted.Job.Summary.Version != 1 || accepted.Job.Summary.ControlVersion != 1 {
		t.Fatalf("bad acceptance: %+v", accepted)
	}
	orders, err := s.Orders(ctx, admin.ID, network.ID, "", 100)
	if err != nil || len(orders) != 0 {
		t.Fatal("acceptance admitted orders", orders, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE networks SET capabilities='{}',current_session='' WHERE id=$1", network.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || !replay.Replayed || replay.Receipt.JobID != accepted.Receipt.JobID {
		t.Fatal("capability change invalidated replay", replay, err)
	}
	in.Entries[0].Quantity++
	if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed creation key accepted", err)
	}
	byKey, err := s.ProductionJobByKey(ctx, admin.ID, network.ID, "", in.RequestKey)
	if err != nil || byKey.Receipt.JobID != accepted.Receipt.JobID {
		t.Fatal(byKey, err)
	}
}

func TestProductionJobOverlapAtomicAndConcurrent(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := jobInput(item, fmt.Sprintf("job-race-%03d", i), "shared-source-001")
			in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("unique-source-%03d", i), ItemID: item, Quantity: 1})
			_, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	ok, overlap := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, store.ErrEntryAlreadyAccepted) {
			var conflict *store.ProductionJobEntryConflictError
			if !errors.As(err, &conflict) || len(conflict.JobIDs) != 1 || conflict.JobIDs[0] == "" {
				t.Fatal("authorized overlap lacked original job identity", err)
			}
			overlap++
		} else {
			t.Fatal(err)
		}
	}
	var jobs, entries, receipts int
	if err := s.Pool().QueryRow(ctx, "SELECT (SELECT count(*) FROM production_jobs),(SELECT count(*) FROM production_job_entries),(SELECT count(*) FROM production_job_entry_receipts)").Scan(&jobs, &entries, &receipts); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || overlap != 1 || jobs != 1 || entries != 2 || receipts != 2 {
		t.Fatal("partial or duplicate acceptance", ok, overlap, jobs, entries, receipts)
	}
}

func TestProductionJobOfflineLargeManifestAndPermissions(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	if _, err := s.Pool().Exec(ctx, "UPDATE networks SET current_session='',last_seen=NULL WHERE id=$1", network.ID); err != nil {
		t.Fatal(err)
	}
	in := jobInput(item, "large-job-001", "unused-source")
	in.Entries = nil
	for i := 0; i < 500; i++ {
		in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("source-%03d", i), ItemID: item, Quantity: 1})
	}
	accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || accepted.Job == nil || len(accepted.Job.Entries) != 500 || accepted.Job.Summary.Counts.Pending != 500 {
		t.Fatal("offline intent not fully durable", accepted, err)
	}
	entryIDs := make(map[string]bool, 500)
	for i, entry := range accepted.Job.Entries {
		if entry.ID == "" || entryIDs[entry.ID] || entry.Ordinal != i || entry.EntryKey != fmt.Sprintf("source-%03d", i) || entry.ItemID != item {
			t.Fatal("equal items lost distinct ordered intent", i, entry)
		}
		entryIDs[entry.ID] = true
	}
	viewer, _, err := s.CreateMember(ctx, admin.ID, "viewer", []protocol.Grant{{NetworkID: network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProductionJob(ctx, viewer.ID, network.ID, jobInput(item, "viewer-job-001", "viewer-source-001")); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer created job", err)
	}
	if _, err = s.ProductionJob(ctx, viewer.ID, network.ID, accepted.Receipt.JobID); err != nil {
		t.Fatal("viewer cannot read job", err)
	}
	if _, err = s.ProductionJobByKey(ctx, viewer.ID, network.ID, admin.ID, in.RequestKey); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer reconciled another owner", err)
	}
	other, _, err := s.CreateNetwork(ctx, admin.ID, "Other", "server-1", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProductionJob(ctx, admin.ID, other.ID, accepted.Receipt.JobID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross-network detail exposed", err)
	}
}

func TestProductionJobInvalidNewLineRollsBack(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	in := jobInput(item, "invalid-job-001", "valid-source-001")
	in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: "invalid-source-001", ItemID: item + 999, Quantity: 1})
	if _, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in); !errors.Is(err, store.ErrCapability) {
		t.Fatal("unknown item accepted", err)
	}
	var jobs, receipts int
	if err := s.Pool().QueryRow(ctx, "SELECT (SELECT count(*) FROM production_jobs),(SELECT count(*) FROM production_job_entry_receipts)").Scan(&jobs, &receipts); err != nil || jobs != 0 || receipts != 0 {
		t.Fatal("partial manifest survived", jobs, receipts, err)
	}
}

func TestProductionJobReceiptsSurviveDetailRemoval(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	in := jobInput(item, "archive-job-001", "archive-source-001")
	accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "DELETE FROM production_jobs WHERE id=$1", accepted.Receipt.JobID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || !replay.Replayed || !replay.Receipt.Archived || replay.Job != nil || replay.Receipt.JobID != accepted.Receipt.JobID {
		t.Fatal("archived intent was recreated", replay, err)
	}
	in.RequestKey = "archive-job-002"
	if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, in); !errors.Is(err, store.ErrEntryAlreadyAccepted) {
		t.Fatal("source tombstone depended on detail", err)
	}
}

func TestProductionJobDetailCeilingDoesNotInvalidateReplay(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	var initialInventoryRevision int64
	if err := s.Pool().QueryRow(ctx, "SELECT revision FROM networks WHERE id=$1", network.ID).Scan(&initialInventoryRevision); err != nil {
		t.Fatal(err)
	}
	in := jobInput(item, "capacity-job-001", "capacity-source-001")
	accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	// Per-owner unsettled jobs are bounded independently of the order queue.
	for i := 1; i < 16; i++ {
		if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, fmt.Sprintf("capacity-job-%03d", i+1), fmt.Sprintf("capacity-source-%03d", i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "capacity-job-999", "capacity-source-999")); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("owner unsettled ceiling ignored", err)
	}
	replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || !replay.Replayed || replay.Receipt.JobID != accepted.Receipt.JobID {
		t.Fatal("capacity invalidated replay", replay, err)
	}
	overlap := in
	overlap.RequestKey = "capacity-overlap-001"
	if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, overlap); !errors.Is(err, store.ErrEntryAlreadyAccepted) {
		t.Fatal("capacity hid already accepted source identity", err)
	}
	var changes, revision, inventoryRevision int64
	if err = s.Pool().QueryRow(ctx, "SELECT (SELECT count(*) FROM production_job_changes WHERE network_id=$1),jobs_revision,revision FROM networks WHERE id=$1", network.ID).Scan(&changes, &revision, &inventoryRevision); err != nil || changes != 16 || revision != 16 || inventoryRevision != initialInventoryRevision {
		t.Fatal("independent summary revisions incorrect", changes, revision, inventoryRevision, err)
	}
}

func TestProductionJobReceiptCeilingsPreserveReplay(t *testing.T) {
	for _, entryCeiling := range []bool{false, true} {
		t.Run(fmt.Sprintf("entries=%v", entryCeiling), func(t *testing.T) {
			s, admin, network, _, _, _, item := orderFixture(t)
			in := jobInput(item, "receipt-job-001", "receipt-source-001")
			accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if entryCeiling {
				for start := 1; start <= 249998; start += 25000 {
					end := start + 24999
					if end > 249998 {
						end = 249998
					}
					_, err = s.Pool().Exec(ctx, `INSERT INTO production_job_entry_receipts(member_id,network_id,entry_key,job_id,entry_id)
						SELECT $1,$2,'seed-source-'||g,'archived-job','seed-entry-'||g FROM generate_series($3::integer,$4::integer) g`, admin.ID, network.ID, start, end)
					if err != nil {
						break
					}
				}
			} else {
				_, err = s.Pool().Exec(ctx, `INSERT INTO production_job_receipts(member_id,network_id,request_key,manifest_digest,job_id)
					SELECT $1,$2,'seed-request-'||g,'digest','archived-job' FROM generate_series(1,16383) g`, admin.ID, network.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			fresh := jobInput(item, "receipt-job-002", "receipt-source-002")
			if entryCeiling {
				fresh.Entries = append(fresh.Entries, protocol.ProductionJobEntryInput{EntryKey: "receipt-source-003", ItemID: item, Quantity: 1})
			}
			if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, fresh); !errors.Is(err, store.ErrCapacity) {
				t.Fatal("receipt ceiling ignored", err)
			}
			replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
			if err != nil || !replay.Replayed || replay.Receipt.JobID != accepted.Receipt.JobID {
				t.Fatal("receipt ceiling invalidated replay", replay, err)
			}
		})
	}
}

func TestProductionJobRequestedIdentitySurvivesAliasChange(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	var alias protocol.Quantity
	if err := s.Pool().QueryRow(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,label,exact)
		SELECT network_id,'alias-fixture',kind,name,damage,label,exact FROM item_definitions WHERE id=$1 RETURNING id`, int64(item)).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "INSERT INTO item_aliases(network_id,alias_id,canonical_id) VALUES($1,$2,$3)", network.ID, int64(alias), int64(item)); err != nil {
		t.Fatal(err)
	}
	in := jobInput(alias, "alias-job-001", "alias-source-001")
	accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || accepted.Job.Entries[0].RequestedItemID != alias || accepted.Job.Entries[0].ItemID != item {
		t.Fatal("requested and canonical identities not retained", accepted, err)
	}
	if _, err = s.Pool().Exec(ctx, "DELETE FROM item_aliases WHERE network_id=$1 AND alias_id=$2", network.ID, int64(alias)); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || !replay.Replayed || replay.Receipt.ManifestDigest != accepted.Receipt.ManifestDigest {
		t.Fatal("alias change invalidated original intent", replay, err)
	}
	in.Entries[0].ItemID = item
	if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("canonical replacement silently changed replay input", err)
	}
}

func TestProductionJobConcurrentExactReplayAndAuthorityEpoch(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	if _, err := s.Pool().Exec(ctx, "INSERT INTO production_job_authority_epochs(member_id,network_id,epoch) VALUES($1,$2,7)", admin.ID, network.ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan protocol.ProductionJobAcceptance, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := jobInput(item, "exact-race-001", "exact-source-001")
			if i == 1 {
				in.TTLSeconds = 3600
			}
			accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
			results <- accepted
			failures <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	created, replayed := 0, 0
	jobID := ""
	for result := range results {
		if jobID != "" && result.Receipt.JobID != jobID {
			t.Fatal("exact replay created duplicate jobs")
		}
		jobID = result.Receipt.JobID
		if result.Replayed {
			replayed++
		} else {
			created++
		}
		if result.Job.Summary.Version != 1 {
			t.Fatal("precommit double increment", result.Job.Summary.Version)
		}
	}
	var epoch, ttl int
	var snapshot protocol.ProductionJobSummary
	var raw []byte
	if err := s.Pool().QueryRow(ctx, "SELECT owner_authority_epoch,ttl_seconds FROM production_jobs WHERE id=$1", jobID).Scan(&epoch, &ttl); err != nil || epoch != 7 || ttl != 3600 || created != 1 || replayed != 1 {
		t.Fatal(epoch, ttl, created, replayed, err)
	}
	if err := s.Pool().QueryRow(ctx, "SELECT summary FROM production_job_changes WHERE network_id=$1 AND revision=1", network.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.ID != jobID || snapshot.Counts.Pending != 1 || snapshot.Version != 1 {
		t.Fatal("historical summary not persisted", snapshot, err)
	}
}

func TestProductionJobRejectsIncompatibleCachedEligibility(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE item_definitions SET exact=false",
		"UPDATE inventory SET craftable=false",
		"UPDATE networks SET capabilities='{}'",
		`UPDATE networks SET capabilities=jsonb_set(capabilities,'{max_craft_quantity}','"1"')`,
	} {
		t.Run(mutation, func(t *testing.T) {
			s, admin, network, _, _, _, item := orderFixture(t)
			if _, err := s.Pool().Exec(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "invalid-cap-001", "invalid-cap-source-001")); !errors.Is(err, store.ErrCapability) {
				t.Fatal("incompatible cached crafting accepted", err)
			}
		})
	}
}

func TestProductionJobNetworkAndDetailCeilings(t *testing.T) {
	for _, detailCeiling := range []bool{false, true} {
		t.Run(fmt.Sprintf("detail=%v", detailCeiling), func(t *testing.T) {
			s, admin, network, _, _, _, item := orderFixture(t)
			in := jobInput(item, "network-limit-001", "network-limit-source-001")
			accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			other, _, err := s.CreateMember(ctx, admin.ID, "other-owner", []protocol.Grant{{NetworkID: network.ID, CanCraft: true}})
			if err != nil {
				t.Fatal(err)
			}
			seed := 63
			if detailCeiling {
				seed = 511
			}
			if _, err = s.Pool().Exec(ctx, `INSERT INTO production_jobs(id,network_id,member_id,manifest_digest,ttl_seconds,owner_authority_epoch,total,settled_at)
				SELECT 'seed-job-'||g,$1,$2,'digest',3600,0,1,CASE WHEN $4 THEN now() ELSE NULL END FROM generate_series(1,$3) g`, network.ID, admin.ID, seed, detailCeiling); err != nil {
				t.Fatal(err)
			}
			if _, err = s.CreateProductionJob(ctx, other.ID, network.ID, jobInput(item, "network-limit-002", "network-limit-source-002")); !errors.Is(err, store.ErrCapacity) {
				t.Fatal("network or retained-detail ceiling ignored", err)
			}
			replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
			if err != nil || replay.Receipt.JobID != accepted.Receipt.JobID || !replay.Replayed {
				t.Fatal(replay, err)
			}
		})
	}
}
