package usecase

import (
	"context"
	"testing"

	"github.com/brienze1/lyrebird/internal/domain"
)

func recordWithResponse(id, method, path string, status int, response []byte) domain.TrafficRecord {
	return domain.TrafficRecord{ID: id, Method: method, Path: path, Status: status, Response: response}
}

func TestPromoteTrafficReproducesRecordedResponse(t *testing.T) {
	respJSON, err := EncodeRecordedMessage(RecordedMessage{
		Headers: map[string][]string{"X-Test": {"1"}}, Body: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(): %v", err)
	}
	traffic := &fakeTrafficRepo{getResult: recordWithResponse("traffic-1", "GET", "/anything", 200, respJSON)}

	mockCRUD, _ := newCRUD()
	uc := NewPromoteTraffic(traffic, mockCRUD)

	m, err := uc.Execute(context.Background(), PromoteTrafficInput{Partition: "default", TrafficID: "traffic-1", Name: "promoted"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if m.Name != "promoted" {
		t.Errorf("Name = %q, want %q", m.Name, "promoted")
	}
	if m.Match.Method != "GET" || m.Match.Path != "~^/anything$" {
		t.Errorf("Match = %+v, want method=GET path=~^/anything$", m.Match)
	}
	if m.Action.Respond == nil || m.Action.Respond.Status != 200 || string(m.Action.Respond.Body) != "hello" {
		t.Errorf("Action.Respond = %+v, unexpected", m.Action.Respond)
	}
	if m.Action.Respond.Headers["X-Test"] != "1" {
		t.Errorf("Action.Respond.Headers = %+v, want X-Test=1", m.Action.Respond.Headers)
	}
}

func TestPromoteTrafficDefaultsNameToTrafficID(t *testing.T) {
	respJSON, err := EncodeRecordedMessage(RecordedMessage{Body: []byte("ok")})
	if err != nil {
		t.Fatalf("EncodeRecordedMessage(): %v", err)
	}
	traffic := &fakeTrafficRepo{getResult: recordWithResponse("traffic-42", "GET", "/x", 200, respJSON)}

	mockCRUD, _ := newCRUD()
	uc := NewPromoteTraffic(traffic, mockCRUD)

	m, err := uc.Execute(context.Background(), PromoteTrafficInput{Partition: "default", TrafficID: "traffic-42"})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if m.Name != "promoted-traffic-42" {
		t.Errorf("Name = %q, want %q", m.Name, "promoted-traffic-42")
	}
}
