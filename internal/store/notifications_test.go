package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestNotificationWaitsForCommitAndSuppressesCommitFailure(t *testing.T) {
	s, admin, network, token := fixture(t)
	d, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	wake, unsubscribe := s.SubscribeEvents(network.ID)
	defer unsubscribe()
	holder, err := s.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	key := holder.Conn().PgConn().PID()
	if _, err = holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(key)); err != nil {
		t.Fatal(err)
	}
	function := fmt.Sprintf(`CREATE FUNCTION hold_event_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END $$`, key)
	if _, err = s.Pool().Exec(ctx, function); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "CREATE CONSTRAINT TRIGGER hold_event_commit AFTER INSERT ON network_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION hold_event_commit()"); err != nil {
		t.Fatal(err)
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Hello(work, d, protocol.Hello{Protocol: 1, BootID: "commit-gate", Source: "simulator", MaxResponseBytes: 32768})
		done <- err
	}()
	for {
		var waiting bool
		if err = s.Pool().QueryRow(work, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)", key).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-wake:
		t.Fatal("notification escaped before the database commit")
	default:
	}
	if err = holder.Commit(work); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("successful commit did not wake subscriber")
	}
	page, err := s.Events(ctx, admin.ID, network.ID, 0)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("notified event was not durable: %+v %v", page, err)
	}
	if _, err = s.Pool().Exec(ctx, "CREATE OR REPLACE FUNCTION hold_event_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced commit failure'; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: "failed-commit", Source: "simulator", MaxResponseBytes: 32768}); err == nil {
		t.Fatal("test failed to reject the commit")
	}
	select {
	case <-wake:
		t.Fatal("rolled back transaction notified a subscriber")
	default:
	}
	next, err := s.Events(ctx, admin.ID, network.ID, page.LastID)
	if err != nil || len(next.Events) != 0 || next.LastID != page.LastID {
		t.Fatalf("failed transaction changed durable events: %+v %v", next, err)
	}
}

func TestNotificationsCoalesceIsolateAndReleaseSubscribers(t *testing.T) {
	s, admin, network, token := fixture(t)
	a, stopA := s.SubscribeEvents(network.ID)
	b, stopB := s.SubscribeEvents(network.ID)
	other, stopOther := s.SubscribeEvents("other-network")
	defer stopA()
	defer stopB()
	defer stopOther()
	hello(t, s, token, "coalesce-1")
	hello(t, s, token, "coalesce-2")
	for _, wake := range []<-chan struct{}{a, b} {
		select {
		case <-wake:
		default:
			t.Fatal("subscriber was not woken")
		}
		select {
		case <-wake:
			t.Fatal("duplicate hints were not coalesced")
		default:
		}
	}
	select {
	case <-other:
		t.Fatal("notification crossed network boundary")
	default:
	}
	page, err := s.Events(ctx, admin.ID, network.ID, 0)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("coalescing discarded durable events: %+v %v", page, err)
	}
	stopA()
	stopA()
	stopB()
	stopOther()
	if stats := s.NotificationStats(); stats.Networks != 0 || stats.Subscribers != 0 || stats.Coalesced != 2 {
		t.Fatalf("subscriptions leaked or were not coalesced: %+v", stats)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				_, stop := s.SubscribeEvents(network.ID)
				stop()
			}
		}()
	}
	for i := 0; i < 5; i++ {
		hello(t, s, token, fmt.Sprintf("concurrent-%d", i))
	}
	workers.Wait()
	if stats := s.NotificationStats(); stats.Subscribers != 0 || stats.Networks != 0 {
		t.Fatalf("concurrent subscriptions leaked: %+v", stats)
	}
}
