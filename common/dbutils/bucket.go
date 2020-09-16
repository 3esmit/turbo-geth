package dbutils

import (
	"bytes"
	"sort"
	"strings"

	"github.com/ledgerwatch/lmdb-go/lmdb"
	"github.com/ledgerwatch/turbo-geth/metrics"
)

// Buckets
var (
	// "Plain State". The same as CurrentStateBucket, but the keys arent' hashed.

	/*
		Logical layout:
			Contains Accounts:
			  key - address (unhashed)
			  value - account encoded for storage
			Contains Storage:
			  key - address (unhashed) + incarnation + storage key (unhashed)
			  value - storage value(common.hash)

		Physical layout:
			PlainStateBucket and CurrentStateBucket utilises DupSort feature of LMDB (store multiple values inside 1 key).
		-------------------------------------------------------------
			   key              |            value
		-------------------------------------------------------------
		[acc_hash]              | [acc_value]
		[acc_hash]+[inc]        | [storage1_hash]+[storage1_value]
								| [storage2_hash]+[storage2_value] // this value has no own key. it's 2nd value of [acc_hash]+[inc] key.
								| [storage3_hash]+[storage3_value]
								| ...
		[acc_hash]+[old_inc]    | [storage1_hash]+[storage1_value]
								| ...
		[acc2_hash]             | [acc2_value]
								...
	*/
	PlainStateBucket     = "PLAIN-CST2"
	PlainStateBucketOld1 = "PLAIN-CST"

	// "Plain State"
	//key - address+incarnation
	//value - code hash
	PlainContractCodeBucket = "PLAIN-contractCode"

	// PlainAccountChangeSetBucket keeps changesets of accounts ("plain state")
	// key - encoded timestamp(block number)
	// value - encoded ChangeSet{k - address v - account(encoded).
	PlainAccountChangeSetBucket = "PLAIN-ACS"

	// PlainStorageChangeSetBucket keeps changesets of storage ("plain state")
	// key - encoded timestamp(block number)
	// value - encoded ChangeSet{k - plainCompositeKey(for storage) v - originalValue(common.Hash)}.
	PlainStorageChangeSetBucket = "PLAIN-SCS"

	// Contains Accounts:
	// key - address hash
	// value - account encoded for storage
	// Contains Storage:
	//key - address hash + incarnation + storage key hash
	//value - storage value(common.hash)
	CurrentStateBucket     = "CST2"
	CurrentStateBucketOld1 = "CST"

	//key - address hash
	//value - list of block where it's changed
	AccountsHistoryBucket = "hAT"

	//key - address hash
	//value - list of block where it's changed
	StorageHistoryBucket = "hST"

	//key - contract code hash
	//value - contract code
	CodeBucket = "CODE"

	//key - addressHash+incarnation
	//value - code hash
	ContractCodeBucket = "contractCode"

	// Incarnations for deleted accounts
	//key - address
	//value - incarnation of account when it was last deleted
	IncarnationMapBucket = "incarnationMap"

	//AccountChangeSetBucket keeps changesets of accounts
	// key - encoded timestamp(block number)
	// value - encoded ChangeSet{k - addrHash v - account(encoded).
	AccountChangeSetBucket = "ACS"

	// StorageChangeSetBucket keeps changesets of storage
	// key - encoded timestamp(block number)
	// value - encoded ChangeSet{k - compositeKey(for storage) v - originalValue(common.Hash)}.
	StorageChangeSetBucket = "SCS"

	// some_prefix_of(hash_of_address_of_account) => hash_of_subtrie
	IntermediateTrieHashBucket     = "iTh2"
	IntermediateTrieHashBucketOld1 = "iTh"

	// DatabaseInfoBucket is used to store information about data layout.
	DatabaseInfoBucket = "DBINFO"

	// databaseVerisionKey tracks the current database version.
	DatabaseVerisionKey = "DatabaseVersion"

	// Data item prefixes (use single byte to avoid mixing data types, avoid `i`, used for indexes).
	HeaderPrefix       = "h"         // headerPrefix + num (uint64 big endian) + hash -> header
	HeaderTDSuffix     = []byte("t") // headerPrefix + num (uint64 big endian) + hash + headerTDSuffix -> td
	HeaderHashSuffix   = []byte("n") // headerPrefix + num (uint64 big endian) + headerHashSuffix -> hash
	HeaderNumberPrefix = "H"         // headerNumberPrefix + hash -> num (uint64 big endian)

	BlockBodyPrefix     = "b" // blockBodyPrefix + num (uint64 big endian) + hash -> block body
	BlockReceiptsPrefix = "r" // blockReceiptsPrefix + num (uint64 big endian) + hash -> block receipts

	BlockReceiptsPrefix2 = "r2"     // same as BlockReceiptsPrefix, but no logs
	ReceiptsIndex        = "ri"     // addr -> blockN + txIdx + logIdx + topics
	ReceiptsIndex2       = "ri2"    // blockN -> addr + txIdx + logIdx + topics - this block must be bigger than ReceiptsIndex+Logs buckets
	ReceiptsIndex3       = "ri3"    // blockN -> last2Bytes(topic) + txIdx + logIdx + topics
	ReceiptsIndex4       = "ri4"    // blockN -> last2Bytes(topic) + addr + txIdx + logIdx + topics
	ReceiptsIndex5       = "ri5"    // addr -> last2Bytes(topic) + blockN + txIdx + logIdx + topics
	Topics               = "topic"  // topic -> bitmap(BlockN)
	Topics2              = "topic2" // addr + topic -> bitmap(BlockN)

	Logs   = "rd"  // blockN + txIdx + logIdx -> logData
	Logs2  = "rd2" // blockN + txIdx + logIdx + addr + topics -> logData
	TxHash = "txh" // blockN -> txIdx + txHash

	Test1 = "test_1" // addr -> blockN
	Test2 = "test_2" // blockN -> addr

	TxLookupPrefix  = "l" // txLookupPrefix + hash -> transaction/receipt lookup metadata
	BloomBitsPrefix = "B" // bloomBitsPrefix + bit (uint16 big endian) + section (uint64 big endian) + hash -> bloom bits

	PreimagePrefix = "secure-key-"      // preimagePrefix + hash -> preimage
	ConfigPrefix   = "ethereum-config-" // config prefix for the db

	// Chain index prefixes (use `i` + single byte to avoid mixing data types).
	BloomBitsIndexPrefix = "iB" // BloomBitsIndexPrefix is the data table of a chain indexer to track its progress

	// Progress of sync stages: stageName -> stageData
	SyncStageProgress     = "SSP2"
	SyncStageProgressOld1 = "SSP"
	// Position to where to unwind sync stages: stageName -> stageData
	SyncStageUnwind     = "SSU2"
	SyncStageUnwindOld1 = "SSU"

	CliqueBucket = "clique-"

	// this bucket stored in separated database
	InodesBucket = "inodes"

	// Transaction senders - stored separately from the block bodies
	Senders  = "txSenders"
	Senders2 = "txSenders2"

	// fastTrieProgressKey tracks the number of trie entries imported during fast sync.
	FastTrieProgressKey = "TrieSync"
	// headBlockKey tracks the latest know full block's hash.
	HeadBlockKey = "LastBlock"
	// headFastBlockKey tracks the latest known incomplete block's hash during fast sync.
	HeadFastBlockKey = "LastFast"

	// migrationName -> serialized SyncStageProgress and SyncStageUnwind buckets
	// it stores stages progress to understand in which context was executed migration
	// in case of bug-report developer can ask content of this bucket
	Migrations = "migrations"
)

