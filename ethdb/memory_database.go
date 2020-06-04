// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package ethdb

import (
	"context"

	"github.com/ledgerwatch/bolt"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/debug"
	"github.com/ledgerwatch/turbo-geth/log"
)

func NewMemDatabase() *ObjectDatabase {
	//logger := log.New("database", "in-memory")
	//
	//// Open the db and recover any potential corruptions
	//db, errOpen := bolt.Open("in-memory", 0600, &bolt.Options{MemOnly: true})
	//if errOpen != nil {
	//	panic(errOpen)
	//}
	//
	//if err := db.Update(func(tx *bolt.Tx) error {
	//	for _, bucket := range dbutils.Buckets {
	//		if _, err := tx.CreateBucketIfNotExists(bucket, false); err != nil {
	//			return err
	//		}
	//	}
	//	return nil
	//}); err != nil {
	//	panic(err)
	//}
	//
	//b := &BoltDatabase{
	//	db:  db,
	//	log: logger,
	//	id:  id(),
	//}
	//
	//return b

	switch debug.TestDB() {
	case "badger":
		return NewObjectDatabase(NewBadger().InMem().MustOpen(context.Background()))
	case "lmdb":
		return NewObjectDatabase(NewLMDB().InMem().MustOpen(context.Background()))
	default:
		return NewObjectDatabase(NewLMDB().InMem().MustOpen(context.Background()))
		//Badgerf2, _ = os.OpenFile("/Users/alex.sharov/projects/go/src/github.com/ledgerwatch/turbo-geth/contracts/checkpointoracle/badger2.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		//badgerf, _ = os.OpenFile("/Users/alex.sharov/projects/go/src/github.com/ledgerwatch/turbo-geth/contracts/checkpointoracle/badger.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		//return NewObjectDatabase(NewBadger().InMem().MustOpen(context.Background()))

		//Boltf2, _ = os.OpenFile("/Users/alex.sharov/projects/go/src/github.com/ledgerwatch/turbo-geth/contracts/checkpointoracle/bolt2.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		//boltf, _ = os.OpenFile("/Users/alex.sharov/projects/go/src/github.com/ledgerwatch/turbo-geth/contracts/checkpointoracle/bolt.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)

		//return NewObjectDatabase(NewBolt().InMem().MustOpen(context.Background()))
	}
}

func NewMemDatabase2() (*BoltDatabase, KV) {
	logger := log.New("database", "in-memory")
	// Open the db and recover any potential corruptions
	db, err := bolt.Open("in-memory", 0600, &bolt.Options{MemOnly: true, KeysPrefixCompressionDisable: true})
	if err != nil {
		panic(err)
	}

	kv, err := NewBolt().WrapBoltDb(db)
	if err != nil {
		panic(err)
	}
	return &BoltDatabase{
		db:  db,
		log: logger,
		id:  id(),
	}, kv
}

func (db *BoltDatabase) MemCopy() Database {
	logger := log.New("database", "in-memory")

	// Open the db and recover any potential corruptions
	mem, err := bolt.Open("in-memory", 0600, &bolt.Options{MemOnly: true})
	if err != nil {
		panic(err)
	}

	if err := db.db.View(func(readTx *bolt.Tx) error {
		return readTx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return mem.Update(func(writeTx *bolt.Tx) error {
				newBucketToWrite, err := writeTx.CreateBucket(name, true)
				if err != nil {
					return err
				}
				return b.ForEach(func(k, v []byte) error {
					if err := newBucketToWrite.Put(common.CopyBytes(k), common.CopyBytes(v)); err != nil {
						return err
					}
					return nil
				})
			})
		})
	}); err != nil {
		panic(err)
	}
	return &BoltDatabase{
		db:  mem,
		log: logger,
		id:  id(),
	}
}
