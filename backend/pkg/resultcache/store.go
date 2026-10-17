package resultcache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.etcd.io/bbolt"
)

// resultsBucket holds tournament results. It is separate from the
// geocoordinates bucket so the two caches can be reasoned about and cleared
// independently.
const resultsBucket = "tournament_results"

// BoltStore persists cache entries in BoltDB.
type BoltStore struct {
	db *bbolt.DB
}

// NewBoltStore opens (or creates) the cache database at dbPath.
func NewBoltStore(dbPath string) (*BoltStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	db, err := bbolt.Open(dbPath, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open result cache at %s: %w", dbPath, err)
	}

	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(resultsBucket))
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create result bucket: %w", err)
	}

	return &BoltStore{db: db}, nil
}

func (s *BoltStore) Get(key string) (Entry, bool, error) {
	var entry Entry
	var found bool

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(resultsBucket))
		if bucket == nil {
			return nil
		}
		data := bucket.Get([]byte(key))
		if data == nil {
			return nil
		}

		decoded, err := decode(data)
		if err != nil {
			// Treat unreadable data as a miss rather than failing the request;
			// it will be overwritten by the next refresh.
			return nil
		}
		entry, found = decoded, true
		return nil
	})

	return entry, found, err
}

func (s *BoltStore) Set(key string, entry Entry) error {
	data, err := encode(entry)
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(resultsBucket))
		if bucket == nil {
			return fmt.Errorf("bucket %s does not exist", resultsBucket)
		}
		return bucket.Put([]byte(key), data)
	})
}

func (s *BoltStore) Delete(key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(resultsBucket))
		if bucket == nil {
			return nil
		}
		return bucket.Delete([]byte(key))
	})
}

func (s *BoltStore) ForEach(fn func(key string, entry Entry) error) error {
	return s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(resultsBucket))
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(k, v []byte) error {
			entry, err := decode(v)
			if err != nil {
				return nil // skip corrupt entries
			}
			return fn(string(k), entry)
		})
	})
}

func (s *BoltStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// MemoryStore keeps entries in memory. It is used by tests and as a fallback
// when the persistent store cannot be opened, so caching still works (just
// without surviving a restart).
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]Entry)}
}

func (s *MemoryStore) Get(key string) (Entry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[key]
	return entry, ok, nil
}

func (s *MemoryStore) Set(key string, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = entry
	return nil
}

func (s *MemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

func (s *MemoryStore) ForEach(fn func(key string, entry Entry) error) error {
	s.mu.RLock()
	snapshot := make(map[string]Entry, len(s.entries))
	for k, v := range s.entries {
		snapshot[k] = v
	}
	s.mu.RUnlock()

	for k, v := range snapshot {
		if err := fn(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStore) Close() error { return nil }
