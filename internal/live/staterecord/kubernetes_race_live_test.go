// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

// Claim 32 on the Kubernetes record store: two writers, one record, the
// loser named and nothing clobbered (GitHub issue #1441).
//
// The bucket store proves this in live/smoke/scenarios/two-writers-one-record.sh,
// where a proxy in front of S3 holds each writer's PutObject until both have
// arrived. Until this file the Kubernetes store had only
// [TestKubernetesStoreVersionIsTheResourceVersion], which runs one writer
// after the other: it shows that a stale version is refused, and says
// nothing about two writes that are genuinely in flight at once.
//
// # What makes this a race rather than a sequence
//
// Two goroutines calling Update prove very little. One usually finishes
// before the other has begun, which is a sequence, and a sequence is what
// the existing test already measures. So the writers here go through a
// wrapping [http.RoundTripper] on each one's own rest.Config
// ([wireBarrier]): the first request of each writer's write is PARKED on the
// wire, unanswered, until BOTH writers are parked, and only then are they let
// through one at a time in an order this test chooses. That is the shape
// live/smoke/s3proxy.py's `hold` and `release` give the bucket scenario,
// including "each completing its round trip before the next starts".
//
// A round where the two requests did not overlap is not evidence and is
// counted separately: the barrier gives up after [raceBarrierWait] and the
// round fails saying so, rather than passing on a sequence.
//
// # Where it runs
//
// `just smoke k8s-records-in-the-cluster`, step 10, against the kind cluster
// that scenario creates. There is no useful fake: client-go's fake clientset
// assigns no metadata.resourceVersion, so every conflict here would be
// vacuous against it, and a fake has no wire to park a request on.

// raceBreakEnvVar is the BREAK control, and nothing but this test file reads
// it. With it set, each writer's write is [raceWriter.stockStylePut] - the
// stock kubernetes backend's own Put (internal/backend/remote-state/kubernetes/client.go,
// line 86), which reads the Secret and updates what it read - instead of
// [KubernetesStore.PutIfVersion]. The assertions do not change: the same
// round that one winner and one named conflict pass, two silent successes
// fail, and the failure says both writes landed.
//
// It is an environment variable read in a _test.go file on purpose. The
// bucket scenario's BREAK arm rebuilds choudoufu with `go build -overlay` so
// the corruption never reaches a shipped binary; a test-only writer never
// reaches one either, and needs no build.
const raceBreakEnvVar = "CHOUDOUFU_K8S_RECORD_RACE_BREAK"

const (
	// raceRounds is how many times each case races. Six, alternating the
	// release order, so the loser is not always the writer that arrived
	// second - the same count and the same alternation the bucket scenario
	// runs.
	raceRounds = 6

	// raceBarrierWait bounds every wait in the barrier. Nothing here may
	// block forever: a hang in a smoke step is worse than a failure, and
	// this test runs inside a scenario that has a CI job's clock on it.
	raceBarrierWait = 30 * time.Second

	// raceWriters is how many writers contend. Two is the claim.
	raceWriters = 2
)

// raceWriterName is what the report calls writer i.
func raceWriterName(i int) string { return string(rune('a' + i)) }

// wireBarrier parks record requests on the wire.
//
// One round: every writer's FIRST record request is parked; when all
// [wireBarrier.want] of them are parked, they are released one at a time in
// arrival order or its reverse, and a writer is released only once every
// writer before it in that order has finished its whole write
// ([wireBarrier.finish]). The bucket scenario's proxy releases held PUTs the
// same way, "each completing its round trip before the next starts".
//
// The first request and not every request: a refused conditional write is
// followed by one Get to name the version the store now holds
// ([KubernetesStore.conflictError]), and parking that would deadlock the
// round against a writer that has already been released.
type wireBarrier struct {
	want    int
	timeout time.Duration

	mu       sync.Mutex
	reversed bool
	arrived  []int
	desc     map[int]string
	parkedAt map[int]time.Time
	freedAt  map[int]time.Time
	done     map[int]chan struct{}
	closed   map[int]bool
	allHere  chan struct{}
	overlap  bool
	together time.Duration
}

