# High Level CDC

The ethdb/cdc module captures low level  write operations as they go into
LevelDB. This is useful for reconstructing a mirror of the replica, but there
are many other ways to represent the same data. If we capture the data at a
higher level of abstraction, we can feed it directly into other systems such as
state indexes, log indexes, and potentially other tools like block explorers.

## Design

The high level CDC implementation will inherently be a bit more invasive than
the low-level CDC implementation. With the low level, we simply needed a
database wrapper that could capture write operations and pass them through to
the underlying database while tee-ing them out to a stream. That could be done
with a wrapper, without ever touching the vast majority of the calling code. At
a time when we didn't fully understand the Geth datamodel, that was fairly
necessary. Given our current depth of understanding, we're more confident in our
ability to capture all the data we need.

The plan is to capture this data as it gets written through the core/rawdb
module. The callers to core/rawdb, however, just pass in an ethdb instance and
the data to be written. To avoid having to touch every caller to rawdb, we are
going to make an ethdb wrapper that passes conventional database calls to the
underlying database, but provides additional interfaces that the rawdb module
can check for and call if available.

Avoiding changes throughout the Geth codebase and limiting our changes to the
rawdb module serves multiple purposes. First, it makes sure that we capture
everything we need to capture - nearly everything that writes to leveldb does so
through rawdb, so we are unlikely to miss anything. Additionally, it limits the
surface for merge conflicts in the future; If we modified everything that calls
rawdb to give it an additional CDC object, it's very likely that changes to that
code in the future may introduce merge conflicts, some of which may be very
challenging to resolve.

This data is not comprehensive. Unlike low level CDC, you cannot necessarily
reconstruct a node from this stream. In particular, maintaining a full state
trie based on this data may not be possible. We provide all of the state
changes, but not the contract account roots for storage data.
