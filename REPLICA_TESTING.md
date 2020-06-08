## Setting up a Replica Cluster

### Building

```
make geth-cross
```

Grab the version for your platform from `./build/bin/` - We'll refer to it as
`geth` from here forward.


### Initial Sync

You'll need to start with a synced chain. If you're just trying to test this
project, we recommend Goerli, as it's new and syncs quickly.

```
geth --goerli
```

Once it's synced, you'll need to snapshot your `~/.ethereum` directory.

## Kafka Setup

Streaming Replication relies on Kafka. The configuration of Kafka for the
purposes of a production environment is outside the scope of this document, but
if you're just looking to get up and running quickly, take a look here:

https://hub.docker.com/r/wurstmeister/kafka

### Master Setup

The system requirements for a master are somewhat higher than a typical Geth
server. Streaming replication requires writing to disk more often, which
necessitates faster disks to keep the server in sync.

After snapshotting your `~/.ethereum` directory:

```
./geth --goerli --gcmode=archive --kafka.broker=kafka:9092 --kafka.topic=goerli
```

The `--gcmode=archive` flag tells it to flush to disk after every block. Without
that, the master will operate with an in-memory cache of the state trie, and it
won't be available to replicas.

The `--kafka.topic=goerli` flag designates a topic in Kafka. This will default
to `"geth"`, so if you're only running one replica cluster against your Kafka
cluster, you don't need to specify this flag. If you're running different
testnets, or multiple instances of the same network (which you might want to do
for a variety of reasons), you can specify different topics.

The `--kafka.broker=kafka:9092` flag tells the replica what broker to connect
to. The Kafka client will establish connections to multiple brokers from your
Kafka cluster after the initial connection.

#### Transaction Relay

If your cluster needs to broadcast transactions to the network, rather than just
providing access to state data, you'll need to run the transaction relay on the
master as well:

```
./geth-tx txrelay --kafka.broker=kafka:9092 --kafka.tx.topic=goerli-tx --kafka.tx.consumergroup=goerli-tx ~/.ethereum/goerli/geth.ipc
```

The `--kafka.broker=kafka:9092` flag is thes same as above.

The `--kafka.tx.topic=goerli-tx` designates the topic through which replicas
will send transactions back to the master. This defaults to `geth-tx`, so like
with the `--kafka.topic` flag above, if you're only running one replica cluster
on your kafka cluster you can omit this.

The `--kafka.tx.consumergroup=goerli-tx` flag designates the consumer group for
Kafka to track which transactions have been processed. This defaults to
`geth-tx`, and can be omitted if you're only running one replica cluster on your
kafka cluster.

The `~/.ethereum/goerli/geth.ipc` argument tells the transaction relay where to
send transactions. This can be a local IPC endpoint or an HTTP(S) RPC endpoint.
It defaults to `~/.ethereum/geth.ipc`, so if you're running a mainnet node in
its default configuration, this argument can be omitted.

### Replica setup

The system requirements for a replica are small compared to the master. The
biggest consideration for replicas is disk - the disks don't need to be fast,
but they need to be big enough to hold the whole chain. As far as CPU and RAM, a
single CPU and 1 GB of RAM is sufficient.

With a separate copy of the `~/.ethereum` directory you snapshotted earlier,
we'll spin up a replica:

```
./geth replica --goerli --kafka.broker=kafka:9092 --kafka.topic=goerli --kafka.tx.topic=goerli-tx
```

These flags should look familiar:

`--kafka.broker=kafka:9092` should point to the same broker as the master (or at
least a broker in the same Kafka cluster)

`--kafka.topic=goerli` indicates the topic that the master is sending write
operations to. The replica nodes will track Kafka offsets in its local database,
so if it gets restarted it can resume where it left off. We don't use consumer
groups for replicas, because then we would risk the local database getting out
of sync with Kafka's record of what write operations have been processed.

`--kafka.tx.topic=goerli-tx` indicates the Kafka topic for sending transactions.
This will be picked up by the `txrelay` service to be processed. This defaults
to `geth-tx` and can be omitted if you're only running one replica cluster on
your kafka cluster.

When the replica starts up, it will start processing messages from Kafka between
the last record in its local database and the latest message in Kafka. Once it
has caught up, it will start serving RPC requests through both IPC and on HTTP
port 8545.


### Known Issues

When replicas run behind a load balancer, event log subscriptions are
unreliable. If you create an event subscription with one replica, and subsequent
requests go to a separate replica, the new replica will be unable to serve your
request because it doesn't know about the subscription. If your application is
written in JavaScript, you can use [Web3 Provider Engine's](https://github.com/MetaMask/provider-engine)
`Filter Provider` to simulate event subscriptions with a load balanced backend.
Other languages can also simulate the behavior, but not quite as easily. We may
eventually develop a way to support event subscriptions with load balanced
replicas, but with the ease of work-arounds its not currently a high priority.

## How You Can Help

If you have a dApp you'd be open to testing against Ether Cattle replicas, we
have a couple of options.

First, we are hosting a public Goerli RPC server at
https://goerli-rpc.openrelay.xyz/ &mdash; If your dApp runs on Goerli, we'd
encourage you to point at our RPC server and check that everything works as
expected. Also, if you run your dApp on Goerli, we're trying to build a list of
dApps that support Goerli, and would appreciate a pull request at
github.com/openrelayxyz/goerli-dapp-list

To get Goerli working with Metamask, use
[these instructions](https://mudit.blog/getting-started-goerli-testnet/), with
`https://goerli-rpc.openrelay.xyz` as the network RPC URL.

If your dApp doesn't run on Goerli, we also have a mainnet endpoint available,
but we are not publishing it just yet. If you are open to helping test your dApp
against our mainnet endpoint, **reach out to me directly and I'll get you the endpoint URL**.

We plan to leave these endpoints up through the end of April, 2019.

## Reporting Bugs

If you run into issues with Ether Cattle Replicas, please report them to our
[Github Repository](https://github.com/openrelayxyz/ethercattle-initiative).
Note that at this time we are running minimal infrastructure for the purposes of
testing the behavior of our RPC servers; we do not have these endpoints deployed
in a highly available configuration. We are monitoring for gateway errors, but
don't need bug reports when the endpoints go down.

If you have questions that you don't think necessarily warrant a bug report, you
can also reach out to us [on gitter](https://gitter.im/ethercattle-initiative/community).

### Acknowledgements

The work OpenRelay has done on the Ether Cattle Initiative has been possible (in part)
thanks to a 0x Ecosystem Development Grant. The work on Ether Cattle will always
be [open source](https://github.com/notegio/go-ethereum), and we hope to
contribute it back to the Go Ethereum project once it's stable.
