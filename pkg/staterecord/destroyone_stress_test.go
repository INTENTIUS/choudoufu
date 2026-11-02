// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestStressRunCacheOverS3SeesBothRecords is GitHub issue #1355's shape at
// the store layer, repeated: two record keys (the issue's own for_each keys,
// "a.b" and "plain", as projection encodes them) in an S3 namespace, read
// through [RunCache] over [S3Store] the way a plan reads them, with the fake
// S3 made as unhelpful as it can be while still telling the truth: every
// ListObjectsV2 page holds one key, so the listing is paginated with
// continuation tokens, and every GetObject waits a random 0..maxDelay before
// it is answered, so the fan-out's GETs complete in random order.
//
// Readers race each other the way the plan phase's callers do: several
// goroutines call Get on both keys and List on the namespace at once, the
// first of them triggering the bulk load. Every Get must answer that the
// record exists, and every List must name both keys.
//
// It is a reproduction attempt, not a regression gate, so it runs only when
// CHOUDOUFU_STRESS_1355 names an iteration count.
func TestStressRunCacheOverS3SeesBothRecords(t *testing.T) {
	iters, _ := strconv.Atoi(os.Getenv("CHOUDOUFU_STRESS_1355"))
	if iters <= 0 {
		t.Skip("set CHOUDOUFU_STRESS_1355=<iterations> to run the #1355 stress loop")
	}
	const ns = "tofu-records/tagged-estate/"
	keys := []string{
		ns + "terraform_data/dGVycmFmb3JtX2RhdGEuZWZmZWN0WyJhLmIiXQ",
		ns + "terraform_data/dGVycmFmb3JtX2RhdGEuZWZmZWN0WyJwbGFpbiJd",
	}
	for _, parallelism := range []int{1, 2, 8, 32} {
		t.Run(fmt.Sprintf("parallelism=%d", parallelism), func(t *testing.T) {
			bad := 0
			for it := 0; it < iters; it++ {
				ResetRunCacheForTest(t)
				server, fake := newFakeS3Server(t)
				fake.pageSize = 1
				maxDelay := time.Duration(rand.Intn(4000)) * time.Microsecond
				control := os.Getenv("CHOUDOUFU_STRESS_1355_CONTROL") == "1"
				fake.beforeGet = func(path string) int {
					time.Sleep(time.Duration(rand.Int63n(int64(maxDelay) + 1)))
					// The control: a store that 404s one record every time.
					// The loop must report it, or it is a loop that cannot fail.
					if control && strings.HasSuffix(path, keys[1]) {
						return http.StatusNotFound
					}
					return 0
				}
				client := s3.NewFromConfig(aws.Config{
					Region:      "us-east-1",
					Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
				}, func(o *s3.Options) {
					o.BaseEndpoint = aws.String(server.URL)
					o.UsePathStyle = true
				})
				store, err := NewS3Store(S3Config{Client: client, Bucket: "b", GetAllParallelism: parallelism})
				if err != nil {
					t.Fatal(err)
				}
				// Noise under the same bucket and namespace root, so the
				// listing pages through more than the two keys: the sentinel
				// and other types' records.
				seed := append([]string{ns + ".store-sentinel", ns + "aws_s3_bucket/eA", ns + "zzz/eQ"}, keys...)
				for _, k := range seed {
					if _, err := store.PutIfAbsent(context.Background(), k, []byte(`{"k":"`+k+`"}`)); err != nil {
						t.Fatal(err)
					}
				}
				cache := NewRunCache(store, ns)
				ResetRunCacheForTest(t) // the seeding wrote through no cache, but be explicit

				var wg sync.WaitGroup
				var mu sync.Mutex
				var failures []string
				readers := 1 + rand.Intn(6)
				for r := 0; r < readers; r++ {
					wg.Add(1)
					go func(r int) {
						defer wg.Done()
						order := rand.Perm(len(keys))
						if r%3 == 2 {
							got, err := cache.List(context.Background(), ns)
							mu.Lock()
							if err != nil {
								failures = append(failures, "list error: "+err.Error())
							} else {
								n := 0
								for _, g := range got {
									if g == keys[0] || g == keys[1] {
										n++
									}
								}
								if n != 2 {
									failures = append(failures, fmt.Sprintf("list named %d of 2 record keys: %v", n, got))
								}
							}
							mu.Unlock()
						}
						for _, i := range order {
							_, _, exists, err := cache.Get(context.Background(), keys[i])
							mu.Lock()
							if err != nil {
								failures = append(failures, "get error: "+err.Error())
							} else if !exists {
								failures = append(failures, "ABSENT: "+keys[i])
							}
							mu.Unlock()
						}
					}(r)
				}
				wg.Wait()
				server.Close()
				if len(failures) > 0 {
					bad++
					t.Errorf("iteration %d (readers=%d maxDelay=%s): %v", it, readers, maxDelay, failures)
				}
			}
			t.Logf("parallelism=%d: %d iterations, %d with a record missing or an error", parallelism, iters, bad)
		})
	}
}
