package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"
	"github.com/timoknapp/tennis-tournament-finder/pkg/logger"
	"github.com/timoknapp/tennis-tournament-finder/pkg/models"
)

const (
	// BoltDB bucket name for storing geocoordinates
	GeocordinatesBucket = "geocoordinates"
)

// Store provides an interface for geocoordinates caching operations
type Store interface {
	Get(key string) (models.Geocoordinates, bool, error)
	Set(key string, value models.Geocoordinates) error
	Delete(key string) error
	ForEach(fn func(key string, value models.Geocoordinates) error) error
	GetCacheStatistics() (map[string]int, error)
	Close() error
}

// BoltStore implements Store interface using BoltDB for persistence
type BoltStore struct {
	db *bbolt.DB
}

// NewBoltStore creates a new BoltDB-backed cache store
func NewBoltStore(dbPath string) (*BoltStore, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory %s: %w", dir, err)
	}

	// Open BoltDB database
	db, err := bbolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open BoltDB at %s: %w", dbPath, err)
	}

	// Create the geocoordinates bucket if it doesn't exist
	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(GeocordinatesBucket))
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}

	logger.Info("BoltDB cache store initialized at: %s", dbPath)
	return &BoltStore{db: db}, nil
}

// Get retrieves a geocoordinates entry by key
func (s *BoltStore) Get(key string) (models.Geocoordinates, bool, error) {
	var geo models.Geocoordinates
	var found bool

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(GeocordinatesBucket))
		if bucket == nil {
			return nil // bucket doesn't exist, return empty result
		}

		data := bucket.Get([]byte(key))
		if data == nil {
			return nil // key doesn't exist
		}

		found = true
		return json.Unmarshal(data, &geo)
	})

	if err != nil {
		return models.Geocoordinates{}, false, fmt.Errorf("failed to get key %s: %w", key, err)
	}

	return geo, found, nil
}

// Set stores a geocoordinates entry with the given key
func (s *BoltStore) Set(key string, value models.Geocoordinates) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(GeocordinatesBucket))
		if bucket == nil {
			return fmt.Errorf("bucket %s does not exist", GeocordinatesBucket)
		}

		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal geocoordinates: %w", err)
		}

		return bucket.Put([]byte(key), data)
	})
}

// Delete removes a geocoordinates entry by key
func (s *BoltStore) Delete(key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(GeocordinatesBucket))
		if bucket == nil {
			return nil // bucket doesn't exist, nothing to delete
		}

		return bucket.Delete([]byte(key))
	})
}

// ForEach iterates over all entries in the cache
func (s *BoltStore) ForEach(fn func(key string, value models.Geocoordinates) error) error {
	return s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(GeocordinatesBucket))
		if bucket == nil {
			return nil // bucket doesn't exist, nothing to iterate
		}

		return bucket.ForEach(func(k, v []byte) error {
			var geo models.Geocoordinates
			if err := json.Unmarshal(v, &geo); err != nil {
				// Log the error but continue iteration
				logger.Error("Failed to unmarshal geocoordinates for key %s: %v", string(k), err)
				return nil
			}

			return fn(string(k), geo)
		})
	})
}

// Close closes the BoltDB database
func (s *BoltStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// GetCacheStatistics returns statistics about the BoltDB cache
func (s *BoltStore) GetCacheStatistics() (map[string]int, error) {
	stats := map[string]int{
		"total_entries":      0,
		"successful":         0,
		"failed":             0,
		"pending_retry":      0,
		"permanently_failed": 0,
	}

	err := s.ForEach(func(key string, geo models.Geocoordinates) error {
		stats["total_entries"]++

		if geo.IsFailed {
			stats["failed"]++
			// Note: retry logic is handled in openstreetmap package to avoid circular imports
			if geo.FailCount >= 4 {
				stats["permanently_failed"]++
			}
		} else if geo.Lat != "" && geo.Lon != "" {
			stats["successful"]++
		}

		return nil
	})

	return stats, err
}