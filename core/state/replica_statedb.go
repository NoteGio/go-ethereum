	package state

	import (
		"github.com/ethereum/go-ethereum/common"
		"github.com/ethereum/go-ethereum/log"
		"github.com/ethereum/go-ethereum/core/types"
		"github.com/ethereum/go-ethereum/core/vm"
	)

var precompiles map[common.Address]struct{}


func (s *StateDB) AccessList() *types.AccessList {
	if precompiles == nil {
		precompiles = make(map[common.Address]struct{})
		for _, address := range vm.PrecompiledAddressesBerlin {
			precompiles[address] = struct{}{}
		}
	}
	al := types.AccessList{}
	log.Info("Calculating access list")
	for address, idx := range s.accessList.addresses {
		if _, ok := precompiles[address]; ok { continue }
		log.Info("Adding entries", "address", address, "idx", idx)
		if idx == -1 {
			// al = append(al, types.AccessTuple{
			// 	Address: address,
			// 	StorageKeys: []common.Hash{},
			// })
			continue
		}
		k := len(al)
		al = append(al, types.AccessTuple{
			Address: address,
			StorageKeys: make([]common.Hash, len(s.accessList.slots[idx])),
		})
		i := 0
		for hash := range s.accessList.slots[idx] {
			al[k].StorageKeys[i] = hash
			i++
		}
	}
	return &al
}
