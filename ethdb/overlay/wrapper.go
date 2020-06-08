package overlay

import (
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/log"
  "time"
  "strings"
  "sync/atomic"
  // "fmt"
)


// OverlayWrapperDB muxes an overlay database with an underlay database and an
// optional cache database. Write operations go into the overlay database. The
// underlay is treated as read-only. If a cache is specified, reads pulled from
// the underlay will be written to the cache. The cache should be used when
// reads from the underlay are slower than reads from the cache.
type OverlayWrapperDB struct {
  overlay ethdb.KeyValueStore
  cache ethdb.KeyValueStore
  underlay ethdb.KeyValueStore
  overlayHits  uint64
  cacheHits    uint64
  underlayHits uint64
}

func NewOverlayWrapperDB(overlay, underlay ethdb.KeyValueStore) ethdb.KeyValueStore {
  kv := &OverlayWrapperDB{overlay, nil, underlay, 0, 0, 0}
  go func() {
    for _ = range time.NewTicker(60 * time.Second).C {
      log.Info("Overlay statistics", "overlay", atomic.LoadUint64(&kv.overlayHits), "underlay", atomic.LoadUint64(&kv.underlayHits))
      atomic.StoreUint64(&kv.cacheHits, 0)
      atomic.StoreUint64(&kv.underlayHits, 0)
      atomic.StoreUint64(&kv.overlayHits, 0)
    }
  }()
  return kv
}

func NewCachedOverlayWrapperDB(overlay, cache, underlay ethdb.KeyValueStore) ethdb.KeyValueStore {
  kv := &OverlayWrapperDB{overlay, cache, underlay, 0, 0, 0}
  go func() {
    for _ = range time.NewTicker(60 * time.Second).C {
      log.Info("Overlay statistics", "overlay", atomic.LoadUint64(&kv.overlayHits), "underlay", atomic.LoadUint64(&kv.underlayHits), "cache", atomic.LoadUint64(&kv.cacheHits))
      atomic.StoreUint64(&kv.cacheHits, 0)
      atomic.StoreUint64(&kv.underlayHits, 0)
      atomic.StoreUint64(&kv.overlayHits, 0)
    }
  }()
  return kv
}

func deleted(key []byte) ([]byte) {
  return append([]byte("deleted/"), key...)
}

func (wrapper *OverlayWrapperDB) Put(key, value []byte) (error) {
  batch := wrapper.overlay.NewBatch()
  batch.Put(key, value)
  batch.Delete(deleted(key))
  return batch.Write()
}

func (wrapper *OverlayWrapperDB) Delete(key []byte) error {
  batch := wrapper.overlay.NewBatch()
  batch.Put(deleted(key), []byte{})
  batch.Delete(key)
  return batch.Write()
}

func (wrapper *OverlayWrapperDB) Get(key []byte) ([]byte, error) {
  val, err := wrapper.overlay.Get(key)
  if err != nil && strings.HasSuffix(err.Error(), "not found") {
    isDeleted, _ := wrapper.overlay.Has(deleted(key))
    if isDeleted {
      atomic.AddUint64(&wrapper.overlayHits, 1)
      return val, err
    }
    // Not in overlay, not deleted in overlay
    if wrapper.cache != nil {
      val, err := wrapper.cache.Get(key)
      if err == nil || !strings.HasSuffix(err.Error(), "not found") {
        atomic.AddUint64(&wrapper.cacheHits, 1)
        return val, err
      }
    }
    val, err := wrapper.underlay.Get(key)
    if err == nil && wrapper.cache != nil {
      wrapper.cache.Put(key, val)
    }
    atomic.AddUint64(&wrapper.underlayHits, 1)
    return val, err
  }
  atomic.AddUint64(&wrapper.overlayHits, 1)
  return val, err
}

func (wrapper *OverlayWrapperDB) Has(key []byte) (bool, error) {
  val, err := wrapper.overlay.Has(key)
  if !val {
    isDeleted, _ := wrapper.overlay.Has(deleted(key))
    if isDeleted {
      atomic.AddUint64(&wrapper.overlayHits, 1)
      return false, nil
    }
    // Not in overlay, not deleted in overlay
    if wrapper.cache != nil {
      val, err := wrapper.cache.Has(key)
      if val {
        atomic.AddUint64(&wrapper.cacheHits, 1)
        return val, err
      }
    }
    atomic.AddUint64(&wrapper.underlayHits, 1)
    return wrapper.underlay.Has(key)
  }
  atomic.AddUint64(&wrapper.overlayHits, 1)
  return val, err
}

func (wrapper *OverlayWrapperDB) Stat(property string) (string, error) {
  return wrapper.overlay.Stat(property)
}

func (wrapper *OverlayWrapperDB) Compact(start []byte, limit []byte) error {
  return wrapper.overlay.Compact(start, limit)
}

func (wrapper *OverlayWrapperDB) Close() error {
  err1 := wrapper.overlay.Close()
  err2 := wrapper.underlay.Close()
  var err3 error
  if wrapper.cache != nil {
    err3 = wrapper.cache.Close()
  }
  if err1 != nil { return err1 }
  if err2 != nil { return err2 }
  return err3
}
func (wrapper *OverlayWrapperDB) NewBatch() ethdb.Batch {
  return &Batch{wrapper.overlay.NewBatch()}
}
func (wrapper *OverlayWrapperDB) NewIterator(start, end []byte) ethdb.Iterator {
  oiterator := wrapper.overlay.NewIterator(start, end)
  uiterator := wrapper.underlay.NewIterator(start, end)
  oiterator.Next()
  uiterator.Next()
  return &WrappedIterator{
    wrapper,
    oiterator,
    false,
    uiterator,
    false,
    nil,
    []byte{},
    []byte{},
  }
}