func newWireBarrier(want int, timeout time.Duration) *wireBarrier {
	b := &wireBarrier{want: want, timeout: timeout}
	b.reset(false)
	return b
}

// reset starts a round. reversed releases the parked requests in the reverse
// of the order they arrived in.
func (b *wireBarrier) reset(reversed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reversed = reversed
	b.arrived = nil
	b.desc = map[int]string{}
	b.parkedAt = map[int]time.Time{}
	b.freedAt = map[int]time.Time{}
	b.done = map[int]chan struct{}{}
	b.closed = map[int]bool{}
	for w := 0; w < b.want; w++ {
		b.done[w] = make(chan struct{})
	}
	b.allHere = make(chan struct{})
	b.overlap = false
	b.together = 0
}

// park holds writer w's request until its turn. The second and later
// requests of the same writer in one round pass straight through.
func (b *wireBarrier) park(w int, desc string) error {
	b.mu.Lock()
	if _, already := b.desc[w]; already {
		b.mu.Unlock()
		return nil
	}
	now := time.Now()
	b.desc[w] = desc
	b.parkedAt[w] = now
	b.arrived = append(b.arrived, w)
	all := b.allHere
	full := len(b.arrived) == b.want
	if full {
		b.overlap = true
		b.together = now.Sub(b.parkedAt[b.arrived[0]])
	}
	b.mu.Unlock()
	if full {
		close(all)
	}

	select {
	case <-all:
	case <-time.After(b.timeout):
		return fmt.Errorf("staterecord: race barrier: writer %s parked %q and waited %s for the other writer, which never arrived: these requests did not overlap, so this round is not a race and is not evidence", raceWriterName(w), desc, b.timeout)
	}

	b.mu.Lock()
	order := append([]int(nil), b.arrived...)
	if b.reversed {
		for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
			order[i], order[j] = order[j], order[i]
		}
	}
	var waitFor []int
	for _, id := range order {
		if id == w {
			break
		}
		waitFor = append(waitFor, id)
	}
	chans := make([]chan struct{}, 0, len(waitFor))
	for _, id := range waitFor {
		chans = append(chans, b.done[id])
	}
	b.mu.Unlock()

	for i, ch := range chans {
		select {
		case <-ch:
		case <-time.After(b.timeout):
			return fmt.Errorf("staterecord: race barrier: writer %s waited %s for writer %s, which was released first and never finished its write", raceWriterName(w), b.timeout, raceWriterName(waitFor[i]))
		}
	}

	b.mu.Lock()
	b.freedAt[w] = time.Now()
	b.mu.Unlock()
	return nil
}

// finish reports that writer w's whole write is over, which is what releases
// the next writer in the round's order.
func (b *wireBarrier) finish(w int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed[w] {
		return
	}
	b.closed[w] = true
	close(b.done[w])
}

// releaseOrder is the order the round's parked requests were let through in,
// which is the order the API server judged them in.
func (b *wireBarrier) releaseOrder() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	order := append([]int(nil), b.arrived...)
	if b.reversed {
		for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
			order[i], order[j] = order[j], order[i]
		}
	}
	return order
}

// roundWire is what one round looked like on the wire: whether every writer
// was parked at once, how long apart their requests arrived, what each one
// was, and how long each sat parked before it was let through. A round that
// did not overlap must not count towards the verdict.
type roundWire struct {
	overlapped bool
	arrivalGap time.Duration
	request    map[int]string
	heldFor    map[int]time.Duration
}

func (b *wireBarrier) wire() roundWire {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := roundWire{
		overlapped: b.overlap,
		arrivalGap: b.together,
		request:    map[int]string{},
		heldFor:    map[int]time.Duration{},
	}
	for id, desc := range b.desc {
		w.request[id] = desc
		if freed, ok := b.freedAt[id]; ok {
			w.heldFor[id] = freed.Sub(b.parkedAt[id])
		}
	}
	return w
}

