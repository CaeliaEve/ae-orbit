package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestPlayerInvitationsIdentityAndTaskOwnership(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	invitation, code, err := s.CreatePlayerInvitation(ctx, admin.ID, network.ID, true, 3)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.RegisterPlayer(ctx, "Player_A", "a password with spaces", code)
	if err != nil {
		t.Fatal(err)
	}
	if first.Member.Admin || first.Member.GameID != "Player_A" || !first.Member.PasswordLogin {
		t.Fatal(first.Member)
	}
	if _, err = s.RegisterPlayer(ctx, "player_a", "another long password", code); err == nil {
		t.Fatal("case variant reclaimed identity")
	}
	second, err := s.RegisterPlayer(ctx, "Player_B", "b password with spaces", code)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RequireAdmin(ctx, first.Member.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("invited player inherited admin", err)
	}
	if _, _, err = s.CreatePlayerInvitation(ctx, first.Member.ID, network.ID, true, 1); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("player created invitation", err)
	}
	if _, err = s.PlayerLogin(ctx, "PLAYER_A", "incorrect password"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal(err)
	}
	logged, err := s.PlayerLogin(ctx, "player_a", "a password with spaces")
	if err != nil || logged.Member.ID != first.Member.ID {
		t.Fatal(logged, err)
	}
	job, err := s.CreateProductionJob(ctx, first.Member.ID, network.ID, jobInput(item, "player-job-request", "player-job-entry"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Job.Summary.OwnerID != first.Member.ID || job.Job.Summary.OwnerName != "Player_A" {
		t.Fatal(job.Job.Summary)
	}
	control := protocol.ProductionJobControlInput{OperationKey: "player-control-pause", ExpectedControlVersion: 1}
	if _, err = s.ControlProductionJob(ctx, second.Member.ID, network.ID, job.Receipt.JobID, "pause", control); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("other player controlled task", err)
	}
	paused, err := s.ControlProductionJob(ctx, admin.ID, network.ID, job.Receipt.JobID, "pause", control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ControlProductionJob(ctx, admin.ID, network.ID, job.Receipt.JobID, "pause", control); err != nil {
		t.Fatal(err)
	}
	detail, err := s.ProductionJob(ctx, second.Member.ID, network.ID, job.Receipt.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Activity) != 1 || detail.Activity[0].ActorID != admin.ID || detail.Activity[0].ActorName != "admin" || detail.Activity[0].Action != "pause" {
		t.Fatal("actor audit or replay changed", detail.Activity)
	}
	if _, err = s.UpdatePlayerProfile(ctx, first.Member.ID, "Renamed_A", "wrong password", "", false); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal(err)
	}
	updated, err := s.UpdatePlayerProfile(ctx, first.Member.ID, "Renamed_A", "a password with spaces", "new password for a", false)
	if err != nil || updated.ID != first.Member.ID {
		t.Fatal(updated, err)
	}
	if _, err = s.AuthSession(ctx, first.Token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("password change retained old browser", err)
	}
	if _, err = s.PlayerLogin(ctx, "Player_A", "a password with spaces"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err = s.PlayerLogin(ctx, "renamed_a", "new password for a"); err != nil {
		t.Fatal(err)
	}
	detail, err = s.ProductionJob(ctx, first.Member.ID, network.ID, job.Receipt.JobID)
	if err != nil || detail.Summary.OwnerName != "Player_A" || detail.Summary.OwnerID != first.Member.ID {
		t.Fatal("rename changed historical attribution", detail, err)
	}
	_, err = s.ControlProductionJob(ctx, first.Member.ID, network.ID, job.Receipt.JobID, "resume", protocol.ProductionJobControlInput{OperationKey: "player-control-resume", ExpectedControlVersion: paused.ControlVersion})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = s.ProductionJob(ctx, first.Member.ID, network.ID, job.Receipt.JobID)
	if err != nil || len(detail.Activity) != 2 || detail.Activity[1].ActorName != "Renamed_A" {
		t.Fatal(detail.Activity, err)
	}
	order, err := s.CreateOrder(ctx, first.Member.ID, network.ID, protocol.OrderInput{ItemID: item, Quantity: 1, IdempotencyKey: "player-independent-order"})
	if err != nil || order.OwnerName != "Renamed_A" {
		t.Fatal(order, err)
	}
	if _, err = s.CancelOrder(ctx, second.Member.ID, network.ID, order.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("other player canceled order", err)
	}
	if _, err = s.CancelOrder(ctx, first.Member.ID, network.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	invites, err := s.PlayerInvitations(ctx, admin.ID)
	if err != nil || len(invites) != 1 || invites[0].Uses != 2 {
		t.Fatal("duplicate registration consumed invite", invites, err)
	}
	if err = s.RevokePlayerInvitation(ctx, admin.ID, invitation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterPlayer(ctx, "Player_C", "c password with spaces", code); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("revoked invite accepted", err)
	}
}

func TestPlayerRenamingFencesStaleSessionsAndReleasesOldGameID(t *testing.T) {
	s, admin, network, _, _, _, _ := orderFixture(t)
	_, code, err := s.CreatePlayerInvitation(ctx, admin.ID, network.ID, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.RegisterPlayer(ctx, "Player_A", "a long player password", code)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdatePlayerProfile(ctx, first.Member.ID, "Renamed_A", "a long player password", "", false)
	if err != nil {
		t.Fatal(err)
	}
	// A login that verified an old identity before a reset must not create
	// another session after the reset has already retired all sessions.
	if _, err = s.SessionForMember(ctx, first.Member); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("stale authentication recreated a session", err)
	}
	if _, err = s.SessionForMember(ctx, updated); err != nil {
		t.Fatal(err)
	}
	second, err := s.RegisterPlayer(ctx, "Player_A", "b long player password", code)
	if err != nil {
		t.Fatal("old game ID remained tied to legacy member name", err)
	}
	if second.Member.ID == first.Member.ID {
		t.Fatal("renamed member identity was reassigned")
	}
	if _, err = s.PlayerLogin(ctx, "renamed_a", "a long player password"); err != nil {
		t.Fatal(err)
	}
}

func TestPlayerInvitationExpiryQuotaAndReadOnlyGrants(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	invitation, code, err := s.CreatePlayerInvitation(ctx, admin.ID, network.ID, false, 1)
	if err != nil {
		t.Fatal(err)
	}
	player, err := s.RegisterPlayer(ctx, "Viewer_A", "viewer password long", code)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RequireAccess(ctx, player.Member.ID, network.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProductionJob(ctx, player.Member.ID, network.ID, jobInput(item, "viewer-job-request", "viewer-job-entry")); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.RegisterPlayer(ctx, "Viewer_B", "viewer password long", code); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("exhausted invite accepted", err)
	}
	invitation, code, err = s.CreatePlayerInvitation(ctx, admin.ID, network.ID, true, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE player_invitations SET expires_at=$2 WHERE id=$1", invitation.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterPlayer(ctx, "Expired_A", "long password here", code); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	for _, name := range []string{"ab", "bad id", "玩家甲", "toolongplayername123"} {
		if _, err = s.RegisterPlayer(ctx, name, "long password here", code); !errors.Is(err, store.ErrInvalid) {
			t.Fatal(name, err)
		}
	}
	if _, err = s.UpdatePlayerProfile(ctx, player.Member.ID, "Viewer_A", "", "tiny", true); !errors.Is(err, store.ErrInvalid) {
		t.Fatal(err)
	}
	if err = s.RevokeMember(ctx, admin.ID, player.Member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PlayerLogin(ctx, "Viewer_A", "viewer password long"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("revoked member logged in", err)
	}
}
