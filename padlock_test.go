package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestPadlockPlan(t *testing.T) {
	mac := []byte{1, 2, 3, 4, 5, 6}
	mid := []byte("machine-id-hash")
	stored := []byte("saved-padlock")
	hasID := func(string) ([]byte, error) { return mid, nil }
	noID := func(string) ([]byte, error) { return nil, errors.New("no machine id") }

	// Locked, with a machine ID: the machine ID, and a MAC database moves to it.
	target, fallbacks, _, _, note := planDBPadlock(nil, hasID, mac)
	if !bytes.Equal(target, mid) || len(fallbacks) != 1 || !bytes.Equal(fallbacks[0], mac) || note != "" {
		t.Errorf("locked: target %q fallbacks %q note %q", target, fallbacks, note)
	}

	// Locked, no machine ID: stays on the MAC, and says so.
	target, fallbacks, _, _, note = planDBPadlock(nil, noID, mac)
	if !bytes.Equal(target, mac) || len(fallbacks) != 0 || note == "" {
		t.Errorf("no id: target %q fallbacks %q note %q", target, fallbacks, note)
	}

	// Lock off: the saved padlock; machine ID and MAC only open and move.
	target, fallbacks, names, _, _ := planDBPadlock(stored, hasID, mac)
	if !bytes.Equal(target, stored) || len(fallbacks) != 2 || !bytes.Equal(fallbacks[0], mid) || !bytes.Equal(fallbacks[1], mac) || len(names) != 2 {
		t.Errorf("unlocked: target %q fallbacks %q names %q", target, fallbacks, names)
	}
}
