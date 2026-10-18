package apikey

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type retainedKeyStore struct {
	Store
	key     Key
	updates int
	last    Update
}

func (s *retainedKeyStore) Get(_ context.Context, owner, id string) (Key, error) {
	if owner != s.key.OwnerID || id != s.key.ID {
		return Key{}, ErrNotFound
	}
	return s.key, nil
}
func (s *retainedKeyStore) Update(ctx context.Context, owner, id string, u Update) (Key, error) {
	key, err := s.Get(ctx, owner, id)
	if err != nil {
		return Key{}, err
	}
	s.updates++
	s.last = u
	return key, nil
}

func TestKeyEditsRetainExistingRetiredIDsWithoutApprovingNewOnes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
		aliases map[string]string
		want    bool
	}{
		{"retain restriction while adding approved ID", []string{"retired/model", "current/model"}, nil, true},
		{"retain exact alias while adding approved target", nil, map[string]string{"old": "retired/model", "new": "current/model"}, true},
		{"drop retired restriction", []string{"current/model"}, nil, true},
		{"new restriction still needs approval", []string{"retired/model", "unknown/model"}, nil, false},
		{"retired alias target cannot seed a new alias", nil, map[string]string{"new": "retired/model"}, false},
		{"retargeting an old alias is a new target", nil, map[string]string{"old": "unknown/model"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &retainedKeyStore{key: Key{ID: "key", OwnerID: "owner", AllowedModels: []string{"retired/model"}, ModelAliases: map[string]string{"old": "retired/model"}}}
			service := NewService(store, nil, func(context.Context) ([]string, error) { return []string{"current/model"}, nil })
			u := Update{}
			if tc.allowed != nil {
				u.AllowedModels = &tc.allowed
			}
			if tc.aliases != nil {
				u.ModelAliases = &tc.aliases
			}
			_, err := service.Update(context.Background(), "owner", "key", u)
			if tc.want {
				if err != nil || store.updates != 1 {
					t.Fatal("retained restriction blocked", err)
				}
				if tc.allowed != nil && !slices.Contains(*store.last.AllowedModels, "current/model") {
					t.Fatal("approved ID lost")
				}
			} else if !errors.Is(err, ErrInvalidInput) || store.updates != 0 {
				t.Fatal("unknown ID accepted", err)
			}
		})
	}
}
func TestRetiredKeyEntriesCannotBeBorrowedFromAnotherOwnerOrNewKey(t *testing.T) {
	store := &retainedKeyStore{key: Key{ID: "key", OwnerID: "owner", AllowedModels: []string{"retired/model"}}}
	service := NewService(store, nil, func(context.Context) ([]string, error) { return []string{}, nil })
	allowed := []string{"retired/model"}
	if _, err := service.Update(context.Background(), "other", "key", Update{AllowedModels: &allowed}); !errors.Is(err, ErrNotFound) || store.updates != 0 {
		t.Fatal("another owner's retained IDs borrowed", err)
	}
	if err := service.validateSettings(context.Background(), &Settings{Status: StatusEnabled, AllowedModels: allowed}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("new key inherited an unknown restriction", err)
	}
}