// Keys
var (
	// last block that was pruned
	// it's saved one in 5 minutes
	LastPrunedBlockKey = []byte("LastPrunedBlock")
	//StorageModeHistory - does node save history.
	StorageModeHistory = []byte("smHistory")
	//StorageModeReceipts - does node save receipts.
	StorageModeReceipts = []byte("smReceipts")
	//StorageModeTxIndex - does node save transactions index.
	StorageModeTxIndex = []byte("smTxIndex")

	HeadHeaderKey = "LastHeader"
)

// Metrics
var (
	PreimageCounter    = metrics.NewRegisteredCounter("db/preimage/total", nil)
	PreimageHitCounter = metrics.NewRegisteredCounter("db/preimage/hits", nil)
)

// Buckets - list of all buckets. App will panic if some bucket is not in this list.
// This list will be sorted in `init` method.
// BucketsConfigs - can be used to find index in sorted version of Buckets list by name
var Buckets = []string{
	CurrentStateBucket,
	AccountsHistoryBucket,
	StorageHistoryBucket,
	CodeBucket,
	ContractCodeBucket,
	AccountChangeSetBucket,
	StorageChangeSetBucket,
	IntermediateTrieHashBucket,
	DatabaseVerisionKey,
	HeaderPrefix,
	HeaderNumberPrefix,
	BlockBodyPrefix,
	BlockReceiptsPrefix,
	ReceiptsIndex,
	ReceiptsIndex2,
	Test1,
	Test2,
	Logs,
	TxLookupPrefix,
	BloomBitsPrefix,
	PreimagePrefix,
	ConfigPrefix,
	BloomBitsIndexPrefix,
	DatabaseInfoBucket,
	IncarnationMapBucket,
	CliqueBucket,
	SyncStageProgress,
	SyncStageUnwind,
	PlainStateBucket,
	PlainContractCodeBucket,
	PlainAccountChangeSetBucket,
	PlainStorageChangeSetBucket,
	InodesBucket,
	Senders,
	FastTrieProgressKey,
	HeadBlockKey,
	HeadFastBlockKey,
	HeadHeaderKey,
	Migrations,
	TxHash,
	BlockReceiptsPrefix2,
	ReceiptsIndex3,
	ReceiptsIndex4,
	ReceiptsIndex5,
	Topics,
	Logs2,
	Senders2,
	Topics2,
}