// racedWriteMarker marks the context of a write this test is racing, so the
// reads it makes to set a round up, and any client the judge uses, are never
// parked.
type racedWriteMarker struct{}

func withRacedWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, racedWriteMarker{}, true)
}

func isRacedWrite(ctx context.Context) bool {
	marked, _ := ctx.Value(racedWriteMarker{}).(bool)
	return marked
}

// barrierTransport is the wire the writers' requests are held at.
type barrierTransport struct {
	next    http.RoundTripper
	writer  int
	barrier *wireBarrier
}

func (t *barrierTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if isRacedWrite(req.Context()) && strings.Contains(req.URL.Path, "/secrets") {
		if err := t.barrier.park(t.writer, req.Method+" "+req.URL.Path); err != nil {
			return nil, err
		}
	}
	return t.next.RoundTrip(req)
}

// raceWriter is one of the contending writers: its own client, its own
// connection to the API server, its own store over the same namespace and
// key prefix as the other's.
type raceWriter struct {
	id      int
	name    string
	store   *KubernetesStore
	secrets corev1client.SecretInterface
}

// put is the write under test. broken swaps in the stock backend's
// read-then-update, which is the BREAK control.
func (w *raceWriter) put(ctx context.Context, key string, payload []byte, version string, broken bool) (string, error) {
	if !broken {
		return w.store.PutIfVersion(ctx, key, payload, version)
	}
	return w.stockStylePut(ctx, key, payload)
}

// stockStylePut is internal/backend/remote-state/kubernetes/client.go's Put
// (line 86) written in this package's terms: read the Secret, put the new
// payload on the object that came back, and Update it. The version that
// update carries is therefore one this call fetched a moment ago, not the
// one its caller planned against, so a writer whose read happens after
// another writer's write overwrites that write and reports success.
//
// It exists only in this file and is reachable only through
// [raceBreakEnvVar]. Nothing in cmd/choudoufu can call it.
func (w *raceWriter) stockStylePut(ctx context.Context, key string, payload []byte) (string, error) {
	compressed, err := compressRecord(payload)
	if err != nil {
		return "", err
	}
	name := w.store.SecretName(key)
	secret, err := w.secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return "", err
		}
		created, createErr := w.secrets.Create(ctx, w.store.buildSecret(ctx, key, compressed), metav1.CreateOptions{})
		if createErr != nil {
			return "", createErr
		}
		return created.ResourceVersion, nil
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[kubernetesPayloadKey] = compressed
	updated, err := w.secrets.Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return "", err
	}
	return updated.ResourceVersion, nil
}

// shortRequest trims the SHA-256 in a record Secret's name so one round's
// line stays readable; the method and the namespace, which are what say
// WHICH request was parked, are untouched.
func shortRequest(request string) string {
	i := strings.Index(request, KubernetesSecretNamePrefix)
	if i < 0 || len(request) < i+len(KubernetesSecretNamePrefix)+12 {
		return request
	}
	return request[:i+len(KubernetesSecretNamePrefix)+12] + "..."
}

// raceTally is what the scenario reads: how many rounds ran, how many of
// them actually overlapped on the wire, how many produced a named conflict,
// and how many produced two winners.
type raceTally struct {
	rounds     int
	overlapped int
	conflicts  int
	clobbers   int
}

func (c *raceTally) add(o raceTally) {
	c.rounds += o.rounds
	c.overlapped += o.overlapped
	c.conflicts += o.conflicts
	c.clobbers += o.clobbers
}

