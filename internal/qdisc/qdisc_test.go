package qdisc

import (
	"errors"
	"sync"
	"testing"
)

type fakeManager struct {
	mu            sync.Mutex
	links         []LinkInfo
	replaceCalls  []string
	replaceErrors []error
}

func (f *fakeManager) ListLinks() ([]LinkInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]LinkInfo(nil), f.links...)
	return out, nil
}

func (f *fakeManager) ReplaceRootWithPfifoFast(dev string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaceCalls = append(f.replaceCalls, dev)
	if len(f.replaceErrors) > 0 {
		err := f.replaceErrors[0]
		f.replaceErrors = f.replaceErrors[1:]
		return err
	}
	return nil
}

func TestApplyReplacementReplacesFqOnKataTap(t *testing.T) {
	f := &fakeManager{links: []LinkInfo{
		{Name: "eth0", RootQdiscType: "noqueue"},
		{Name: "tap0_kata", RootQdiscType: "fq"},
		{Name: "lo", RootQdiscType: "noqueue"},
	}}
	res, err := Apply(f, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Replaced != 1 {
		t.Errorf("Replaced = %d, want 1", res.Replaced)
	}
	if len(f.replaceCalls) != 1 || f.replaceCalls[0] != "tap0_kata" {
		t.Errorf("replaceCalls = %v, want [tap0_kata]", f.replaceCalls)
	}
}

func TestApplyReplacementSkipsNonFqTap(t *testing.T) {
	f := &fakeManager{links: []LinkInfo{
		{Name: "tap0_kata", RootQdiscType: "pfifo_fast"},
	}}
	res, err := Apply(f, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced != 0 {
		t.Errorf("Replaced = %d, want 0", res.Replaced)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
}

func TestApplyReplacementSkipsNonKataTap(t *testing.T) {
	f := &fakeManager{links: []LinkInfo{
		{Name: "tap0_qemu", RootQdiscType: "fq"},
		{Name: "tap", RootQdiscType: "fq"},
	}}
	res, err := Apply(f, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced != 0 {
		t.Errorf("Replaced = %d, want 0", res.Replaced)
	}
	if len(f.replaceCalls) != 0 {
		t.Errorf("replaceCalls = %v, want []", f.replaceCalls)
	}
}

func TestApplyReplacementDryRun(t *testing.T) {
	f := &fakeManager{links: []LinkInfo{
		{Name: "tap0_kata", RootQdiscType: "fq"},
	}}
	res, err := Apply(f, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced != 0 {
		t.Errorf("Replaced = %d, want 0 (dry-run)", res.Replaced)
	}
	if res.WouldReplace != 1 {
		t.Errorf("WouldReplace = %d, want 1", res.WouldReplace)
	}
	if len(f.replaceCalls) != 0 {
		t.Errorf("dry-run made replace calls: %v", f.replaceCalls)
	}
}

func TestApplyReplacementIsIdempotentInPractice(t *testing.T) {
	f := &fakeManager{links: []LinkInfo{
		{Name: "tap0_kata", RootQdiscType: "fq"},
	}}
	if _, err := Apply(f, false); err != nil {
		t.Fatal(err)
	}
	f.links[0].RootQdiscType = "pfifo_fast"
	res, err := Apply(f, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced != 0 {
		t.Errorf("second call Replaced = %d, want 0", res.Replaced)
	}
	if res.Skipped != 1 {
		t.Errorf("second call Skipped = %d, want 1", res.Skipped)
	}
	if len(f.replaceCalls) != 1 {
		t.Errorf("total replaceCalls = %d across two runs, want 1", len(f.replaceCalls))
	}
}

func TestApplyReplacementPropagatesError(t *testing.T) {
	want := errors.New("boom")
	f := &fakeManager{
		links:         []LinkInfo{{Name: "tap0_kata", RootQdiscType: "fq"}},
		replaceErrors: []error{want},
	}
	_, err := Apply(f, false)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wraps %v", err, want)
	}
}
