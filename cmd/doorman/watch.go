package main

import (
	"context"
	"crypto/sha256"
	"os"
	"time"
)

// watchFile tells changed whenever the file at path settles on content other
// than last, the fingerprint of the config that's running.
// It hashes the file every interval rather than relying on file events,
// which often don't arrive through Docker bind mounts or under WSL.
//
// A change only counts once the content has stayed the same for one more
// check: editors and git can write a file in pieces, and a half-written
// config can still parse, with keys cut off the end.
//
// Take last before loading the config. If the file changes in between, the
// watcher sees the difference and reloads; taken after, that save would be
// missed.
func watchFile(
	ctx context.Context,
	path string,
	last [sha256.Size]byte,
	every time.Duration,
	changed chan<- struct{},
) {
	var pending [sha256.Size]byte // new content waiting to prove it's stable
	var waiting bool

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		current, err := fingerprint(path)
		switch {
		case err != nil: // mid-save, the file can briefly be missing
			continue
		case current == last:
			waiting = false
		case !waiting || current != pending:
			pending, waiting = current, true
		default: // the same new content on two checks in a row
			last, waiting = current, false
			select {
			case changed <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func fingerprint(path string) ([sha256.Size]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}