// TestKubernetesTwoWritersOneRecord is claim 32 on this store: two writers
// that read the same version and are held on the wire until both are there
// settle with exactly one winner, a loser told both versions by name, and
// the loser's payload nowhere in the store.
//
// Both cases the store has a conditional write for are raced:
// update-vs-update (two [KubernetesStore.PutIfVersion] calls carrying one
// resourceVersion) and create-vs-create (two [KubernetesStore.PutIfAbsent]
// calls for a key that does not exist yet). update-vs-delete is not here
// because the bucket claim does not race it either; the conformance suite
// covers a conditional delete against a stale version.
func TestKubernetesTwoWritersOneRecord(t *testing.T) {
	kubeconfig := strings.TrimSpace(os.Getenv(kubeconfigEnvVar))
	if kubeconfig == "" {
		t.Skipf("%s is not set. This test needs a real Kubernetes API server: client-go's fake clientset assigns no resourceVersion, and a fake has no wire to hold a request on, so every round here would be vacuous against it. `just smoke k8s-records-in-the-cluster` creates a kind cluster and runs it. A skip here is not a pass.", kubeconfigEnvVar)
	}
	ns := strings.TrimSpace(os.Getenv(namespaceEnvVar))
	if ns == "" {
		t.Fatalf("%s is set and %s is not. The store never creates a namespace, so the caller has to name one it created.", kubeconfigEnvVar, namespaceEnvVar)
	}
	broken := os.Getenv(raceBreakEnvVar) == "1"
	mode := "conditional-write"
	if broken {
		mode = "read-then-update"
		t.Logf("RACE-MODE %s: the BREAK control is on, so each write is the stock backend's read-then-update (client.go:86) instead of a conditional write. The assertions below are unchanged.", mode)
	}

	prefix := "race-" + randomKeySegment(t)
	barrier := newWireBarrier(raceWriters, raceBarrierWait)

	// The judge's own client is unwrapped: nothing it reads is ever parked,
	// so reading the record back can never be part of the race it judges.
	plain, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("reading the kubeconfig at %s: %v", kubeconfig, err)
	}
	plainClient, err := kubernetes.NewForConfig(plain)
	if err != nil {
		t.Fatalf("building the judge's client: %v", err)
	}
	judgeSecrets := plainClient.CoreV1().Secrets(ns)
	if _, err := plainClient.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace %q: %v. The scenario creates it; this test does not, because the store does not either.", ns, err)
	}
	// Nothing is deleted at the end on purpose. The scenario's step reads
	// this namespace after the test and requires the race's own record
	// Secrets to be in it, because a listing of an empty namespace would
	// report "nothing lock-shaped here" without having seen a single object
	// the race wrote. The namespace belongs to one run of that scenario and
	// goes when its kind cluster does.
	judge, err := NewKubernetesStore(KubernetesConfig{Secrets: judgeSecrets, Namespace: ns, KeyPrefix: prefix, Estate: "race"})
	if err != nil {
		t.Fatalf("NewKubernetesStore for the judge: %v", err)
	}

	writers := make([]*raceWriter, raceWriters)
	for i := range writers {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			t.Fatalf("reading the kubeconfig at %s: %v", kubeconfig, err)
		}
		// Longer than the barrier's own wait, so a request the barrier is
		// still holding is never cut off by the client underneath it.
		cfg.Timeout = 2 * raceBarrierWait
		id := i
		cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
			return &barrierTransport{next: rt, writer: id, barrier: barrier}
		})
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatalf("building writer %s's client: %v", raceWriterName(i), err)
		}
		secrets := client.CoreV1().Secrets(ns)
		store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: ns, KeyPrefix: prefix, Estate: "race"})
		if err != nil {
			t.Fatalf("NewKubernetesStore for writer %s: %v", raceWriterName(i), err)
		}
		writers[i] = &raceWriter{id: i, name: raceWriterName(i), store: store, secrets: secrets}
	}
	// The wrapped clients are real clients: if one cannot reach the API
	// server at all, every round below would fail for that reason and none
	// of it would be about the race.
	if _, err := writers[0].secrets.List(context.Background(), metav1.ListOptions{Limit: 1}); err != nil {
		t.Fatalf("writer a cannot list Secrets in %q, so nothing below would measure a race: %v", ns, err)
	}

	var total raceTally
	t.Run("update-vs-update", func(t *testing.T) {
		tally := raceUpdates(t, writers, barrier, judge, broken)
		total.add(tally)
		reportRace(t, "update-vs-update", mode, tally)
	})
	t.Run("create-vs-create", func(t *testing.T) {
		tally := raceCreates(t, writers, barrier, judge, broken)
		total.add(tally)
		reportRace(t, "create-vs-create", mode, tally)
	})
	reportRace(t, "total", mode, total)

	if total.rounds != 2*raceRounds {
		t.Errorf("%d rounds ran and %d were asked for; a short run is not the measurement this claims to be", total.rounds, 2*raceRounds)
	}
	if total.overlapped != total.rounds {
		t.Errorf("%d of %d rounds had both requests on the wire at once; a round whose requests did not overlap is a sequence and proves nothing about a race", total.overlapped, total.rounds)
	}
}

