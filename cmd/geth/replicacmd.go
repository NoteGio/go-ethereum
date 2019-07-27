// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	// "fmt"
	"time"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/overlay"
	"github.com/ethereum/go-ethereum/ethdb/devnull"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	replicaModule "github.com/ethereum/go-ethereum/replica"
	"github.com/ethereum/go-ethereum/eth/gasprice"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/dashboard"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/nat"
	whisper "github.com/ethereum/go-ethereum/whisper/whisperv6"
	"github.com/ethereum/go-ethereum/node"
	// "github.com/ethereum/go-ethereum/rpc"
	"gopkg.in/urfave/cli.v1"
)

var (
	replicaCommand = cli.Command{
		Action:    utils.MigrateFlags(replica), // keep track of migration progress
		Name:      "replica",
		Usage:     "Track a Geth node's captured changed data and act as an RPC Replica",
		ArgsUsage: " ",
		Category:  "REPLICA COMMANDS",
		Description: `
The Geth replica captures a Geth node's write operations via a change-data-capture
system and acts as an RPC node based on the replicated data.
`,
		Flags: []cli.Flag{
			utils.TestnetFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.KafkaLogBrokerFlag,
			utils.KafkaLogTopicFlag,
			utils.KafkaTransactionTopicFlag,
			utils.DataDirFlag,
			utils.ReplicaSyncShutdownFlag,
			utils.RPCEnabledFlag,
			utils.RPCPortFlag,
			utils.RPCListenAddrFlag,
			utils.RPCCORSDomainFlag,
			utils.ReplicaStartupMaxAgeFlag,
			utils.ReplicaRuntimeMaxOffsetAgeFlag,
			utils.ReplicaRuntimeMaxBlockAgeFlag,
			utils.OverlayFlag,
			utils.AncientFlag,
		},
	}
	replicaTxPoolConfig = core.TxPoolConfig{
		Journal:   "transactions.rlp",
		Rejournal: time.Hour,

		PriceLimit: 1,
		PriceBump:  10,

		AccountSlots: 1,
		GlobalSlots:  1,
		AccountQueue: 1,
		GlobalQueue:  1,

		Lifetime: 0 * time.Hour,
	}
	ethConfig = eth.Config{
		SyncMode: downloader.LightSync,
		Ethash: ethash.Config{
			CacheDir:       "ethash",
			CachesInMem:    2,
			CachesOnDisk:   3,
			DatasetsInMem:  1,
			DatasetsOnDisk: 2,
		},
		NetworkId:     1,
		LightPeers:    0,
		DatabaseCache: 0,
		TrieDirtyCache:     0,
		TrieTimeout:   5 * time.Minute,
		// GasPrice:      big.NewInt(18 * params.Shannon),

		TxPool: replicaTxPoolConfig,
		GPO: gasprice.Config{
			Blocks:     20,
			Percentile: 60,
		},
	}
	nodeConfig = node.Config{
		DataDir:          node.DefaultDataDir(),
		// HTTPHost:         "0.0.0.0",
		// HTTPPort:         node.DefaultHTTPPort,
		HTTPModules:      []string{"net", "web3", "replica"},
		HTTPVirtualHosts: []string{"*"},
		WSPort:           node.DefaultWSPort,
		WSModules:        []string{"net", "web3"},
		P2P: p2p.Config{
			ListenAddr: ":30303",
			MaxPeers:   0,
			NoDiscovery: true,
			NoDial: true,
			NAT:        nat.Any(),
		},
	}
)
// replica starts replica node
func replica(ctx *cli.Context) error {
	node, _ := makeReplicaNode(ctx)
	utils.StartNode(node)
	node.Wait()
	return nil
}


func makeReplicaNode(ctx *cli.Context) (*node.Node, gethConfig) {
	// Load defaults.
	cfg := gethConfig{
		Eth:       ethConfig,
		Shh:       whisper.DefaultConfig,
		Node:      replicaNodeConfig(),
		Dashboard: dashboard.DefaultConfig,
	}

	// Load config file.
	if file := ctx.GlobalString(configFileFlag.Name); file != "" {
		if err := loadConfig(file, &cfg); err != nil {
			utils.Fatalf("%v", err)
		}
	}

	// Apply flags.
	utils.SetNodeConfig(ctx, &cfg.Node)
	stack, err := node.New(&cfg.Node)
	if err != nil {
		utils.Fatalf("Failed to create the protocol stack: %v", err)
	}
	utils.SetEthConfig(ctx, stack, &cfg.Eth)
	if ctx.GlobalIsSet(utils.EthStatsURLFlag.Name) {
		cfg.Ethstats.URL = ctx.GlobalString(utils.EthStatsURLFlag.Name)
	}

	utils.SetShhConfig(ctx, stack, &cfg.Shh)
	utils.SetDashboardConfig(ctx, &cfg.Dashboard)
	stack.Register(func (sctx *node.ServiceContext) (node.Service, error) {
		chainDb, err := sctx.OpenRawDatabaseWithFreezer("chaindata", cfg.Eth.DatabaseCache, cfg.Eth.DatabaseHandles, cfg.Eth.DatabaseFreezer, "eth/db/chaindata/")
		if err != nil {
			utils.Fatalf("Could not open database: %v", err)
		}
		if cfg.Eth.DatabaseOverlay != "" {
			var overlayDb ethdb.KeyValueStore
			var err error
			if cfg.Eth.DatabaseOverlay == "null" {
				overlayDb = devnull.New()
			} else if cfg.Eth.DatabaseOverlay == "mem" {
				overlayDb = memorydb.New()
			} else {
				overlayDb, err = rawdb.NewLevelDBDatabase(cfg.Eth.DatabaseOverlay, cfg.Eth.DatabaseCache, cfg.Eth.DatabaseHandles, "eth/db/chaindata/overlay/")
			}
			if err != nil {
				utils.Fatalf("Failed to create overlaydb", err)
			}
			chainDb = overlay.NewOverlayWrapperDB(overlayDb, chainDb, chainDb)
		}
	  return replicaModule.NewKafkaReplica(
			chainDb,
			&cfg.Eth,
			sctx,
			[]string{ctx.GlobalString(utils.KafkaLogBrokerFlag.Name)},
			ctx.GlobalString(utils.KafkaLogTopicFlag.Name),
			ctx.GlobalString(utils.KafkaTransactionTopicFlag.Name),
			ctx.GlobalBool(utils.ReplicaSyncShutdownFlag.Name),
			ctx.GlobalInt64(utils.ReplicaStartupMaxAgeFlag.Name),
			ctx.GlobalInt64(utils.ReplicaRuntimeMaxOffsetAgeFlag.Name),
			ctx.GlobalInt64(utils.ReplicaRuntimeMaxBlockAgeFlag.Name),
		)
	})
	return stack, cfg
}

func replicaNodeConfig() node.Config {
	cfg := nodeConfig
	cfg.Name = "geth"
	cfg.Version = params.VersionWithCommit(gitCommit, gitDate)
	cfg.HTTPModules = append(cfg.HTTPModules, "eth", "shh", "net")
	cfg.WSModules = append(cfg.WSModules, "eth", "shh")
	cfg.IPCPath = "geth.ipc"
	return cfg
}
