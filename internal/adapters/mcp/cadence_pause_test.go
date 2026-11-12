package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

type fakeCadencePause struct {
	calls []string
	err   error
}

func (f *fakeCadencePause) Pause(_ context.Context, in usecase.CadencePauseInput) error {
	f.calls = append(f.calls, "pause:"+in.Partition+"/"+in.Endpoint)
	return f.err
}

func (f *fakeCadencePause) Resume(_ context.Context, in usecase.CadencePauseInput) error {
	f.calls = append(f.calls, "resume:"+in.Partition+"/"+in.Endpoint)
	return f.err
}

func TestPauseAndResumeCadenceTools(t *testing.T) {
	fake := &fakeCadencePause{}
	deps := fakeDeps()
	deps.CadencePause = fake
	srv := New(deps)

	res := callTool(t, srv, "pause_cadence", map[string]any{"endpoint": "widget"})
	if res.IsError {
		t.Fatalf("pause_cadence errored: %+v", res.Content)
	}
	res = callTool(t, srv, "resume_cadence", map[string]any{"space": "s1", "endpoint": "widget"})
	if res.IsError {
		t.Fatalf("resume_cadence errored: %+v", res.Content)
	}
	want := []string{"pause:default/widget", "resume:s1/widget"}
	if len(fake.calls) != 2 || fake.calls[0] != want[0] || fake.calls[1] != want[1] {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
}

func TestPauseCadenceToolReportsErrors(t *testing.T) {
	deps := fakeDeps()
	deps.CadencePause = &fakeCadencePause{err: domain.ErrNotFound}
	srv := New(deps)
	res := callTool(t, srv, "pause_cadence", map[string]any{"endpoint": "widget"})
	if txt := errText(t, res); !strings.Contains(strings.ToLower(txt), "not found") {
		t.Errorf("error text = %q, want it to say not found", txt)
	}
}
