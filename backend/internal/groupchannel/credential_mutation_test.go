package groupchannel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

type credentialMutations struct {
	Repository
	data Data
	fail error
}

func (r *credentialMutations) Snapshot(context.Context) (Data, error) { return r.data, nil }
func (r *credentialMutations) Get(_ context.Context, id string) (Record, error) {
	for _, v := range r.data.Channels {
		if v.ID == id {
			return v, nil
		}
	}
	return Record{}, ErrNotFound
}
func (r *credentialMutations) Update(_ context.Context, m Mutation) error {
	if r.fail != nil {
		return r.fail
	}
	for i, v := range r.data.Channels {
		if v.ID == m.Record.ID {
			if v.Revision != m.ExpectedRevision {
				return ErrConflict
			}
			next := m.Record
			next.Revision++
			next.UpdatedAt = time.Now()
			r.data.Channels[i] = next
			return nil
		}
	}
	return ErrNotFound
}
func (r *credentialMutations) Delete(_ context.Context, id string, revision int64) error {
	if r.fail != nil {
		return r.fail
	}
	for i, v := range r.data.Channels {
		if v.ID == id {
			if v.Revision != revision {
				return ErrConflict
			}
			r.data.Channels = append(r.data.Channels[:i], r.data.Channels[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func TestGroupCredentialMutationsForgetOnlyCommittedReplacementAndDeletion(t *testing.T) {
	ctx := context.Background()
	directory, data := fixture()
	ring, err := channel.ParseKeyring("synthetic=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	row := &data.Channels[0]
	sealed, err := ring.Encrypt(row.ID, 1, "synthetic-original-key")
	if err != nil {
		t.Fatal(err)
	}
	row.Credential = sealed
	repo := &credentialMutations{data: data}
	runtime := &Runtime{Repository: repo, Removed: ring.ForgetCredential}
	runtime.Rebuild = func(ctx context.Context) error { _, err := runtime.Wrap(cacheBase{}).LoadCandidates(ctx); return err }
	if err := runtime.Install(ctx, directory); err != nil {
		t.Fatal(err)
	}
	service := &Service{Runtime: runtime, Repository: repo, Keyring: ring}
	owner := Actor{ID: "owner"}
	openOriginal := func() {
		t.Helper()
		if raw, err := ring.OpenCredential("channel", sealed); err != nil || raw != "synthetic-original-key" {
			t.Fatal(err)
		}
	}
	openOriginal()
	count := ring.CredentialDecryptions()
	name, key := "renamed", "synthetic-replacement-key"
	if _, err := service.Update(ctx, owner, "channel", UpdateInput{Revision: 1, Name: &name}); err != nil {
		t.Fatal(err)
	}
	openOriginal()
	if ring.CredentialDecryptions() != count {
		t.Fatal("renaming evicted an unchanged credential")
	}
	repo.fail = errors.New("synthetic transaction rejection")
	if _, err := service.Update(ctx, owner, "channel", UpdateInput{Revision: 2, APIKey: &key}); err == nil {
		t.Fatal("failed credential write accepted")
	}
	if err := service.Delete(ctx, owner, "channel", 2); err == nil {
		t.Fatal("failed deletion accepted")
	}
	repo.fail = nil
	if err := service.Delete(ctx, Actor{ID: "foreign"}, "channel", 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign deletion accepted", err)
	}
	openOriginal()
	if ring.CredentialDecryptions() != count {
		t.Fatal("failed or foreign mutation evicted a credential")
	}
	if _, err := service.Update(ctx, owner, "channel", UpdateInput{Revision: 2, APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	openOriginal()
	if ring.CredentialDecryptions() != count+1 {
		t.Fatal("committed rotation retained old plaintext")
	}
	if err := service.Delete(ctx, owner, "channel", 3); err != nil {
		t.Fatal(err)
	}
	openOriginal()
	if ring.CredentialDecryptions() != count+2 {
		t.Fatal("committed deletion retained old plaintext")
	}
	state, err := runtime.Current()
	if err != nil || len(state.Candidates) != 0 {
		t.Fatal("deleted channel remained routable", err)
	}
}
