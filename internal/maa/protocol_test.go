package maa

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOfficialProtocolMock(t *testing.T) {
	task := Task{ID: "test-stable-id", Type: "LinkStart-Base"}
	if err := Validate(task); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(Response{Tasks: []Task{task}}) }))
	defer srv.Close()
	var first, second Response
	for _, out := range []*Response{&first, &second} {
		r, err := http.Post(srv.URL, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.NewDecoder(r.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
	if first.Tasks[0].ID != second.Tasks[0].ID {
		t.Fatal("retry regenerated task ID")
	}
	report, err := ParseReport([]byte(`{"user":"example","device":"example-device","task":"test-stable-id","status":"SUCCESS","payload":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if Classify(task, report) != "unknown" {
		t.Fatal("SUCCESS incorrectly proves game success")
	}
	stop := Task{ID: "stop", Type: "StopTask"}
	if Classify(stop, Report{Task: "stop", Status: "SUCCESS"}) != "stopping" {
		t.Fatal("stop request incorrectly confirmed stopped")
	}
	if Classify(Task{ID: "hb", Type: "HeartBeat"}, Report{Task: "hb", Status: "SUCCESS", Payload: ""}) != "idle" {
		t.Fatal("heartbeat state incorrect")
	}
	if Validate(Task{ID: "x", Type: "Toolbox-GachaTenTimes"}) == nil {
		t.Fatal("unsafe task allowed")
	}
}
