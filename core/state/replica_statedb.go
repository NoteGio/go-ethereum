package state

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)


func (s *StateDB) AccessList() *types.AccessList {
	al := make(types.AccessList, len(s.accessList.addresses))
	k := 0
	for address, idx := range s.accessList.addresses {
		al[k] = types.AccessTuple{
			Address: address,
			StorageKeys: make([]common.Hash, len(s.accessList.slots[idx])),
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