// reportRace prints the one line the scenario reads. It is deliberately
// machine-readable and deliberately printed in both arms: the BREAK arm's
// evidence is this line saying clobbers=6.
func reportRace(t *testing.T, name, mode string, c raceTally) {
	t.Helper()
	t.Logf("RACE-SUMMARY case=%s mode=%s rounds=%d overlapped=%d conflicts=%d clobbers=%d",
		name, mode, c.rounds, c.overlapped, c.conflicts, c.clobbers)
}

// raceUpdates runs update-vs-update: one record, both writers holding the
// version they read before the round, both writes in flight at once.
func raceUpdates(t *testing.T, writers []*raceWriter, barrier *wireBarrier, judge *KubernetesStore, broken bool) raceTally {
	ctx := context.Background()
	key := "tofu-records/race/terraform_data/one"
	if _, err := judge.PutIfAbsent(ctx, key, []byte("seed")); err != nil {
		t.Fatalf("seeding the record: %v", err)
	}

	var tally raceTally
	for round := 1; round <= raceRounds; round++ {
		// Both writers read the record, and both must come away with the
		// same version: that is what "planned against the same version"
		// means, and the round is not a race without it.
		versions := make([]string, len(writers))
		for i, w := range writers {
			_, version, exists, err := w.store.Get(ctx, key)
			if err != nil || !exists {
				t.Fatalf("[update round %d] writer %s could not read the record it is about to write: %v (exists=%v)", round, w.name, err, exists)
			}
			versions[i] = version
		}
		for i := range versions {
			// Non-empty first: "" is the interface's absent sentinel, and two
			// empty strings comparing equal would read here as two writers
			// agreeing on a version neither of them has.
			if versions[i] == "" {
				t.Fatalf("[update round %d] writer %s read the record and came away with no version at all", round, writers[i].name)
			}
			if versions[i] != versions[0] {
				t.Fatalf("[update round %d] the writers read different versions (%q and %q), so they were never contending for one version", round, versions[0], versions[i])
			}
		}
		payloads := make([][]byte, len(writers))
		for i, w := range writers {
			payloads[i] = []byte(fmt.Sprintf("from-%s-r%d", w.name, round))
		}

		results := runRound(t, writers, barrier, round, func(w *raceWriter) (string, error) {
			return w.put(withRacedWrite(ctx), key, payloads[w.id], versions[w.id], broken)
		})
		judgeRound(t, judgeArgs{
			caseName:   "update-vs-update",
			round:      round,
			writers:    writers,
			barrier:    barrier,
			judge:      judge,
			key:        key,
			payloads:   payloads,
			wantBefore: versions[0],
			results:    results,
			tally:      &tally,
		})
	}
	return tally
}

