package api

import (
	"context"
	"sync"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

// FakeEndpointStore is a hand-written in-memory double. No mock library.
type FakeEndpointStore struct {
	mu        sync.Mutex
	endpoints []store.Endpoint

	// ForceErr, when set, makes every method return it. Used to test
	// the 500 path without a real DB.
	ForceErr error
}

func (f *FakeEndpointStore) CreateEndpoint(_ context.Context, e *store.Endpoint) error {
	if f.ForceErr != nil {
		return f.ForceErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints = append(f.endpoints, *e)
	return nil
}

func (f *FakeEndpointStore) ListEndpointsByUser(_ context.Context, userID string) ([]store.Endpoint, error) {
	if f.ForceErr != nil {
		return nil, f.ForceErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Endpoint
	for _, e := range f.endpoints {
		if e.UserID == userID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *FakeEndpointStore) DeleteEndpoint(_ context.Context, userID, id string) error {
	if f.ForceErr != nil {
		return f.ForceErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, e := range f.endpoints {
		if e.ID == id && e.UserID == userID {
			f.endpoints = append(f.endpoints[:i], f.endpoints[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}
