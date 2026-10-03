// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"reflect"
	"strings"
	"testing"
)

// chain is #1750's fixture shape at N=5: e01 is read by e02, which is read
// by e03 (a chain of depth 2), and e04 and e05 read nothing.
func chain() []Root {
	return []Root{
		{Root: "estates/e03", Estate: "ls-e03", Reads: []Read{{Estate: "ls-e02", From: "data.aws_subnet.upstream"}}},
		{Root: "estates/e01", Estate: "ls-e01"},
		{Root: "estates/e05", Estate: "ls-e05"},
		{Root: "estates/e02", Estate: "ls-e02", Reads: []Read{{Estate: "ls-e01", From: "data.aws_vpc.upstream"}}},
		{Root: "estates/e04", Estate: "ls-e04"},
	}
}

func waveRoots(w *Waves) [][]string {
	var out [][]string
	for _, wave := range w.Waves {
		out = append(out, wave.Roots)
	}
	return out
}

// assertReadersAfterProducers is the property #1754's wave-order criterion
// names, checked edge by edge against the reads given rather than against
// the edges Split reports, so a Split that dropped or reversed an edge
// cannot agree with itself.
func assertReadersAfterProducers(t *testing.T, roots []Root, w *Waves) {
	t.Helper()
	owner := map[string]string{}
	for _, r := range roots {
		owner[r.Estate] = r.Root
	}
	for _, r := range roots {
		for _, rd := range r.Reads {
			p, ok := owner[rd.Estate]
			if !ok || p == r.Root {
				continue
			}
			rw, pw := w.WaveOf[r.Root], w.WaveOf[p]
			if rw == 0 || pw == 0 {
				t.Errorf("%s or %s is in no wave", r.Root, p)
				continue
			}
			if rw < pw {
				t.Errorf("%s reads %s (%s) but lands in wave %d, before its producer %s in wave %d", r.Root, rd.Estate, rd.From, rw, p, pw)
			}
			if rw == pw && !w.Waves[rw-1].Canary {
				t.Errorf("%s and its producer %s share non-canary wave %d", r.Root, p, rw)
			}
		}
	}
}

