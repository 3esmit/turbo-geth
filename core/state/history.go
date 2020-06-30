package state

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/changeset"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/core/types/accounts"
	"github.com/ledgerwatch/turbo-geth/eth/stagedsync/stages"
	"github.com/ledgerwatch/turbo-geth/ethdb"
)

func GetAsOf(db ethdb.KV, plain, storage bool, key []byte, timestamp uint64) ([]byte, error) {
	var dat []byte
	err := db.View(context.Background(), func(tx ethdb.Tx) error {
		v, err := FindByHistory(tx, plain, storage, key, timestamp)
		if err == nil {
			dat = make([]byte, len(v))
			copy(dat, v)
			return nil
		}
		if !errors.Is(err, ethdb.ErrKeyNotFound) {
			return err
		}
		{
			var bucket []byte
			if plain {
				bucket = dbutils.PlainStateBucket
			} else {
				bucket = dbutils.CurrentStateBucket
			}
			v, _ := tx.Bucket(bucket).Get(key)
			if v == nil {
				return ethdb.ErrKeyNotFound
			}

			dat = make([]byte, len(v))
			copy(dat, v)
			return nil
		}
	})
	return dat, err
}

func FindByHistory(tx ethdb.Tx, plain, storage bool, key []byte, timestamp uint64) ([]byte, error) {
	var hBucket []byte
	if storage {
		hBucket = dbutils.StorageHistoryBucket
	} else {
		hBucket = dbutils.AccountsHistoryBucket
	}
	hB := tx.Bucket(hBucket)
	if hB == nil {
		return nil, ethdb.ErrKeyNotFound
	}
	c := hB.Cursor()
	k, v, err := c.Seek(dbutils.IndexChunkKey(key, timestamp))
	if err != nil {
		return nil, err
	}
	if k == nil {
		return nil, ethdb.ErrKeyNotFound
	}
	if storage {
		if plain {
			if !bytes.Equal(k[:common.AddressLength], key[:common.AddressLength]) ||
				!bytes.Equal(k[common.AddressLength:common.AddressLength+common.HashLength], key[common.AddressLength+common.IncarnationLength:]) {
				return nil, ethdb.ErrKeyNotFound
			}
		} else {
			if !bytes.Equal(k[:common.HashLength], key[:common.HashLength]) ||
				!bytes.Equal(k[common.HashLength:common.HashLength+common.HashLength], key[common.HashLength+common.IncarnationLength:]) {
				return nil, ethdb.ErrKeyNotFound
			}
		}
	} else {
		if !bytes.HasPrefix(k, key) {
			return nil, ethdb.ErrKeyNotFound
		}
	}
	index := dbutils.WrapHistoryIndex(v)

	changeSetBlock, set, ok := index.Search(timestamp)
	var data []byte
	if ok {
		// set == true if this change was from empty record (non-existent account) to non-empty
		// In such case, we do not need to examine changeSet and return empty data
		if set {
			return []byte{}, nil
		}
		csBucket := dbutils.ChangeSetByIndexBucket(plain, storage)
		csB := tx.Bucket(csBucket)
		if csB == nil {
			return nil, fmt.Errorf("no changeset bucket %s", csB)
		}

		csKey := dbutils.EncodeTimestamp(changeSetBlock)
		changeSetData, _ := csB.Get(csKey)

		if plain {
			if storage {
				data, err = changeset.StorageChangeSetPlainBytes(changeSetData).FindWithoutIncarnation(key[:common.AddressLength], key[common.AddressLength+common.IncarnationLength:])
			} else {
				data, err = changeset.AccountChangeSetPlainBytes(changeSetData).Find(key)
			}
		} else if storage {
			data, err = changeset.StorageChangeSetBytes(changeSetData).FindWithoutIncarnation(key[:common.HashLength], key[common.HashLength+common.IncarnationLength:])
		} else {
			data, err = changeset.AccountChangeSetBytes(changeSetData).Find(key)
		}
		if err != nil {
			if !errors.Is(err, ethdb.ErrKeyNotFound) {
				return nil, fmt.Errorf("finding %x in the changeset %d: %w", key, changeSetBlock, err)
			}
			return nil, err
		}
	} else if plain {
		var lastChangesetBlock, lastIndexBlock uint64
		stageBucket := tx.Bucket(dbutils.SyncStageProgress)
		if stageBucket != nil {
			v1, err1 := stageBucket.Get([]byte{byte(stages.Execution)})
			if err1 != nil && !errors.Is(err1, ethdb.ErrKeyNotFound) {
				return nil, err1
			}
			if len(v1) > 0 {
				lastChangesetBlock = binary.BigEndian.Uint64(v1[:8])
			}
			if storage {
				v1, err1 = stageBucket.Get([]byte{byte(stages.AccountHistoryIndex)})
			} else {
				v1, err1 = stageBucket.Get([]byte{byte(stages.StorageHistoryIndex)})
			}
			if err1 != nil && !errors.Is(err1, ethdb.ErrKeyNotFound) {
				return nil, err1
			}
			if len(v1) > 0 {
				lastIndexBlock = binary.BigEndian.Uint64(v1[:8])
			}
		}
		if lastChangesetBlock > lastIndexBlock {
			// iterate over changeset to compensate for lacking of the history index
			csBucket := dbutils.ChangeSetByIndexBucket(plain, storage)
			csB := tx.Bucket(csBucket)
			c := csB.Cursor()
			var startTimestamp uint64
			if timestamp < lastIndexBlock {
				startTimestamp = lastIndexBlock + 1
			} else {
				startTimestamp = timestamp + 1
			}
			startKey := dbutils.EncodeTimestamp(startTimestamp)
			err = nil
			for k, v, err1 := c.Seek(startKey); k != nil && err1 == nil; k, v, err1 = c.Next() {
				if storage {
					data, err = changeset.StorageChangeSetPlainBytes(v).FindWithoutIncarnation(key[:common.AddressLength], key[common.AddressLength+common.IncarnationLength:])
				} else {
					data, err = changeset.AccountChangeSetPlainBytes(v).Find(key)
				}
				if err == nil {
					break
				}
				if !errors.Is(err, changeset.ErrNotFound) {
					return nil, fmt.Errorf("finding %x in the changeset %d: %w", key, changeSetBlock, err)
				}
			}
			if err != nil {
				return nil, ethdb.ErrKeyNotFound
			}
		}
	} else {
		return nil, ethdb.ErrKeyNotFound
	}

	//restore codehash
	if !storage {
		var acc accounts.Account
		if err := acc.DecodeForStorage(data); err != nil {
			return nil, err
		}
		if acc.Incarnation > 0 && acc.IsEmptyCodeHash() {
			var codeHash []byte
			if plain {
				codeBucket := tx.Bucket(dbutils.PlainContractCodeBucket)
				codeHash, _ = codeBucket.Get(dbutils.PlainGenerateStoragePrefix(key, acc.Incarnation))
			} else {
				codeBucket := tx.Bucket(dbutils.ContractCodeBucket)
				codeHash, _ = codeBucket.Get(dbutils.GenerateStoragePrefix(key, acc.Incarnation))
			}
			if len(codeHash) > 0 {
				acc.CodeHash = common.BytesToHash(codeHash)
			}
			data = make([]byte, acc.EncodingLengthForStorage())
			acc.EncodeForStorage(data)
		}
		return data, nil
	}

	return data, nil
}


