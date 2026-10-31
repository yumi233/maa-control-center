package model

import (
	"strings"
	"time"
)

type RecruitCombination struct {
	Level int      `json:"level"`
	Tags  []string `json:"tags"`
}

// Recruitment describes a guaranteed tag combination, not a hired operator.
type Recruitment struct {
	ID           string               `json:"id"`
	JobID        string               `json:"job_id"`
	InstanceID   string               `json:"instance_id"`
	Profile      string               `json:"profile"`
	ObservedAt   string               `json:"observed_at"`
	Level        int                  `json:"level"`
	Tags         []string             `json:"tags"`
	Combinations []RecruitCombination `json:"combinations,omitempty"`
}

func (r Recruitment) Valid() bool {
	hex := func(s string) bool {
		if len(s) != 64 {
			return false
		}
		for _, c := range s {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return true
	}
	safe := func(s string) bool { return s != "" && len(s) <= 128 && !strings.ContainsAny(s, "\r\n\x00") }
	if !hex(r.ID) || !hex(r.JobID) || !safe(r.InstanceID) || !safe(r.Profile) || (r.Level != 5 && r.Level != 6) || len(r.Tags) != 5 || len(r.Combinations) > 10 {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, r.ObservedAt); e != nil {
		return false
	}
	tags := map[string]bool{}
	for _, t := range r.Tags {
		if !safe(t) || tags[t] {
			return false
		}
		tags[t] = true
	}
	if r.Level == 6 && !tags["高级资深干员"] {
		return false
	}
	guaranteed := tags["高级资深干员"] || tags["资深干员"]
	for _, c := range r.Combinations {
		if c.Level < 5 || c.Level > r.Level || len(c.Tags) < 1 || len(c.Tags) > 3 {
			return false
		}
		seen := map[string]bool{}
		for _, t := range c.Tags {
			if !tags[t] || seen[t] {
				return false
			}
			seen[t] = true
		}
		if c.Level == r.Level {
			guaranteed = true
		}
	}
	return guaranteed
}
