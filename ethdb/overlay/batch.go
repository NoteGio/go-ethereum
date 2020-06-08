package overlay

import (
	"github.com/ethereum/go-ethereum/ethdb"
)

type Batch struct {
	batch ethdb.Batch
}

func (b *Batch) ValueSize() int {
	return b.batch.ValueSize()
}
func (b *Batch) Write() error {
	return b.batch.Write()
}
func (b *Batch) Reset() {
	b.batch.Reset()
}
func (b *Batch) Replay(w ethdb.KeyValueWriter) error {
	return b.batch.Replay(w)
}
func (b *Batch) Put(key []byte, value []byte) error {
	err := b.batch.Put(key, value)
	if err != nil {
		return err
	}
	return b.batch.Delete(deleted(key))
}
func (b *Batch) Delete(key []byte) error {
	err := b.batch.Delete(key)
	if err != nil {
		return err
	}
	return b.batch.Put(deleted(key), []byte{})
}