func WalkAsOf(db ethdb.KV, bucket, hBucket, startkey []byte, fixedbits int, timestamp uint64, walker func(k []byte, v []byte) (bool, error)) error {
	//fmt.Printf("WalkAsOf %x %x %x %d %d\n", dbi, hBucket, startkey, fixedbits, timestamp)
	if !(bytes.Equal(bucket, dbutils.PlainStateBucket) || bytes.Equal(bucket, dbutils.CurrentStateBucket)) {
		return fmt.Errorf("unsupported state bucket: %s", string(bucket))
	}
	if bytes.Equal(hBucket, dbutils.AccountsHistoryBucket) {
		return walkAsOfThinAccounts(db,bucket, hBucket, startkey, fixedbits, timestamp, walker)
	} else if  bytes.Equal(hBucket, dbutils.StorageHistoryBucket) {
		return walkAsOfThinStorage(db, bucket, hBucket, startkey, fixedbits, timestamp, func(k1, k2, v []byte) (bool, error) {
			return walker(append(common.CopyBytes(k1), k2...), v)
		})
	}

	panic(fmt.Sprintf("Not implemented for arbitrary buckets: %s, %s", string(bucket), string(hBucket)))
}


func walkAsOfThinStorage(db ethdb.KV, bucket, hBucket, startkey []byte, fixedbits int, timestamp uint64, walker func(k1, k2, v []byte) (bool, error)) error {
	err := db.View(context.Background(), func(tx ethdb.Tx) error {

		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("storageBucket not found")
		}
		hB := tx.Bucket(dbutils.StorageHistoryBucket)
		if hB == nil {
			return fmt.Errorf("storageHistoryBucket not found")
		}

		csBucket:= dbutils.StorageChangeSetBucket
		if bytes.Equal(bucket, dbutils.PlainStateBucket) {
			csBucket = dbutils.PlainStorageChangeSetBucket
		}
		csB := tx.Bucket(csBucket)
		if csB == nil {
			return fmt.Errorf("storageChangeBucket not found")
		}

		startkeyNoInc:=dbutils.CompositeKeyWithoutIncarnation(startkey)
		part1End:=common.HashLength
		part2Start:=common.HashLength+common.IncarnationLength
		part3Start:=common.HashLength+common.IncarnationLength+common.HashLength
		if bytes.Equal(bucket, dbutils.PlainStateBucket) {
			part1End=common.AddressLength
			part2Start=common.AddressLength+common.IncarnationLength
			part3Start=common.AddressLength+common.IncarnationLength+common.HashLength

		}

		//for storage
		mainCursor := ethdb.NewSplitCursor(
			b,
			startkey,
			fixedbits,
			part1End,
			part2Start,
			part3Start,
		)
		fixetBitsForHistory:=fixedbits-8*common.IncarnationLength
		if fixetBitsForHistory<0 {
			fixetBitsForHistory=0
		}

		part1End=common.HashLength
		part2Start=common.HashLength
		part3Start=common.HashLength*2
		if bytes.Equal(bucket, dbutils.PlainStateBucket) {
			part1End=common.AddressLength
			part2Start=common.AddressLength
			part3Start=common.AddressLength+common.HashLength
		}

		//for historic data
		var historyCursor historyCursor = ethdb.NewSplitCursor(
			hB,
			startkeyNoInc,
			fixetBitsForHistory,
			part1End,   /* part1end */
			part2Start,   /* part2start */
			part3Start, /* part3start */
		)
		if true {
			part1End:=common.HashLength
			part2Start:=common.HashLength+common.IncarnationLength
			part3Start:=common.HashLength+common.IncarnationLength+common.HashLength

			decorator:=NewChangesetSearchDecorator(historyCursor, startkey,fixetBitsForHistory, part1End, part2Start, part3Start)
			err:=decorator.buildChangeset(csB, 0, 7, timestamp, changeset.Mapper[string(csBucket)].WalkerAdapter)
			if err!=nil {
				return err
			}
			historyCursor = decorator
		}
		addrHash, keyHash, _, v, err1 := mainCursor.Seek()
		if err1 != nil {
			return err1
		}

		hAddrHash, hKeyHash, tsEnc, hV, err2 := historyCursor.Seek()
		if err2 != nil {
			return err2
		}

		//find firsh chunk after timestamp
		for hKeyHash != nil && binary.BigEndian.Uint64(tsEnc) < timestamp {
			hAddrHash, hKeyHash, tsEnc, hV, err2 = historyCursor.Next()
			if err2 != nil {
				return err2
			}
		}
		goOn := true
		var err error
		for goOn {
			cmp,br:=walkAsOfCmp(keyHash, hKeyHash)
			if br {
				break
			}

			//next key in state
			if cmp < 0 {
				goOn, err = walker(addrHash, keyHash, v)
			} else {
				hK:=make([]byte, len(hAddrHash)+len(hKeyHash))
				copy(hK[:len(hAddrHash)], hAddrHash)
				copy(hK[len(hAddrHash):], hKeyHash)
				data, found, err:=findInHistory(hK, hV,timestamp,csB.Get, returnCorrectWalker(bucket, hBucket))
				if err!=nil{
					return err
				}

				if found && len(data) > 0 { // Skip accounts did not exist
					goOn, err = walker(hAddrHash, hKeyHash, data)
				}
				if !found && cmp == 0 {
					goOn, err = walker(addrHash, keyHash, v)
				}

			}
			if goOn {
				if cmp <= 0 {
					addrHash, keyHash, _, v, err1 = mainCursor.Next()
					if err1 != nil {
						return err1
					}
				}
				if cmp >= 0 {
					hKeyHash0 := hKeyHash
					hAddrHash0:=hAddrHash
					for bytes.Equal(hAddrHash0, hAddrHash) && hKeyHash != nil && (bytes.Equal(hKeyHash0, hKeyHash) || binary.BigEndian.Uint64(tsEnc) < timestamp) {
						hAddrHash, hKeyHash, tsEnc, hV, err2 = historyCursor.Next()
						if err2 != nil {
							return err2
						}
					}
				}
			}
		}
		return err
	})
	return err
}



