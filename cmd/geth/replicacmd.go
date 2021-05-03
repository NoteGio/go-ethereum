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

// import (
// 	_ "net/http/pprof" // TODO: Disable this
// 	"net/http"
// )

import (
	// "fmt"
	"path/filepath"
	"time"
	"strings"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/internal/debug"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/overlay"
	"github.com/ethereum/go-ethereum/ethdb/devnull"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	replicaModule "github.com/ethereum/go-ethereum/replica"
	"github.com/ethereum/go-ethereum/eth/gasprice"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/eth/downloader"
	// "github.com/ethereum/go-ethereum/dashboard"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/nat"
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
		Flags: append(debug.Flags, []cli.Flag{
			utils.HTTPEnabledFlag,
			utils.HTTPListenAddrFlag,
			utils.HTTPPortFlag,
			utils.HTTPCORSDomainFlag,
			utils.HTTPVirtualHostsFlag,
			utils.RopstenFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.KafkaLogBrokerFlag,
			utils.KafkaLogTopicFlag,
			utils.KafkaTransactionTopicFlag,
			utils.KafkaTransactionPoolTopicFlag,
			utils.DataDirFlag,
			utils.ReplicaSyncShutdownFlag,
			utils.LegacyRPCEnabledFlag,
			utils.LegacyRPCPortFlag,
			utils.LegacyRPCListenAddrFlag,
			utils.LegacyRPCCORSDomainFlag,
			utils.WSEnabledFlag,
			utils.WSListenAddrFlag,
			utils.WSPortFlag,
			utils.WSAllowedOriginsFlag,
			// utils.LegacyWSListenAddrFlag,
			// utils.LegacyWSPortFlag,
			// utils.LegacyWSAllowedOriginsFlag,
			utils.GraphQLEnabledFlag,
			utils.GraphQLCORSDomainFlag,
			utils.GraphQLVirtualHostsFlag,
			utils.ReplicaStartupMaxAgeFlag,
			utils.ReplicaRuntimeMaxOffsetAgeFlag,
			utils.ReplicaRuntimeMaxBlockAgeFlag,
			utils.ReplicaEVMConcurrencyFlag,
			utils.ReplicaWarmAddressesFlag,
			utils.OverlayFlag,
			utils.AncientFlag,
			utils.CacheFlag,
			utils.CacheTrieFlag,
			utils.CacheGCFlag,
			utils.CacheDatabaseFlag,
			utils.SnapshotFlag,
		}...),
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
		DatabaseCache:      512,
		TrieCleanCache:     0,
		TrieDirtyCache:     512,
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
	// go func() {
	// 	log.Info("Serving", "err", http.ListenAndServe("0.0.0.0:6060", nil))
	// }()
	debug.Setup(ctx)
	node, _, err := makeReplicaNode(ctx)
	if err != nil { return err }
	defer node.Close()

	utils.StartNode(ctx, node)
	node.Wait()
	return nil
}

func makeReplicaNode(ctx *cli.Context) (*node.Node, ethapi.Backend, error) {
	// Load defaults.
	cfg := gethConfig{
		Eth:       ethConfig,
		Node:      replicaNodeConfig(),
		// Dashboard: dashboard.DefaultConfig,
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

	log.Info("Opening leveldb")
	var chainKv ethdb.KeyValueStore
	log.Info("Allocating DB", "path", stack.ResolvePath("chaindata"), "dbcache", cfg.Eth.DatabaseCache, "handles", cfg.Eth.DatabaseHandles)
	chainKv, err = rawdb.NewLevelDBDatabase(stack.ResolvePath("chaindata"), cfg.Eth.DatabaseCache * 3 / 4, cfg.Eth.DatabaseHandles, "eth/db/chaindata", false)
	// chainKv, err := stack.OpenRawDatabaseWithFreezer("chaindata", cfg.Eth.DatabaseCache, cfg.Eth.DatabaseHandles, cfg.Eth.DatabaseFreezer, "eth/db/chaindata/")
	if err != nil {
		utils.Fatalf("Could not open database: %v", err)
	}
	if cfg.Eth.DatabaseOverlay != "" {
		log.Info("Opening overlay folder", "path", cfg.Eth.DatabaseOverlay)
		var overlayKv ethdb.KeyValueStore
		var err error
		if cfg.Eth.DatabaseOverlay == "null" {
			overlayKv = devnull.New()
		} else if cfg.Eth.DatabaseOverlay == "mem" {
			overlayKv = memorydb.New()
		} else {
			log.Info("Cache size", "dbcache", cfg.Eth.DatabaseCache)
			overlayKv, err = rawdb.NewLevelDBDatabase(cfg.Eth.DatabaseOverlay, cfg.Eth.DatabaseCache * 1 / 4, cfg.Eth.DatabaseHandles, "eth/db/chaindata/overlay/", false)
		}
		if err != nil {
			utils.Fatalf("Failed to create overlaydb", err)
		}
		log.Info("Constructing Overlay")
		chainKv = overlay.NewOverlayWrapperDB(overlayKv, chainKv)
	}
	root := stack.ResolvePath("chaindata")
	freezer := cfg.Eth.DatabaseFreezer
	switch {
	case freezer == "":
		freezer = filepath.Join(root, "ancient")
	case strings.HasPrefix(freezer, "s3://"):
		log.Info("S3 freezer", "path", freezer)
	case strings.HasPrefix(freezer, "s3:/"):
		// For some reason the flags system is dropping the second slash
		freezer = "s3://" + strings.TrimPrefix(freezer, "s3:/")
	case !filepath.IsAbs(freezer):
		log.Info("Non-s3 path", "path", freezer)
		freezer = stack.ResolvePath(freezer)
	}
	chainDb, err := rawdb.NewDatabaseWithFreezer(chainKv, freezer, "eth/db/chaindata", false)
	if err != nil {
		utils.Fatalf("Could not open freezer: %v", err)
	}
  replica, err := replicaModule.NewKafkaReplica(
		chainDb,
		&cfg.Eth,
		stack,
		ctx.GlobalString(utils.KafkaLogBrokerFlag.Name),
		ctx.GlobalString(utils.KafkaLogTopicFlag.Name),
		ctx.GlobalString(utils.KafkaTransactionTopicFlag.Name),
		ctx.GlobalString(utils.KafkaTransactionPoolTopicFlag.Name),
		ctx.GlobalBool(utils.ReplicaSyncShutdownFlag.Name),
		ctx.GlobalInt64(utils.ReplicaStartupMaxAgeFlag.Name),
		ctx.GlobalInt64(utils.ReplicaRuntimeMaxOffsetAgeFlag.Name),
		ctx.GlobalInt64(utils.ReplicaRuntimeMaxBlockAgeFlag.Name),
		cfg.Node.HTTPTimeouts,
		int(ctx.GlobalInt64(utils.ReplicaEVMConcurrencyFlag.Name)),
		ctx.GlobalString(utils.ReplicaWarmAddressesFlag.Name),
		ctx.GlobalBool(utils.SnapshotFlag.Name),
		ctx.GlobalInt64(utils.ReplicaMaxOffsetFlag.Name),
	)
	if err != nil { return stack, nil, err }
	if ctx.GlobalBool(utils.GraphQLEnabledFlag.Name) {
		utils.RegisterGraphQLService(stack, replica.GetBackend(), cfg.Node)
	}
	stack.RegisterAPIs(replica.APIs())
	stack.RegisterLifecycle(replica)
	return stack, replica.GetBackend(), nil
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
