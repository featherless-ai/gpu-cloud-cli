package instance

import "testing"

func wantContent(t *testing.T, previous, next, want string) {
	t.Helper()

	if got := NewContent(previous, next); got != want {
		t.Errorf("NewContent(%q, %q) = %q, want %q", previous, next, got, want)
	}
}

func TestNewContentEmitsOnlyAppendedLines(t *testing.T) {
	wantContent(t, "a\nb\n", "a\nb\nc\n", "c\n")
}

func TestNewContentSkipsTheOverlapWhenTheTailWindowSlides(t *testing.T) {
	wantContent(t, "a\nb\nc\nd", "c\nd\ne", "e")
}

func TestNewContentPrintsEverythingAfterAContainerRestart(t *testing.T) {
	wantContent(t, "old output", "fresh start\nready", "fresh start\nready")
}

func TestNewContentEmitsNothingWhenTheTailIsUnchanged(t *testing.T) {
	wantContent(t, "same", "same", "")
}

func TestJobClientDoesNotReplayARetainedTailAfterATemporaryEmptyLiveLogResponse(t *testing.T) {
	previous := "progress-0\nprogress-1\n"
	preserved := NextLogCursor(previous, "", false)

	wantContent(t, preserved, previous, "")
}