func walkAsOfThinAccounts(db ethdb.KV, bucket, hBucket, startkey []byte, fixedbits int, timestamp uint64, walker func(k []byte, v []byte) (bool, error)) error {
	fixedbytes, mask := ethdb.Bytesmask(fixedbits)
	err := db.View(context.Background(), func(tx ethdb.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("currentStateBucket not found")
		}
		hB := tx.Bucket(dbutils.AccountsHistoryBucket)
		if hB == nil {
			return fmt.Errorf("accountsHistoryBucket not found")
		}

		csBucket:= dbutils.AccountChangeSetBucket
		if bytes.Equal(bucket, dbutils.PlainStateBucket) {
			csBucket = dbutils.PlainAccountChangeSetBucket
		}

		csB := tx.Bucket(csBucket)
		if csB == nil {
			return fmt.Errorf("accountChangeBucket not found")
		}
		//for state
		mainCursor := b.Cursor()
		//for historic data
		part1End:=common.HashLength
		part2Start:=common.HashLength
		part3Start:=common.HashLength+8
		maxKeyLen:=common.HashLength
		if bytes.Equal(bucket, dbutils.PlainStateBucket) {
			part1End=common.AddressLength
			part2Start=common.AddressLength
			part3Start=common.AddressLength+8
			maxKeyLen=common.AddressLength
		}

		historyCursor := ethdb.NewSplitCursor(
			hB,
			startkey,
			fixedbits,
			part1End,   /* part1end */
			part2Start,   /* part2start */
			part3Start, /* part3start */
		)
		k, v, err1 := mainCursor.Seek(startkey)
		if err1 != nil {
			return err1
		}
		for k != nil && len(k) > maxKeyLen {
			k, v, err1 = mainCursor.Next()
			if err1 != nil {
				return err1
			}
		}
		hK, tsEnc, _, hV, err1 := historyCursor.Seek()
		if err1 != nil {
			return err1
		}
		for hK != nil && binary.BigEndian.Uint64(tsEnc) < timestamp {
			hK, tsEnc, _, hV, err1 = historyCursor.Next()
			if err1 != nil {
				return err1
			}
		}
		goOn := true
		var err error
		for goOn {
			//exit or next conditions
			if k != nil && fixedbits > 0 && !bytes.Equal(k[:fixedbytes-1], startkey[:fixedbytes-1]) {
				k = nil
			}
			if k != nil && fixedbits > 0 && (k[fixedbytes-1]&mask) != (startkey[fixedbytes-1]&mask) {
				k = nil
			}
			var cmp int
			if k == nil {
				if hK == nil {
					break
				} else {
					cmp = 1
				}
			} else if hK == nil {
				cmp = -1
			} else {
				cmp = bytes.Compare(k, hK)
			}
			if cmp < 0 {
				goOn, err = walker(k, v)
			} else {
				data, found, err:=findInHistory(hK, hV,timestamp,csB.Get, returnCorrectWalker(bucket, hBucket))
				if err!=nil{
					return err
				}

				if found && len(data) > 0 { // Skip accounts did not exist
					goOn, err = walker(hK, data)
				}
				if !found && cmp == 0 {
					goOn, err = walker(k, v)
				}
			}
			if goOn {
				if cmp <= 0 {
					k, v, err1 = mainCursor.Next()
					if err1 != nil {
						return err1
					}
					for k != nil && len(k) > common.HashLength {
						k, v, err1 = mainCursor.Next()
						if err1 != nil {
							return err1
						}
					}
				}
				if cmp >= 0 {
					hK0 := hK
					for hK != nil && (bytes.Equal(hK0, hK) || binary.BigEndian.Uint64(tsEnc) < timestamp) {
						hK, tsEnc, _, hV, err1 = historyCursor.Next()
						if err1 != nil {
							return err1
						}
					}
				}
			}
		}
		return err
	})
	return err
}


