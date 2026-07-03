package support

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/brienze1/lyrebird/internal/domain"
	"github.com/brienze1/lyrebird/internal/usecase"
)

// spyState carries spy_record.feature's own fixtures on top of the shared
// appState (which owns "Lyrebird boots" et al.).
type spyState struct {
	s *appState

	fakeUpstreams    []*FakeUpstream // every fake upstream created this scenario, for cleanup
	lastFakeUpstream *FakeUpstream

	lastResp      *http.Response
	lastRespBody  []byte
	lastPartition string
}

func (t *spyState) newFakeUpstream() *FakeUpstream {
	fu := NewFakeUpstream()
	t.fakeUpstreams = append(t.fakeUpstreams, fu)
	t.lastFakeUpstream = fu
	return fu
}

func (t *spyState) aFakeUpstreamServerThatRespondsWithBodyAndHeader(status int, body, headerLine string) error {
	fu := t.newFakeUpstream()
	headers := map[string]string{}
	if headerLine != "" {
		name, value, ok := strings.Cut(headerLine, ":")
		if !ok {
			return fmt.Errorf("malformed header %q, want \"Name: value\"", headerLine)
		}
		headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	fu.SetResponse(status, []byte(body), headers)
	return nil
}

func (t *spyState) aFakeUpstreamServerThatHangsFor(d string) error {
	dur, err := time.ParseDuration(d)
	if err != nil {
		return fmt.Errorf("parse hang duration %q: %w", d, err)
	}
	t.newFakeUpstream().HangFor(dur)
	return nil
}

func (t *spyState) aFakeUpstreamServerThatEchoesTheRequestBodyItReceives() error {
	t.newFakeUpstream().EchoRequestBody()
	return nil
}

func (t *spyState) anUpstreamConfiguredInPartitionPointingAtTheFakeUpstream(ctx context.Context, matchHost, partition string) error {
	if t.lastFakeUpstream == nil {
		return fmt.Errorf("no fake upstream server has been set up yet")
	}
	return t.s.app.Store.SetUpstream(ctx, domain.Upstream{
		Partition: partition, MatchHost: matchHost, TargetURL: t.lastFakeUpstream.URL(),
	})
}

func (t *spyState) anUpstreamConfiguredInPartitionPointingAtAClosedPort(ctx context.Context, matchHost, partition string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("reserve a port: %w", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		return fmt.Errorf("close reserved port: %w", err)
	}
	return t.s.app.Store.SetUpstream(ctx, domain.Upstream{
		Partition: partition, MatchHost: matchHost, TargetURL: "http://" + addr,
	})
}

func (t *spyState) sendRequest(ctx context.Context, method, path, host, partition string, body []byte) error {
	url := fmt.Sprintf("http://%s%s", t.s.app.DataAddr(), path)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Host = host
	if partition != "" {
		req.Header.Set("X-Lyrebird-Space", partition)
		t.lastPartition = partition
	} else {
		t.lastPartition = "default"
	}

	client := &http.Client{Timeout: 5 * time.Second} // client-side bound so a Lyrebird-side hang fails the test fast, not after go test's own suite timeout
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	t.lastResp, t.lastRespBody = resp, respBody
	return nil
}

func (t *spyState) iSendAGETRequestToOnTheDataPlaneWithHost(ctx context.Context, path, host string) error {
	return t.sendRequest(ctx, http.MethodGet, path, host, "", nil)
}

func (t *spyState) iSendAGETRequestToOnTheDataPlaneWithHostInPartition(ctx context.Context, path, host, partition string) error {
	return t.sendRequest(ctx, http.MethodGet, path, host, partition, nil)
}

func (t *spyState) iSendAPOSTRequestToOnTheDataPlaneWithHostAndABodyOfBytes(ctx context.Context, path, host string, n int) error {
	return t.sendRequest(ctx, http.MethodPost, path, host, "", bytes.Repeat([]byte("x"), n))
}

func (t *spyState) theResponseStatusIs(want int) error {
	if t.lastResp.StatusCode != want {
		return fmt.Errorf("response status = %d, want %d (body: %s)", t.lastResp.StatusCode, want, t.lastRespBody)
	}
	return nil
}

func (t *spyState) theResponseBodyIs(want string) error {
	if string(t.lastRespBody) != want {
		return fmt.Errorf("response body = %q, want %q", t.lastRespBody, want)
	}
	return nil
}

func (t *spyState) theResponseBodyContains(want string) error {
	if !strings.Contains(string(t.lastRespBody), want) {
		return fmt.Errorf("response body = %q, want it to contain %q", t.lastRespBody, want)
	}
	return nil
}

func (t *spyState) theResponseHeaderIs(name, want string) error {
	got := t.lastResp.Header.Get(name)
	if got != want {
		return fmt.Errorf("response header %q = %q, want %q", name, got, want)
	}
	return nil
}

func (t *spyState) theFakeUpstreamReceivedABodyOfBytes(want int) error {
	got := t.lastFakeUpstream.LastReceivedBodyLen()
	if got != want {
		return fmt.Errorf("fake upstream received %d bytes, want %d", got, want)
	}
	return nil
}

// lastTraffic fetches the most recently recorded traffic entry in the
// partition the last request was sent to. Each scenario in this feature
// sends exactly one data-plane request, so "the most recent record" and
// "the record for that request" are the same thing — ListTraffic already
// orders newest-first.
func (t *spyState) lastTraffic(ctx context.Context) (domain.TrafficRecord, error) {
	list, err := t.s.app.Store.ListTraffic(ctx, t.lastPartition, usecase.TrafficFilter{})
	if err != nil {
		return domain.TrafficRecord{}, fmt.Errorf("list traffic: %w", err)
	}
	if len(list) == 0 {
		return domain.TrafficRecord{}, fmt.Errorf("no traffic recorded in partition %q", t.lastPartition)
	}
	return list[0], nil
}

func (t *spyState) theRecordedTrafficForThatRequestHasDecision(ctx context.Context, want string) error {
	tr, err := t.lastTraffic(ctx)
	if err != nil {
		return err
	}
	if string(tr.Decision) != want {
		return fmt.Errorf("recorded decision = %q, want %q", tr.Decision, want)
	}
	return nil
}

func (t *spyState) theRecordedTrafficResponseBodyIs(ctx context.Context, want string) error {
	tr, err := t.lastTraffic(ctx)
	if err != nil {
		return err
	}
	msg, err := usecase.DecodeRecordedMessage(tr.Response)
	if err != nil {
		return fmt.Errorf("decode recorded response: %w", err)
	}
	if string(msg.Body) != want {
		return fmt.Errorf("recorded response body = %q, want %q", msg.Body, want)
	}
	return nil
}

func (t *spyState) theRecordedTrafficRequestBodyIsTruncated(ctx context.Context) error {
	tr, err := t.lastTraffic(ctx)
	if err != nil {
		return err
	}
	msg, err := usecase.DecodeRecordedMessage(tr.Request)
	if err != nil {
		return fmt.Errorf("decode recorded request: %w", err)
	}
	if !msg.BodyTruncated {
		return fmt.Errorf("recorded request body_truncated = false, want true")
	}
	return nil
}

func (t *spyState) theRecordedTrafficRequestBodyTotalSizeIs(ctx context.Context, want int64) error {
	tr, err := t.lastTraffic(ctx)
	if err != nil {
		return err
	}
	msg, err := usecase.DecodeRecordedMessage(tr.Request)
	if err != nil {
		return fmt.Errorf("decode recorded request: %w", err)
	}
	if msg.BodyTotalSize != want {
		return fmt.Errorf("recorded request body_total_size = %d, want %d", msg.BodyTotalSize, want)
	}
	return nil
}

// RegisterSpySteps wires spy_record.feature's steps against the shared
// appState s.
func RegisterSpySteps(sc *godog.ScenarioContext, s *appState) {
	t := &spyState{s: s}

	sc.Step(`^a fake upstream server that responds (\d+) with body "([^"]*)" and header "([^"]*)"$`,
		t.aFakeUpstreamServerThatRespondsWithBodyAndHeader)
	sc.Step(`^a fake upstream server that hangs for "([^"]*)"$`, t.aFakeUpstreamServerThatHangsFor)
	sc.Step(`^a fake upstream server that echoes the request body it receives$`, t.aFakeUpstreamServerThatEchoesTheRequestBodyItReceives)
	sc.Step(`^an upstream "([^"]*)" configured in partition "([^"]*)" pointing at the fake upstream$`,
		t.anUpstreamConfiguredInPartitionPointingAtTheFakeUpstream)
	sc.Step(`^an upstream "([^"]*)" configured in partition "([^"]*)" pointing at a fake upstream$`,
		func(ctx context.Context, matchHost, partition string) error {
			if err := t.aFakeUpstreamServerThatRespondsWithBodyAndHeader(http.StatusOK, "", ""); err != nil {
				return err
			}
			return t.anUpstreamConfiguredInPartitionPointingAtTheFakeUpstream(ctx, matchHost, partition)
		})
	sc.Step(`^an upstream "([^"]*)" configured in partition "([^"]*)" pointing at a closed port$`,
		t.anUpstreamConfiguredInPartitionPointingAtAClosedPort)

	sc.Step(`^I send a GET request to "([^"]*)" on the data plane with host "([^"]*)"$`, t.iSendAGETRequestToOnTheDataPlaneWithHost)
	sc.Step(`^I send a GET request to "([^"]*)" on the data plane with host "([^"]*)" in partition "([^"]*)"$`,
		t.iSendAGETRequestToOnTheDataPlaneWithHostInPartition)
	sc.Step(`^I send a POST request to "([^"]*)" on the data plane with host "([^"]*)" and a body of (\d+) bytes$`,
		t.iSendAPOSTRequestToOnTheDataPlaneWithHostAndABodyOfBytes)

	sc.Step(`^the response status is (\d+)$`, t.theResponseStatusIs)
	sc.Step(`^the response body is "([^"]*)"$`, t.theResponseBodyIs)
	sc.Step(`^the response body contains "([^"]*)"$`, t.theResponseBodyContains)
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, t.theResponseHeaderIs)
	sc.Step(`^the fake upstream received a body of (\d+) bytes$`, t.theFakeUpstreamReceivedABodyOfBytes)

	sc.Step(`^the recorded traffic for that request has decision "([^"]*)"$`, t.theRecordedTrafficForThatRequestHasDecision)
	sc.Step(`^the recorded traffic response body is "([^"]*)"$`, t.theRecordedTrafficResponseBodyIs)
	sc.Step(`^the recorded traffic request body is truncated$`, t.theRecordedTrafficRequestBodyIsTruncated)
	sc.Step(`^the recorded traffic request body_total_size is (\d+)$`, t.theRecordedTrafficRequestBodyTotalSizeIs)

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		for _, fu := range t.fakeUpstreams {
			fu.Close()
		}
		return ctx, nil
	})
}
