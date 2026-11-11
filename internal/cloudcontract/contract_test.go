package cloudcontract

import (
	"net/http"
	"testing"
)

// TestClientSide: the checks the client's fakes lean on — a call the
// contract takes without a token must come without one, an optional body
// may be left out, a free-form object is free, and an event stream is
// checked event by event.
func TestClientSide(t *testing.T) {
	s, why := Load("1.4.0")
	if s == nil {
		t.Skip("contract checks skipped: " + why)
	}
	j := http.Header{"Conch-Contract": {"1.4.0"}, "Content-Type": {"application/json"}}
	if errs := s.CheckRequest("POST", "/v1/auth/device", j, []byte(`{"client":"cli","device_name":"x"}`)); len(errs) != 0 {
		t.Errorf("device without a token: %v", errs)
	}
	withTok := http.Header{"Conch-Contract": {"1.4.0"}, "Content-Type": {"application/json"}, "Authorization": {"Bearer x"}}
	if errs := s.CheckRequest("POST", "/v1/auth/device", withTok, []byte(`{"client":"cli","device_name":"x"}`)); len(errs) == 0 {
		t.Error("a token sent to the device flow passed")
	}
	if errs := s.CheckRequest("POST", "/v1/machines/m_aaaaaaaaaaaaaaaa/sleep", http.Header{"Conch-Contract": {"1.4.0"}, "Authorization": {"Bearer x"}}, nil); len(errs) != 0 {
		t.Errorf("sleep without its optional body: %v", errs)
	}
	if errs := s.CheckRequest("POST", "/v1/machines", withTok, nil); len(errs) == 0 {
		t.Error("create without its required body passed")
	}
	if errs := s.CheckRequest("GET", "/v1/machines", http.Header{"Conch-Contract": {"1.4.0"}}, nil); len(errs) == 0 {
		t.Error("machines without a token passed")
	}
	ev := http.Header{"Content-Type": {"text/event-stream"}}
	good := ": hb\n\nid: 1\ndata: {\"type\":\"machine.state\",\"at\":\"2026-11-10T10:00:00Z\",\"data\":{\"state\":\"running\"}}\n\n"
	if errs := s.CheckResponse("GET", "/v1/events", 200, ev, []byte(good)); len(errs) != 0 {
		t.Errorf("good events: %v", errs)
	}
	bad := "data: {\"type\":\"machine.state\"}\n\n"
	if errs := s.CheckResponse("GET", "/v1/events", 200, ev, []byte(bad)); len(errs) == 0 {
		t.Error("an event without at passed")
	}
}