func walkAsOfCmp(keyHash, hKeyHash []byte) (int,bool) {
	switch {
	case keyHash==nil&&hKeyHash == nil:
		return 0, true
	case keyHash == nil && hKeyHash != nil:
		return 1,false
	case keyHash != nil && hKeyHash == nil:
		return -1,false
	default:
		return bytes.Compare(keyHash, hKeyHash), false
	}
}



func findInHistory(hK, hV []byte, timestamp uint64, csGetter func([]byte)([]byte, error), adapter func(v []byte) changeset.Walker) ([]byte, bool, error)  {
	index := dbutils.WrapHistoryIndex(hV)
	if changeSetBlock, set, ok := index.Search(timestamp); ok {
		// set == true if this change was from empty record (non-existent account) to non-empty
		// In such case, we do not need to examine changeSet and simply skip the record
		if !set {
			// Extract value from the changeSet
			csKey := dbutils.EncodeTimestamp(changeSetBlock)
			changeSetData, _ := csGetter(csKey)
			if changeSetData == nil {
				return nil, false,fmt.Errorf("could not find ChangeSet record for index entry %d (query timestamp %d)", changeSetBlock, timestamp)
			}

			data, err2 := adapter(changeSetData).Find(hK)
			if err2 != nil {
				return nil, false, fmt.Errorf("could not find key %x in the ChangeSet record for index entry %d (query timestamp %d): %v",
					hK,
					changeSetBlock,
					timestamp,
					err2,
				)
			}
			return data, true, nil
		}
		return nil, true,  nil
	}
	return nil, false, nil
}

