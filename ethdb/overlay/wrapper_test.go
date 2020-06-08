package overlay

import (
	"strings"
	"bytes"
	"testing"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	// "fmt"
)

func GetWrapperNoCache() (ethdb.KeyValueStore, ethdb.KeyValueStore, ethdb.KeyValueStore) {
	overlay := rawdb.NewMemoryDatabase()
	underlay := rawdb.NewMemoryDatabase()
	wrapper := &OverlayWrapperDB{overlay: overlay, underlay: underlay}
	return wrapper, overlay, underlay
}
func GetWrapperWithCache() (ethdb.KeyValueStore, ethdb.KeyValueStore, ethdb.KeyValueStore, ethdb.KeyValueStore) {
	overlay := rawdb.NewMemoryDatabase()
	cache := rawdb.NewMemoryDatabase()
	underlay := rawdb.NewMemoryDatabase()
	wrapper := &OverlayWrapperDB{overlay: overlay, cache: cache, underlay: underlay}
	return wrapper, overlay, cache, underlay
}

func TestPut(t *testing.T) {
	wrapper, overlay, underlay := GetWrapperNoCache()
	defer wrapper.Close()
	wrapper.Put([]byte("hello"), []byte("world"))
	val, err := overlay.Get([]byte("hello"))
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !bytes.Equal(val, []byte("world")){
		t.Errorf("Unexpected value: %v", val)
	}
	if ok, _ := underlay.Has([]byte("hello")); ok {
		t.Errorf("Underlay should not have key")
	}
}

func TestDelete(t *testing.T) {
	wrapper, _, underlay := GetWrapperNoCache()
	defer wrapper.Close()
	underlay.Put([]byte("hello"), []byte("world"))
	wrapper.Delete([]byte("hello"))
	ok, _ := wrapper.Has([]byte("hello"))
	if ok {
		t.Errorf("Key should be gone from wrapper")
	}
	_, err := wrapper.Get([]byte("hello"))
	if !strings.HasSuffix(err.Error(), "not found") {
		t.Errorf("Expected key not found")
	}
}

func TestGetInOverlay(t *testing.T) {
	wrapper, overlay, underlay := GetWrapperNoCache()
	defer wrapper.Close()
	overlay.Put([]byte("hello"), []byte("world"))
	if ok, _ := wrapper.Has([]byte("hello")); !ok {
		t.Fatalf("Expected key")
	}
	val, err := wrapper.Get([]byte("hello"))
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !bytes.Equal(val, []byte("world")){
		t.Errorf("Unexpected value: %v", val)
	}
	if ok, _ := underlay.Has([]byte("hello")); ok {
		t.Errorf("Underlay should not have key")
	}
}

func TestGetInUnderlay(t *testing.T) {
	wrapper, overlay, underlay := GetWrapperNoCache()
	defer wrapper.Close()
	underlay.Put([]byte("hello"), []byte("world"))
	val, err := wrapper.Get([]byte("hello"))
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !bytes.Equal(val, []byte("world")){
		t.Errorf("Unexpected value: %v", val)
	}
	if ok, _ := overlay.Has([]byte("hello")); ok {
		t.Errorf("Overlay should not have key")
	}
}

func TestGetWithCache(t *testing.T) {
	wrapper, _, cache, underlay := GetWrapperWithCache()
	defer wrapper.Close()
	underlay.Put([]byte("hello"), []byte("world"))
	if ok, _ := cache.Has([]byte("hello")); ok {
		t.Errorf("Cache should not have key yet")
	}
	if ok, _ := wrapper.Has([]byte("hello")); !ok {
		t.Fatalf("Expected key")
	}
	val, err := wrapper.Get([]byte("hello"))
	if err != nil {
		t.Fatalf(err.Error())
	}
	if !bytes.Equal(val, []byte("world")) {
		t.Errorf("Unexpected value: '%v'", string(val))
	}
	if ok, _ := cache.Has([]byte("hello")); !ok {
		t.Errorf("Cache should have key")
	}
	if ok, _ := wrapper.Has([]byte("hello")); !ok {
		t.Errorf("Cache should have key")
	}
	if val, _ := wrapper.Get([]byte("hello")); !bytes.Equal(val, []byte("world")) {
		t.Errorf("Unexpected value: '%v'", string(val))
	}
}

func TestIterator(t *testing.T) {
	wrapper, overlay, underlay := GetWrapperNoCache()
	defer wrapper.Close()
	overlay.Put([]byte("a"), []byte("A"))
	underlay.Put([]byte("b"), []byte("B"))
	overlay.Put([]byte("c"), []byte("C"))
	underlay.Put([]byte("d"), []byte("D"))
	underlay.Put([]byte("e"), []byte("E"))
	wrapper.Delete([]byte("e"))

	wrapper2, overlay, underlay := GetWrapperNoCache()
	overlay.Put([]byte("a"), []byte("A"))
	underlay.Put([]byte("b"), []byte("B"))
	overlay.Put([]byte("c"), []byte("C"))
	overlay.Put([]byte("d"), []byte("D"))
	underlay.Put([]byte("e"), []byte("E"))
	wrapper2.Delete([]byte("e"))

	for _, db := range([]ethdb.KeyValueStore{wrapper, wrapper2}) {
		defer wrapper.Close()
		iter := db.NewIterator()

		if !iter.Next() {
			t.Fatalf("Iterator terminated unexpectedly")
		}
		if !bytes.Equal(iter.Key(), []byte("a")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !bytes.Equal(iter.Value(), []byte("A")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !iter.Next() {
			t.Fatalf("Iterator terminated unexpectedly")
		}
		if !bytes.Equal(iter.Key(), []byte("b")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !bytes.Equal(iter.Value(), []byte("B")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !iter.Next() {
			t.Fatalf("Iterator terminated unexpectedly")
		}
		if !bytes.Equal(iter.Key(), []byte("c")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !bytes.Equal(iter.Value(), []byte("C")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !iter.Next() {
			t.Fatalf("Iterator terminated unexpectedly")
		}
		if !bytes.Equal(iter.Key(), []byte("d")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if !bytes.Equal(iter.Value(), []byte("D")){
			t.Errorf("Unexpected key '%v'", string(iter.Key()))
		}
		if iter.Next() {
			t.Errorf("Expected iterator to be exhausted")
		}
		iter.Release()
	}
}

func TestBatch(t *testing.T) {
	wrapper, _, underlay := GetWrapperNoCache()
	defer wrapper.Close()
	underlay.Put([]byte("A"), []byte("a"))
	underlay.Put([]byte("B"), []byte("b"))
	wrapper.Delete([]byte("B"))
	underlay.Put([]byte("C"), []byte("c"))
	batch := wrapper.NewBatch()
	batch.Delete([]byte("C"))
	batch.Put([]byte("B"), []byte("x"))
	batch.Write()
	if val, _ := wrapper.Get([]byte("A")); !bytes.Equal(val, []byte("a")) {
		t.Errorf("Unexpected value '%v'", string(val))
	}
	if val, _ := wrapper.Get([]byte("B")); !bytes.Equal(val, []byte("x")) {
		t.Errorf("Unexpected value '%v'", string(val))
	}
	if ok, _ := wrapper.Has([]byte("C")); ok {
		t.Errorf("Expected key to be deleted")
	}
}
