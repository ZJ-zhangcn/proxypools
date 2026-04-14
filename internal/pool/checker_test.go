package pool_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/pool"
)

type fakeSwitcher struct {
	group string
	name  string
}

func (f *fakeSwitcher) SwitchSelector(group, name string) error {
	f.group = group
	f.name = name
	return nil
}

func TestProbeNodeMeasuresLatencyAndSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	switcher := &fakeSwitcher{}
	checker := pool.NewChecker(pool.CheckerConfig{ProbeURL: ts.URL, SelectorSwitcher: switcher})
	status, err := checker.ProbeNode("node-1", model.NodeRuntimeStatus{NodeID: 1, State: "active"})
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if switcher.group != "health-check" || switcher.name != "node-1" {
		t.Fatalf("expected health-check selector switch to node-1, got %s/%s", switcher.group, switcher.name)
	}
	if status.LatencyMS <= 0 {
		t.Fatalf("expected positive latency, got %d", status.LatencyMS)
	}
	if status.RecentSuccessRate != 1 {
		t.Fatalf("expected success rate 1, got %v", status.RecentSuccessRate)
	}
}
