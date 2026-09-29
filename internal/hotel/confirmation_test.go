package hotel

import (
	"testing"
	"time"
)

func TestNotifierEmitsAndCloses(t *testing.T) {
	emitted := make(chan string, 1)
	notifier := NewNotifier(0, func(reference string) { emitted <- reference })
	notifier.Schedule("booking-reference")

	select {
	case reference := <-emitted:
		if reference != "booking-reference" {
			t.Fatalf("emitted reference = %q", reference)
		}
	case <-time.After(time.Second):
		t.Fatal("notifier did not emit")
	}

	notifier.Close()
	// Scheduling after shutdown is deliberately ignored and must not race with Wait.
	notifier.Schedule("too-late")
}