func returnCorrectWalker(bucket, hBucket []byte) func(v []byte) changeset.Walker  {
	switch  {
	case bytes.Equal(bucket,dbutils.CurrentStateBucket)&&bytes.Equal(hBucket, dbutils.StorageHistoryBucket):
		return changeset.Mapper[string(dbutils.StorageChangeSetBucket)].WalkerAdapter
	case bytes.Equal(bucket,dbutils.CurrentStateBucket)&&bytes.Equal(hBucket, dbutils.AccountsHistoryBucket):
		return changeset.Mapper[string(dbutils.AccountChangeSetBucket)].WalkerAdapter
	case bytes.Equal(bucket,dbutils.PlainStateBucket)&&bytes.Equal(hBucket, dbutils.StorageHistoryBucket):
		return changeset.Mapper[string(dbutils.PlainStorageChangeSetBucket)].WalkerAdapter
	case bytes.Equal(bucket,dbutils.PlainStateBucket)&&bytes.Equal(hBucket, dbutils.AccountsHistoryBucket):
		return changeset.Mapper[string(dbutils.PlainAccountChangeSetBucket)].WalkerAdapter
	default:
		panic("not implemented")
	}
}

type historyCursor interface {
	Seek() (key1, key2, key3, val []byte, err error)
	Next() (key1, key2, key3, val []byte, err error)
}

func NewChangesetSearchDecorator(historyCursor historyCursor, startKey []byte,matchBits, part1End, part2Start, part3Start int) *changesetSearchDecorator {
	matchBytes, mask := ethdb.Bytesmask(matchBits)
	return &changesetSearchDecorator{
		startKey: startKey,
		historyCursor: historyCursor,
		part1End: part1End,
		part2Start: part2Start,
		part3Start: part3Start,

		matchBytes: matchBytes,
		byteMask: mask,
	}
}

