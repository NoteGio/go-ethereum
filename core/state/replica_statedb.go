package state

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)


func (s *StateDB) AccessList() *types.AccessList {
	al := make(types.AccessList, len(s.accessList.addresses))
	k := 0
	for address, idx := range s.accessList.addresses {
		var keyCount int
		if idx == -1 {
			keyCount = 0
		} else {
			keyCount = len(s.accessList.slots[idx])
		}
		al[k] = types.AccessTuple{
			Address: address,
			StorageKeys: make([]common.Hash, keyCount),
		}
		i := 0
		for hash := range s.accessList.slots[idx] {
			al[k].StorageKeys[i] = hash
			i++
		}
		k++
	}
	return &al
}