// raceCreates runs create-vs-create: a key neither writer has written,
// PutIfAbsent from both at once. The loser is told a record exists and which
// version it is.
func raceCreates(t *testing.T, writers []*raceWriter, barrier *wireBarrier, judge *KubernetesStore, broken bool) raceTally {
	ctx := context.Background()
	var tally raceTally
	for round := 1; round <= raceRounds; round++ {
		key := fmt.Sprintf("tofu-records/race/terraform_data/new-%d", round)
		if _, _, exists, err := judge.Get(ctx, key); err != nil || exists {
			t.Fatalf("[create round %d] the key this round creates already holds a record (exists=%v, err=%v)", round, exists, err)
		}
		payloads := make([][]byte, len(writers))
		for i, w := range writers {
			payloads[i] = []byte(fmt.Sprintf("created-by-%s-r%d", w.name, round))
		}

		results := runRound(t, writers, barrier, round, func(w *raceWriter) (string, error) {
			if broken {
				return w.stockStylePut(withRacedWrite(ctx), key, payloads[w.id])
			}
			return w.store.PutIfAbsent(withRacedWrite(ctx), key, payloads[w.id])
		})
		judgeRound(t, judgeArgs{
			caseName:   "create-vs-create",
			round:      round,
			writers:    writers,
			barrier:    barrier,
			judge:      judge,
			key:        key,
			payloads:   payloads,
			wantBefore: "",
			results:    results,
			tally:      &tally,
		})
	}
	return tally
}

// roundResult is one writer's answer.
type roundResult struct {
	version string
	err     error
}

// runRound resets the barrier, starts every writer at once and waits for all
// of them. Every wait is bounded: the barrier gives up on its own, and this
// gives up after that so a stuck writer fails the round instead of hanging
// the scenario that runs it.
func runRound(t *testing.T, writers []*raceWriter, barrier *wireBarrier, round int, write func(*raceWriter) (string, error)) []roundResult {
	t.Helper()
	barrier.reset(round%2 == 0)
	results := make([]roundResult, len(writers))
	var wg sync.WaitGroup
	for _, w := range writers {
		wg.Add(1)
		go func(w *raceWriter) {
			defer wg.Done()
			// Deferred, and registered after wg.Done so it runs before it:
			// the next writer in the round's order is waiting on this, and
			// a writer that panicked would otherwise leave the others
			// parked until their own timeouts ran out.
			defer barrier.finish(w.id)
			version, err := write(w)
			results[w.id] = roundResult{version: version, err: err}
		}(w)
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(4 * raceBarrierWait):
		t.Fatalf("[round %d] a writer was still running %s after the round started; the barrier is supposed to bound every wait it makes", round, 4*raceBarrierWait)
	}
	return results
}

type judgeArgs struct {
	caseName   string
	round      int
	writers    []*raceWriter
	barrier    *wireBarrier
	judge      *KubernetesStore
	key        string
	payloads   [][]byte
	wantBefore string
	results    []roundResult
	tally      *raceTally
}

