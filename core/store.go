package core

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mitsuakki/minestrate/core/domain"
	bolt "go.etcd.io/bbolt"
)

var serversBucket = []byte("servers")

// Store persists server state to a bbolt database.
type Store struct {
	db *bolt.DB
}

// OpenStore opens or creates a bbolt database at the given path.
func OpenStore(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	// Create the servers bucket if it doesn't exist.
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(serversBucket)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create bucket: %w", err)
	}

	return &Store{db: db}, nil
}

// SaveServer persists a server to the store. No-op if store is nil.
func (s *Store) SaveServer(srv *domain.Server) error {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(srv)
	if err != nil {
		return fmt.Errorf("marshal server %s: %w", srv.ID, err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(serversBucket)
		return b.Put([]byte(srv.ID), data)
	})
}

// LoadServers loads all servers from the store. Returns empty slice if store is nil.
func (s *Store) LoadServers() ([]*domain.Server, error) {
	if s == nil {
		return nil, nil
	}
	var servers []*domain.Server

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(serversBucket)
		return b.ForEach(func(k, v []byte) error {
			var srv domain.Server
			if err := json.Unmarshal(v, &srv); err != nil {
				slog.Warn("failed to unmarshal persisted server, skipping", "key", string(k), "error", err)
				return nil // skip corrupted entries
			}
			servers = append(servers, &srv)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("load servers: %w", err)
	}

	return servers, nil
}

// DeleteServer removes a server from the store. No-op if store is nil.
func (s *Store) DeleteServer(id string) error {
	if s == nil {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(serversBucket)
		return b.Delete([]byte(id))
	})
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
