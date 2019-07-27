package overlay

import (
  "bytes"
  "github.com/ethereum/go-ethereum/ethdb"
)

type WrappedIterator struct {
  wrapper *OverlayWrapperDB
  overlayIterator ethdb.Iterator
  overlayDone bool
  underlayIterator ethdb.Iterator
  underlayDone bool
  err error
  key []byte
  val []byte
}

func (wi *WrappedIterator) Next() bool {
  if (wi.overlayDone && wi.underlayDone) || wi.err != nil {
    return false
  }
  oKey := wi.overlayIterator.Key()
  for !wi.overlayDone && bytes.HasPrefix(oKey, []byte("deleted/")) {
    if !wi.overlayIterator.Next() {
      wi.overlayDone = true
      wi.err = wi.overlayIterator.Error()
    }
    oKey = wi.overlayIterator.Key()
  }
  uKey := wi.underlayIterator.Key()
  for !wi.underlayDone {
    if isDeleted, _ := wi.wrapper.overlay.Has(deleted(uKey)); !isDeleted {
      break
    }
    if !wi.underlayIterator.Next() {
      wi.underlayDone = true
      wi.err = wi.underlayIterator.Error()
    }
  }
  if !wi.overlayDone {
    if wi.underlayDone || (bytes.Compare(oKey, uKey) < 0) {
      wi.key = oKey
      wi.val = wi.overlayIterator.Value()
      if !wi.overlayIterator.Next() {
        wi.overlayDone = true
        wi.err = wi.overlayIterator.Error()
      }
      return true
    }
  }
  if !wi.underlayDone {
    wi.key = uKey
    wi.val = wi.underlayIterator.Value()
    if !wi.underlayIterator.Next() {
      wi.underlayDone = true
      wi.err = wi.underlayIterator.Error()
    }
    return true
  }
  return false
}
func (wi *WrappedIterator) Error() error {
  return wi.err
}
func (wi *WrappedIterator) Key() []byte {
  return wi.key
}
func (wi *WrappedIterator) Value() []byte {
  return wi.val
}
func (wi *WrappedIterator) Release() {
  wi.underlayIterator.Release()
  wi.overlayIterator.Release()
}