type changesetSearchDecorator struct {
	startKey []byte
	historyCursor historyCursor
	part1End int
	part2Start int
	part3Start int
	matchBytes int
	byteMask byte

	pos    int
	values []changeset.Change
}
func (csd *changesetSearchDecorator) Seek() (key1, key2, key3, val []byte, err error) {
	pos:=sort.Search(len(csd.values), func(i int) bool {
		return bytes.Compare(csd.startKey,csd.values[i].Key) < 0
	})
	if pos<len(csd.values) {
		csd.pos=pos
		if !csd.matchKey(csd.values[csd.pos].Key) {
			return nil, nil, nil, nil, nil
		}
		return csd.values[csd.pos].Key[:csd.part1End], csd.values[csd.pos].Key[csd.part2Start:csd.part3Start], csd.values[csd.pos].Key[csd.part3Start:], csd.values[csd.pos].Value, nil
	}
	return csd.historyCursor.Seek()
}
func (csd *changesetSearchDecorator) Next() (key1, key2, key3, val []byte, err error) {
	if len(csd.values)>0 && csd.pos < len(csd.values) {
		if !csd.matchKey(csd.values[csd.pos].Key) {
			return nil, nil, nil, nil, nil
		}
		defer func() {
			csd.pos++
		}()
		return csd.values[csd.pos].Key[:csd.part1End], csd.values[csd.pos].Key[csd.part2Start:csd.part3Start], csd.values[csd.pos].Key[csd.part3Start:], csd.values[csd.pos].Value, nil
	}
	return csd.historyCursor.Next()
}

func (csd *changesetSearchDecorator) matchKey(k []byte) bool {
	if k == nil {
		return false
	}
	if csd.matchBytes == 0 {
		return true
	}
	if len(k) < csd.matchBytes {
		return false
	}
	if !bytes.Equal(k[:csd.matchBytes-1], csd.startKey[:csd.matchBytes-1]) {
		return false
	}
	return (k[csd.matchBytes-1] & csd.byteMask) == (csd.startKey[csd.matchBytes-1] & csd.byteMask)
}

func (csd *changesetSearchDecorator) buildChangeset(bucket ethdb.Bucket, from, to, timestamp uint64, walkerAdapter func(v []byte)changeset.Walker) error {
	fmt.Println("buildChangeset")
	cs:=make([]struct{
		Walker changeset.Walker
		BlockNum uint64
	},0, to-from)
	c:=bucket.Cursor()
	_,_,err:=c.Seek(dbutils.EncodeTimestamp(from))
	if err!=nil {
		return err
	}
	err = c.Walk(func(k, v []byte) (bool, error) {
		blockNum,_:=dbutils.DecodeTimestamp(k)
		if blockNum>to {
			return false, nil
		}
		cs=append(cs, struct {
			Walker   changeset.Walker
			BlockNum uint64
		}{Walker: walkerAdapter(common.CopyBytes(v)), BlockNum: blockNum})

		return true, nil
	})
	if err!=nil {
		return err
	}

	mp:=make(map[string][]byte)
	for i:=len(cs)-1; i>=0; i-- {
		replace:=cs[i].BlockNum>=timestamp
		err = cs[i].Walker.Walk(func(k, v []byte) error {
			if replace {
				mp[string(k)]=v
			}
			return nil
		})
		if err!=nil {
			return err
		}
	}
	res:=make([]changeset.Change, len(mp))
	i:=0
	for k:=range mp{
		res[i]=changeset.Change{
			Key: []byte(k),
			Value: mp[k],
		}
		i++
	}
	sort.Slice(res, func(i, j int) bool {
		cmp := bytes.Compare(res[i].Key, res[j].Key)
		return cmp <= 0
	})
	csd.values =res
	for _,v:=range res {
		fmt.Println("core/state/history.go:623", common.Bytes2Hex(v.Key), string(v.Value))
	}
	return nil
}