// judgeRound is the whole claim, applied to one round, and it is the same in
// both arms. Exactly one write lands; the other comes back as a
// *VersionConflictError naming the version it expected and the version the
// store holds; and the record holds the winner's payload and not a byte of
// the loser's.
func judgeRound(t *testing.T, a judgeArgs) {
	t.Helper()
	ctx := context.Background()
	a.tally.rounds++

	wire := a.barrier.wire()
	order := a.barrier.releaseOrder()
	if !wire.overlapped || len(order) != len(a.writers) {
		t.Errorf("[%s round %d] the writers' requests were not on the wire together (parked: %v); this round is a sequence and is not evidence of a race", a.caseName, a.round, wire.request)
		return
	}
	a.tally.overlapped++
	first := order[0]

	recorded, _, _, err := a.judge.Get(ctx, a.key)
	if err != nil {
		t.Fatalf("[%s round %d] reading the record back: %v", a.caseName, a.round, err)
	}

	var landed, refused []int
	for _, w := range a.writers {
		if a.results[w.id].err == nil {
			landed = append(landed, w.id)
		} else {
			refused = append(refused, w.id)
		}
	}

	orderNames := make([]string, len(order))
	for i, id := range order {
		orderNames[i] = raceWriterName(id)
	}
	// One line per round, and it carries the two numbers that say this was a
	// race: the gap between the two arrivals, and how long each request sat
	// on the wire unanswered. Neither writer is released until both are
	// parked, so the first one's hold covers the second one's arrival.
	var held []string
	for _, id := range order {
		held = append(held, fmt.Sprintf("%s:%s", raceWriterName(id), wire.heldFor[id].Round(time.Millisecond)))
	}
	t.Logf("RACE-ROUND case=%s round=%d released=%s request=%q arrival_gap=%s parked_for=%s landed=%d record=%q",
		a.caseName, a.round, strings.Join(orderNames, ","),
		shortRequest(wire.request[order[0]]), wire.arrivalGap.Round(time.Millisecond),
		strings.Join(held, ","), len(landed), string(recorded))

	if len(landed) == len(a.writers) {
		a.tally.clobbers++
		t.Errorf("[%s round %d] BOTH writers reported success against one version: writer %s wrote %q and writer %s wrote %q, both said it worked, and the record now holds %q. One write silently replaced the other and neither writer was told.",
			a.caseName, a.round,
			a.writers[order[0]].name, a.payloads[order[0]],
			a.writers[order[1]].name, a.payloads[order[1]], recorded)
		return
	}
	if len(landed) == 0 {
		t.Errorf("[%s round %d] both writers were refused; exactly one write must land: %s=%v %s=%v",
			a.caseName, a.round, a.writers[0].name, a.results[0].err, a.writers[1].name, a.results[1].err)
		return
	}

	winner, loser := landed[0], refused[0]
	if winner != first {
		t.Errorf("[%s round %d] the request released first was writer %s's and the write that landed was writer %s's",
			a.caseName, a.round, raceWriterName(first), a.writers[winner].name)
	}

	var conflict *VersionConflictError
	if !errors.As(a.results[loser].err, &conflict) {
		t.Errorf("[%s round %d] writer %s was refused with %v (%T), and a loser must be told by name: want a *VersionConflictError",
			a.caseName, a.round, a.writers[loser].name, a.results[loser].err, a.results[loser].err)
		return
	}
	switch {
	case conflict.ExpectedVersion != a.wantBefore:
		t.Errorf("[%s round %d] the conflict names expected version %q; writer %s planned against %q",
			a.caseName, a.round, conflict.ExpectedVersion, a.writers[loser].name, a.wantBefore)
	case conflict.ActualVersion != a.results[winner].version:
		t.Errorf("[%s round %d] the conflict says the store holds version %q; the write that landed produced %q",
			a.caseName, a.round, conflict.ActualVersion, a.results[winner].version)
	case conflict.ActualVersion == "":
		t.Errorf("[%s round %d] the conflict names no version the store holds, so the loser is not told what it collided with", a.caseName, a.round)
	case conflict.ActualVersion == conflict.ExpectedVersion:
		t.Errorf("[%s round %d] the conflict names one version twice (%q), which tells the loser nothing", a.caseName, a.round, conflict.ActualVersion)
	case conflict.Key != a.key:
		t.Errorf("[%s round %d] the conflict names key %q, not %q", a.caseName, a.round, conflict.Key, a.key)
	default:
		// The message an operator sees has to carry the versions too, not
		// just the struct a caller can read them off.
		text := conflict.Error()
		if !strings.Contains(text, conflict.ActualVersion) {
			t.Errorf("[%s round %d] the conflict's message does not carry the version the store holds: %s", a.caseName, a.round, text)
		} else {
			a.tally.conflicts++
		}
	}

	if got, want := string(recorded), string(a.payloads[winner]); got != want {
		t.Errorf("[%s round %d] the record holds %q and the write that landed was %q", a.caseName, a.round, got, want)
	}
	if string(recorded) == string(a.payloads[loser]) {
		t.Errorf("[%s round %d] the record holds the REFUSED writer's payload %q: the loser was told it failed and its write landed anyway", a.caseName, a.round, recorded)
	}
}
