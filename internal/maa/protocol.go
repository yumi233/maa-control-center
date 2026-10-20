// Package maa models the official GUI remote protocol independently of MaaCore.
// It is tested with a mock only; production HTTP endpoints stay locked in v0.1.
package maa

import (
	"encoding/json"
	"errors"
)

type Identity struct {
	User   string `json:"user"`
	Device string `json:"device"`
}
type Task struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}
type Response struct {
	Tasks []Task `json:"tasks"`
}
type Report struct {
	Identity
	Task    string `json:"task"`
	Status  string `json:"status"`
	Payload string `json:"payload"`
}

var allowed = map[string]bool{"LinkStart": true, "LinkStart-Base": true, "LinkStart-Recruiting": true, "StopTask": true, "HeartBeat": true, "CaptureImageNow": true}

func Validate(t Task) error {
	if t.ID == "" || len(t.ID) > 128 || !allowed[t.Type] {
		return errors.New("unsupported or unidentified remote task")
	}
	return nil
}
func ParseReport(b []byte) (Report, error) {
	var r Report
	if len(b) > 2<<20 {
		return r, errors.New("report exceeds limit")
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Task == "" || (r.Status != "SUCCESS" && r.Status != "FAILED") {
		return r, errors.New("invalid report")
	}
	return r, nil
}

// Classify never confuses protocol acceptance with a verified game result.
func Classify(task Task, r Report) string {
	if r.Task != task.ID {
		return "unknown"
	}
	if r.Status == "FAILED" {
		return "failed"
	}
	if task.Type == "StopTask" {
		return "stopping"
	}
	if task.Type == "HeartBeat" {
		if r.Payload == "" {
			return "idle"
		}
		return "running"
	}
	if task.Type == "CaptureImageNow" && r.Payload != "" {
		return "received"
	}
	return "unknown"
}