// DeprecatedBuckets - list of buckets which can be programmatically deleted - for example after migration
var DeprecatedBuckets = []string{
	SyncStageProgressOld1,
	SyncStageUnwindOld1,
	CurrentStateBucketOld1,
	PlainStateBucketOld1,
	IntermediateTrieHashBucketOld1,
}

type CustomComparator string

const (
	DefaultCmp     CustomComparator = ""
	DupCmpSuffix32 CustomComparator = "dup_cmp_suffix32"
)

type CmpFunc func(k1, k2, v1, v2 []byte) int

func DefaultCmpFunc(k1, k2, v1, v2 []byte) int { return bytes.Compare(k1, k2) }
func DefaultDupCmpFunc(k1, k2, v1, v2 []byte) int {
	cmp := bytes.Compare(k1, k2)
	if cmp == 0 {
		cmp = bytes.Compare(v1, v2)
	}
	return cmp
}

type BucketsCfg map[string]BucketConfigItem
type Bucket string

type BucketConfigItem struct {
	Flags uint
	// AutoDupSortKeysConversion - enables some keys transformation - to change db layout without changing app code.
	// Use it wisely - it helps to do experiments with DB format faster, but better reduce amount of Magic in app.
	// If good DB format found, push app code to accept this format and then disable this property.
	AutoDupSortKeysConversion bool
	IsDeprecated              bool
	DBI                       lmdb.DBI
	// DupFromLen - if user provide key of this length, then next transformation applied:
	// v = append(k[DupToLen:], v...)
	// k = k[:DupToLen]
	// And opposite at retrieval
	// Works only if AutoDupSortKeysConversion enabled
	DupFromLen          int
	DupToLen            int
	DupFixedSize        int
	CustomComparator    CustomComparator
	CustomDupComparator CustomComparator
}

var BucketsConfigs = BucketsCfg{
	CurrentStateBucket: {
		Flags:                     lmdb.DupSort,
		AutoDupSortKeysConversion: true,
		DupFromLen:                72,
		DupToLen:                  40,
	},
	PlainStateBucket: {
		Flags:                     lmdb.DupSort,
		AutoDupSortKeysConversion: true,
		DupFromLen:                60,
		DupToLen:                  28,
	},
	IntermediateTrieHashBucket: {
		Flags:               lmdb.DupSort,
		CustomDupComparator: DupCmpSuffix32,
	},
	ReceiptsIndex: {
		Flags: lmdb.DupSort,
	},
	ReceiptsIndex2: {
		Flags: lmdb.DupSort,
	},
	ReceiptsIndex3: {
		Flags: lmdb.DupSort,
	},
	ReceiptsIndex4: {
		Flags: lmdb.DupSort,
	},
	ReceiptsIndex5: {
		Flags: lmdb.DupSort,
	},
	TxHash: {
		Flags: lmdb.DupSort | lmdb.DupFixed,
	},
	Senders2: {
		Flags: lmdb.DupSort | lmdb.DupFixed,
	},
	Test1: {
		Flags: lmdb.DupSort,
	},
	Test2: {
		Flags: lmdb.DupSort,
	},
}

func sortBuckets() {
	sort.SliceStable(Buckets, func(i, j int) bool {
		return strings.Compare(Buckets[i], Buckets[j]) < 0
	})
}

func DefaultBuckets() BucketsCfg {
	return BucketsConfigs
}

func UpdateBucketsList(newBucketCfg BucketsCfg) {
	newBuckets := make([]string, 0)
	for k, v := range newBucketCfg {
		if !v.IsDeprecated {
			newBuckets = append(newBuckets, k)
		}
	}
	Buckets = newBuckets
	BucketsConfigs = newBucketCfg

	reinit()
}

func init() {
	reinit()
}

func reinit() {
	sortBuckets()

	for _, name := range Buckets {
		_, ok := BucketsConfigs[name]
		if !ok {
			BucketsConfigs[name] = BucketConfigItem{}
		}
	}

	for _, name := range DeprecatedBuckets {
		_, ok := BucketsConfigs[name]
		if !ok {
			BucketsConfigs[name] = BucketConfigItem{}
		}
		tmp := BucketsConfigs[name]
		tmp.IsDeprecated = true
		BucketsConfigs[name] = tmp
	}
}
