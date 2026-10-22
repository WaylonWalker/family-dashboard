package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	travelDecisionNeeded  = "needed"
	travelDecisionNone    = "none"
	travelDecisionPending = "pending"
)

type travelDecision struct {
	State     string    `json:"state"`
	Minutes   int       `json:"minutes,omitempty"`
	Location  string    `json:"location,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type travelDecisionStore struct {
	mu        sync.Mutex
	path      string
	decisions map[string]travelDecision
}

// A calendar UID identifies a recurring series; applying its decision to the
// series keeps weekly events from returning to the review queue every week.
func travelDecisionKey(item calendarItem) string {
	if uid := strings.TrimSpace(item.uid); uid != "" {
		digest := sha256.Sum256([]byte("uid:" + uid))
		return "uid:" + hex.EncodeToString(digest[:])
	}
	identity := normalizeEventTitle(item.title) + "\x00" + item.start.UTC().Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(identity))
	return "event:" + hex.EncodeToString(digest[:])
}

func openTravelDecisionStore(path string) (*travelDecisionStore, error) {
	s := &travelDecisionStore{path: path, decisions: make(map[string]travelDecision)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.decisions); err != nil {
		return nil, fmt.Errorf("decode travel decisions: %w", err)
	}
	if s.decisions == nil {
		s.decisions = make(map[string]travelDecision)
	}
	return s, nil
}

func (s *travelDecisionStore) get(key string) (travelDecision, bool) {
	if s == nil {
		return travelDecision{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.decisions[key]
	return d, ok
}

func (s *travelDecisionStore) set(key, state string, minutes int, location string, now time.Time) error {
	if s == nil || strings.TrimSpace(key) == "" || len(key) > 512 {
		return errors.New("invalid calendar event")
	}
	location = strings.TrimSpace(location)
	if state == travelDecisionNeeded {
		if minutes < 1 || minutes > 360 || len(location) > 100 || strings.ContainsAny(location, "\x00\r\n") {
			return errors.New("travel time must be 1–360 minutes and destination at most 100 characters")
		}
	} else if (state != travelDecisionNone && state != travelDecisionPending) || minutes != 0 || location != "" {
		return errors.New("invalid travel decision")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, hadPrevious := s.decisions[key]
	s.decisions[key] = travelDecision{State: state, Minutes: minutes, Location: location, UpdatedAt: now}
	if err := s.saveLocked(); err != nil {
		if hadPrevious {
			s.decisions[key] = previous
		} else {
			delete(s.decisions, key)
		}
		return err
	}
	return nil
}

func (s *travelDecisionStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.decisions, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".travel-decisions-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
