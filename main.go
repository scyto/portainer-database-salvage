package main

import (
	"fmt"
	"log"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <corrupt.db> <recovered.db>\n", os.Args[0])
		os.Exit(1)
	}

	srcPath := os.Args[1]
	dstPath := os.Args[2]

	// Open corrupt DB in read-only mode with no freelist sync.
	// Uses bbolt v1.3.x which is less strict about page assertions
	// than v1.4.x, allowing it to open databases that v1.4.x refuses.
	src, err := bolt.Open(srcPath, 0400, &bolt.Options{
		ReadOnly:       true,
		NoFreelistSync: true,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to open source DB: %v", err)
	}
	defer src.Close()

	dst, err := bolt.Open(dstPath, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		log.Fatalf("Failed to create destination DB: %v", err)
	}
	defer dst.Close()

	// Collect bucket names
	var bucketNames [][]byte
	err = src.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			nameCopy := make([]byte, len(name))
			copy(nameCopy, name)
			bucketNames = append(bucketNames, nameCopy)
			return nil
		})
	})
	if err != nil {
		log.Fatalf("Failed to list buckets: %v", err)
	}

	fmt.Printf("Found %d top-level buckets to recover\n", len(bucketNames))

	totalKeys := 0
	failedBuckets := 0

	for _, bname := range bucketNames {
		keys, ok := recoverBucket(src, dst, bname)
		totalKeys += keys
		if !ok {
			failedBuckets++
		}
	}

	fmt.Printf("\n=== Recovery Summary ===\n")
	fmt.Printf("Total keys recovered: %d\n", totalKeys)
	fmt.Printf("Buckets with errors:  %d / %d\n", failedBuckets, len(bucketNames))
	fmt.Printf("Output: %s\n", dstPath)

	if failedBuckets > 0 {
		os.Exit(1)
	}
}

type kv struct {
	key, value []byte
}

type subbucket struct {
	name []byte
	data []kv
}

// recoverBucket reads all key/value pairs from a source bucket and writes
// them to the destination DB. Panics from corrupt pages are caught with
// recover(), allowing partial data salvage.
func recoverBucket(src, dst *bolt.DB, bucketName []byte) (recovered int, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("  [PANIC] Bucket %q: %v (recovered %d keys before panic)\n",
				string(bucketName), r, recovered)
			ok = false
		}
	}()

	fmt.Printf("Recovering bucket %q ...\n", string(bucketName))

	var pairs []kv
	var subs []subbucket

	err := src.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}
		return b.ForEach(func(k, v []byte) error {
			if v == nil {
				// Sub-bucket
				sub := b.Bucket(k)
				if sub != nil {
					nameCopy := make([]byte, len(k))
					copy(nameCopy, k)
					subData := recoverSubBucket(sub, string(bucketName))
					subs = append(subs, subbucket{name: nameCopy, data: subData})
				}
				return nil
			}
			keyCopy := make([]byte, len(k))
			valCopy := make([]byte, len(v))
			copy(keyCopy, k)
			copy(valCopy, v)
			pairs = append(pairs, kv{keyCopy, valCopy})
			return nil
		})
	})

	if err != nil {
		fmt.Printf("  [ERROR] Reading bucket %q: %v\n", string(bucketName), err)
	}

	// Write recovered data to destination
	writeErr := dst.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return err
		}

		for _, p := range pairs {
			if err := b.Put(p.key, p.value); err != nil {
				fmt.Printf("  [WARN] Failed to write key: %v\n", err)
				continue
			}
			recovered++
		}

		for _, sb := range subs {
			child, err := b.CreateBucketIfNotExists(sb.name)
			if err != nil {
				fmt.Printf("  [WARN] Failed to create sub-bucket %q: %v\n", string(sb.name), err)
				continue
			}
			for _, p := range sb.data {
				if err := child.Put(p.key, p.value); err != nil {
					continue
				}
				recovered++
			}
		}

		return nil
	})

	if writeErr != nil {
		fmt.Printf("  [ERROR] Writing bucket %q: %v\n", string(bucketName), writeErr)
		return recovered, false
	}

	fmt.Printf("  [OK] %d keys, %d sub-buckets\n", len(pairs), len(subs))
	return recovered, true
}

func recoverSubBucket(b *bolt.Bucket, parentName string) (pairs []kv) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("  [PANIC] Sub-bucket in %q: %v (recovered %d keys)\n",
				parentName, r, len(pairs))
		}
	}()

	_ = b.ForEach(func(k, v []byte) error {
		if v == nil {
			return nil // skip deeper nesting
		}
		keyCopy := make([]byte, len(k))
		valCopy := make([]byte, len(v))
		copy(keyCopy, k)
		copy(valCopy, v)
		pairs = append(pairs, kv{keyCopy, valCopy})
		return nil
	})

	return pairs
}
