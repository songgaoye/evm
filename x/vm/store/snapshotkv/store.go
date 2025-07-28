package snapshotkv

import (
	"bytes"
	"fmt"
	"io"

	"cosmossdk.io/store/cachekv"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/evm/x/vm/store/types"
)

type journalEntry struct {
	key        []byte
	prevValue  []byte // nil if deleted
	wasPresent bool
}

type Store struct {
	initialStore storetypes.CacheKVStore
	cache        *cachekv.Store
	journal      []journalEntry // Log of changes for undo
	snapshots    []int          // snapshot[id] = journal len at snapshot time
}

var _ types.SnapshotKVStore = (*Store)(nil)

// NewStore creates a new snapshot KV store with the given base store.
func NewStore(base storetypes.CacheKVStore) *Store {
	return &Store{
		initialStore: base,
		cache:        cachekv.NewStore(base),
		journal:      nil,
		snapshots:    nil,
	}
}

// CurrentStore returns the current active KV store, wrapped to intercept writes for journaling.
func (s *Store) CurrentStore() storetypes.CacheKVStore {
	return &snapshotKVWrapper{Store: s}
}

// snapshotKVWrapper intercepts Set and Delete to journal changes before applying them.
type snapshotKVWrapper struct {
	*Store
}

var _ storetypes.CacheKVStore = (*snapshotKVWrapper)(nil)

// Get retrieves the value for the key from the cache.
func (w *snapshotKVWrapper) Get(key []byte) []byte {
	return w.cache.Get(key)
}

// Has checks if the key exists in the cache.
func (w *snapshotKVWrapper) Has(key []byte) bool {
	return w.cache.Has(key)
}

// Iterator returns an iterator over the key range.
func (w *snapshotKVWrapper) Iterator(start, end []byte) storetypes.Iterator {
	return w.cache.Iterator(start, end)
}

// ReverseIterator returns a reverse iterator over the key range.
func (w *snapshotKVWrapper) ReverseIterator(start, end []byte) storetypes.Iterator {
	return w.cache.ReverseIterator(start, end)
}

// GetStoreType returns the store type.
func (w *snapshotKVWrapper) GetStoreType() storetypes.StoreType {
	return w.cache.GetStoreType()
}

// CacheWrap returns a cache wrap of the store.
func (w *snapshotKVWrapper) CacheWrap() storetypes.CacheWrap {
	return w.cache.CacheWrap()
}

// CacheWrapWithTrace returns a traced cache wrap of the store.
func (w *snapshotKVWrapper) CacheWrapWithTrace(writer io.Writer, tc storetypes.TraceContext) storetypes.CacheWrap {
	return w.cache.CacheWrapWithTrace(writer, tc)
}

// Write flushes changes to the underlying store.
func (w *snapshotKVWrapper) Write() {
	w.cache.Write()
}

// Set sets the key to the given value, journaling the change if necessary.
func (w *snapshotKVWrapper) Set(key []byte, value []byte) {
	prev := w.Get(key)
	wasPresent := prev != nil
	prevValue := ([]byte)(nil)
	if wasPresent {
		prevValue = prev
	}

	// Skip journaling if no change (same value)
	if wasPresent && bytes.Equal(prev, value) {
		return
	}

	w.journal = append(w.journal, journalEntry{
		key:        key,
		prevValue:  prevValue,
		wasPresent: wasPresent,
	})
	w.cache.Set(key, value)
}

// Delete removes the key, journaling the change if necessary.
func (w *snapshotKVWrapper) Delete(key []byte) {
	prev := w.Get(key)
	wasPresent := prev != nil
	if !wasPresent {
		return // Skip journaling for no-op delete on absent key
	}

	w.journal = append(w.journal, journalEntry{
		key:        key,
		prevValue:  prev,
		wasPresent: wasPresent,
	})
	w.cache.Delete(key)
}

// Snapshot creates a new snapshot by recording the current journal length.
func (s *Store) Snapshot() int {
	s.snapshots = append(s.snapshots, len(s.journal))
	return len(s.snapshots) - 1
}

// RevertToSnapshot reverts the state to the given snapshot by undoing journal entries.
func (s *Store) RevertToSnapshot(target int) {
	if target < 0 || target >= len(s.snapshots) {
		panic(fmt.Errorf("snapshot index %d out of bound [%d..%d)", target, 0, len(s.snapshots)))
	}
	targetLen := s.snapshots[target]
	for i := len(s.journal) - 1; i >= targetLen; i-- {
		entry := s.journal[i]
		if entry.wasPresent {
			s.cache.Set(entry.key, entry.prevValue)
		} else {
			s.cache.Delete(entry.key)
		}
	}
	s.journal = s.journal[:targetLen]
	s.snapshots = s.snapshots[:target+1] // Keep snapshots up to the target
}

// Commit flushes all changes to the base store and resets the journal and snapshots.
func (s *Store) Commit() {
	s.cache.Write()
	s.initialStore.Write()
	s.cache = cachekv.NewStore(s.initialStore)
	s.journal = nil
	s.snapshots = nil
}