func TestSplitChainOfDepthTwo(t *testing.T) {
	roots := chain()
	w, err := Split(roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertReadersAfterProducers(t, roots, w)
	want := [][]string{
		{"estates/e01", "estates/e04", "estates/e05"},
		{"estates/e02"},
		{"estates/e03"},
	}
	if got := waveRoots(w); !reflect.DeepEqual(got, want) {
		t.Errorf("waves %v, want %v", got, want)
	}
	if w.Waves[0].Canary {
		t.Error("wave 1 is marked canary with no canaries given")
	}
}

func TestSplitCanariesFormWaveOne(t *testing.T) {
	roots := chain()
	// One canary by directory, one by estate.
	w, err := Split(roots, []string{"estates/e04", "ls-e01"})
	if err != nil {
		t.Fatal(err)
	}
	assertReadersAfterProducers(t, roots, w)
	want := [][]string{
		{"estates/e01", "estates/e04"},
		{"estates/e02", "estates/e05"},
		{"estates/e03"},
	}
	if got := waveRoots(w); !reflect.DeepEqual(got, want) {
		t.Errorf("waves %v, want %v", got, want)
	}
	if !w.Waves[0].Canary || w.Waves[1].Canary {
		t.Errorf("canary flags %v %v, want true false", w.Waves[0].Canary, w.Waves[1].Canary)
	}
}

// TestSplitCanaryChain: two canaries where one reads the other share wave
// 1, and the wave carries the edge so an apply of it can keep the order.
func TestSplitCanaryChain(t *testing.T) {
	roots := chain()
	w, err := Split(roots, []string{"estates/e01", "estates/e02"})
	if err != nil {
		t.Fatal(err)
	}
	assertReadersAfterProducers(t, roots, w)
	if got := w.Waves[0].Roots; !reflect.DeepEqual(got, []string{"estates/e01", "estates/e02"}) {
		t.Fatalf("wave 1 %v", got)
	}
	if len(w.Waves[0].Edges) != 1 || w.Waves[0].Edges[0].Reader != "estates/e02" || w.Waves[0].Edges[0].Producer != "estates/e01" {
		t.Errorf("wave 1's edges %+v, want e02 after e01", w.Waves[0].Edges)
	}
	if w.WaveOf["estates/e03"] != 2 {
		t.Errorf("e03 in wave %d, want 2", w.WaveOf["estates/e03"])
	}
}

func TestSplitRefusesACanaryReadingANonCanary(t *testing.T) {
	_, err := Split(chain(), []string{"estates/e03"})
	if err == nil {
		t.Fatal("a canary reading a non-canary was accepted")
	}
	for _, want := range []string{"canary estates/e03", `"ls-e02"`, "data.aws_subnet.upstream", "estates/e02"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
}

func TestSplitRefusesACycle(t *testing.T) {
	roots := chain()
	// e01 now reads e03, closing e01 <- e02 <- e03 <- e01.
	roots[1].Reads = []Read{{Estate: "ls-e03", From: "data.aws_sqs_queue.back"}}
	_, err := Split(roots, nil)
	if err == nil {
		t.Fatal("a cycle was accepted")
	}
	for _, want := range []string{"cycle", `estates/e01 reads estate "ls-e03" (data.aws_sqs_queue.back)`, `estates/e02 reads estate "ls-e01"`, `estates/e03 reads estate "ls-e02"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
}

func TestSplitExternalAndSelfReads(t *testing.T) {
	roots := chain()
	roots[2].Reads = []Read{
		{Estate: "network-shared", From: "data.aws_vpc.core"},
		{Estate: "ls-e05", From: "data.aws_sqs_queue.self"},
	}
	w, err := Split(roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.WaveOf["estates/e05"] != 1 {
		t.Errorf("a root reading only itself and outside the set is in wave %d, want 1", w.WaveOf["estates/e05"])
	}
	if len(w.External) != 1 || w.External[0] != (External{Root: "estates/e05", Estate: "network-shared", From: "data.aws_vpc.core"}) {
		t.Errorf("external reads %+v", w.External)
	}
}

func TestSplitRefusals(t *testing.T) {
	roots := chain()
	roots[0].Estate = "ls-e01"
	if _, err := Split(roots, nil); err == nil || !strings.Contains(err.Error(), `both own estate "ls-e01"`) {
		t.Errorf("two roots owning one estate: %v", err)
	}
	if _, err := Split(chain(), []string{"estates/e99"}); err == nil || !strings.Contains(err.Error(), `canary "estates/e99" names no root`) {
		t.Errorf("an unknown canary: %v", err)
	}
	dup := append(chain(), chain()[0])
	if _, err := Split(dup, nil); err == nil || !strings.Contains(err.Error(), "estates/e03 twice") {
		t.Errorf("a root twice: %v", err)
	}
}

func TestAttachDigests(t *testing.T) {
	w, err := Split(chain(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byRoot := map[string]string{
		"estates/e01": "sha256:1", "estates/e02": "sha256:2", "estates/e03": "sha256:3",
		"estates/e04": "sha256:4", "estates/e05": "sha256:5",
	}
	if err := w.AttachDigests(byRoot); err != nil {
		t.Fatal(err)
	}
	want, _ := SetDigest([]RootDigestEntry{{"estates/e05", "sha256:5"}, {"estates/e01", "sha256:1"}, {"estates/e04", "sha256:4"}})
	if w.Waves[0].Digest != want {
		t.Errorf("wave 1 digest %s, want the set digest over its three roots %s", w.Waves[0].Digest, want)
	}
	delete(byRoot, "estates/e03")
	if err := w.AttachDigests(byRoot); err == nil || !strings.Contains(err.Error(), "estates/e03") {
		t.Errorf("a wave root with no plan: %v", err)
	}
}
