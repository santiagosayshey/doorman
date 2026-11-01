package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const tick = 20 * time.Millisecond

func startWatching(t *testing.T, content string) (string, <-chan struct{}) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doorman.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	changed := make(chan struct{}, 10)
	loaded, err := fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	go watchFile(ctx, path, loaded, tick, changed)
	return path, changed
}

func save(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func expectChange(t *testing.T, changed <-chan struct{}, want bool) {
	t.Helper()
	select {
	case <-changed:
		if !want {
			t.Fatal("got a change notification, expected none")
		}
	case <-time.After(10 * tick):
		if want {
			t.Fatal("no change notification after saving new content")
		}
	}
}

func TestSavingNewContentNotifies(t *testing.T) {
	path, changed := startWatching(t, "version: 1\n")
	save(t, path, "version: 1\nlisten: :9000\n")
	expectChange(t, changed, true)
}

func TestSavingTheSameContentDoesNotNotify(t *testing.T) {
	path, changed := startWatching(t, "version: 1\n")
	save(t, path, "version: 1\n")
	expectChange(t, changed, false)
}

func TestNotifiesOncePerSettledChange(t *testing.T) {
	path, changed := startWatching(t, "version: 1\n")
	save(t, path, "a: 1\n")
	expectChange(t, changed, true)
	expectChange(t, changed, false) // nothing more until the file changes again
}

func TestWaitsForTheFileToSettle(t *testing.T) {
	path, changed := startWatching(t, "version: 1\n")

	// Keep changing the file faster than the watcher checks it: a save that
	// is still being written must not trigger a reload.
	stop := time.After(8 * tick)
	for i := 0; ; i++ {
		select {
		case <-stop:
			goto settled
		case <-changed:
			t.Fatal("notified while the file was still changing")
		default:
		}
		save(t, path, "partial: "+string(rune('a'+i%26))+"\n")
		time.Sleep(tick / 3)
	}
settled:
	expectChange(t, changed, true) // once it stops changing, one notification
}

// A save that lands before the watcher starts is still noticed, because
// the watcher compares against the content that was loaded.
func TestSaveBeforeTheWatcherStartsIsNotMissed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doorman.yaml")
	save(t, path, "version: 1\n")
	loaded, err := fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}

	save(t, path, "version: 1\nlisten: :9000\n") // before watching starts

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	changed := make(chan struct{}, 1)
	go watchFile(ctx, path, loaded, tick, changed)
	expectChange(t, changed, true)
}
