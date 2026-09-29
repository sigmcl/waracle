package hotel

import (
	"errors"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestStoreIntegration(t *testing.T) {
	store := newTestStore(t)

	t.Run("availability and booking rules", func(t *testing.T) {
		resetAndSeed(t, store)

		twoGuests := mustStay(t, "2027-06-01", "2027-06-05", 2)
		rooms, err := store.AvailableRooms(t.Context(), 1, twoGuests)
		if err != nil {
			t.Fatalf("find initial availability: %v", err)
		}
		if len(rooms) != 4 {
			t.Fatalf("available rooms = %d, want 4 double/deluxe rooms", len(rooms))
		}

		booking, err := store.CreateBooking(t.Context(), 3, mustStay(t, "2027-06-01", "2027-06-03", 2))
		if err != nil {
			t.Fatalf("create booking: %v", err)
		}
		if len(booking.Reference) != 36 {
			t.Fatalf("reference length = %d, want 36-character UUID", len(booking.Reference))
		}

		if _, err := store.CreateBooking(t.Context(), 1, mustStay(t, "2027-06-01", "2027-06-02", 2)); !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("oversized booking error = %v, want ErrCapacityExceeded", err)
		}

		found, err := store.BookingByReference(t.Context(), booking.Reference)
		if err != nil {
			t.Fatalf("retrieve booking: %v", err)
		}
		if found.RoomID != 3 || found.CheckIn != "2027-06-01" || found.CheckOut != "2027-06-03" {
			t.Fatalf("retrieved booking = %#v, want original room and dates", found)
		}
	})

	t.Run("interval boundaries", func(t *testing.T) {
		tests := []struct {
			name              string
			checkIn, checkOut string
			wantUnavailable   bool
		}{
			{name: "same stay", checkIn: "2027-06-10", checkOut: "2027-06-15", wantUnavailable: true},
			{name: "overlaps start", checkIn: "2027-06-08", checkOut: "2027-06-12", wantUnavailable: true},
			{name: "overlaps end", checkIn: "2027-06-14", checkOut: "2027-06-18", wantUnavailable: true},
			{name: "inside existing", checkIn: "2027-06-11", checkOut: "2027-06-14", wantUnavailable: true},
			{name: "contains existing", checkIn: "2027-06-08", checkOut: "2027-06-18", wantUnavailable: true},
			{name: "adjacent before", checkIn: "2027-06-08", checkOut: "2027-06-10"},
			{name: "adjacent after", checkIn: "2027-06-15", checkOut: "2027-06-18"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				resetAndSeed(t, store)
				if _, err := store.CreateBooking(t.Context(), 3, mustStay(t, "2027-06-10", "2027-06-15", 2)); err != nil {
					t.Fatalf("create existing booking: %v", err)
				}

				_, err := store.CreateBooking(t.Context(), 3, mustStay(t, test.checkIn, test.checkOut, 2))
				if test.wantUnavailable && !errors.Is(err, ErrRoomUnavailable) {
					t.Fatalf("booking error = %v, want ErrRoomUnavailable", err)
				}
				if !test.wantUnavailable && err != nil {
					t.Fatalf("adjacent booking error = %v, want success", err)
				}
			})
		}
	})

	t.Run("availability never combines rooms", func(t *testing.T) {
		resetAndSeed(t, store)

		// Each suitable room is unavailable for at least part of the requested
		// stay. Combining room 3's later nights with room 4's earlier nights
		// would look available, but would require the guests to move rooms.
		bookings := []struct {
			roomID            int64
			checkIn, checkOut string
		}{
			{3, "2027-07-01", "2027-07-03"},
			{4, "2027-07-03", "2027-07-05"},
			{5, "2027-07-01", "2027-07-05"},
			{6, "2027-07-01", "2027-07-05"},
		}
		for _, fixture := range bookings {
			if _, err := store.CreateBooking(t.Context(), fixture.roomID, mustStay(t, fixture.checkIn, fixture.checkOut, 2)); err != nil {
				t.Fatalf("create fixture booking for room %d: %v", fixture.roomID, err)
			}
		}

		rooms, err := store.AvailableRooms(t.Context(), 1, mustStay(t, "2027-07-01", "2027-07-05", 2))
		if err != nil {
			t.Fatalf("find availability: %v", err)
		}
		if len(rooms) != 0 {
			t.Fatalf("available rooms = %#v, want none for the complete stay", rooms)
		}
	})

	t.Run("concurrent overlap has one winner", func(t *testing.T) {
		resetAndSeed(t, store)
		stay := mustStay(t, "2027-08-10", "2027-08-12", 2)

		const requests = 20
		start := make(chan struct{})
		results := make(chan error, requests)
		ctx := t.Context()
		var wg sync.WaitGroup
		for range requests {
			wg.Go(func() {
				<-start
				_, err := store.CreateBooking(ctx, 3, stay)
				results <- err
			})
		}
		close(start)
		wg.Wait()
		close(results)

		succeeded, unavailable := 0, 0
		for err := range results {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrRoomUnavailable):
				unavailable++
			default:
				t.Fatalf("unexpected booking error: %v", err)
			}
		}
		if succeeded != 1 || unavailable != requests-1 {
			t.Fatalf("results: %d succeeded, %d unavailable; want 1 and %d", succeeded, unavailable, requests-1)
		}
		var stored int
		if err := store.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM bookings`).Scan(&stored); err != nil {
			t.Fatalf("count stored bookings: %v", err)
		}
		if stored != 1 {
			t.Fatalf("stored bookings = %d, want exactly 1", stored)
		}
	})

	t.Run("administration is predictable", func(t *testing.T) {
		resetAndSeed(t, store)
		if err := store.Seed(t.Context()); !errors.Is(err, ErrDatabaseNotEmpty) {
			t.Fatalf("repeat seed error = %v, want ErrDatabaseNotEmpty", err)
		}
		if err := store.Reset(t.Context()); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if err := store.Reset(t.Context()); err != nil {
			t.Fatalf("repeat reset: %v", err)
		}
		if err := store.Seed(t.Context()); err != nil {
			t.Fatalf("seed after reset: %v", err)
		}
	})
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	container, err := postgres.Run(t.Context(),
		"postgres:18-alpine",
		postgres.WithDatabase("waracle_test"),
		postgres.WithUsername("waracle"),
		postgres.WithPassword("waracle"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	databaseURL, err := container.ConnectionString(t.Context(), "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}
	if err := Migrate(databaseURL); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	store, err := OpenStore(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

func resetAndSeed(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Reset(t.Context()); err != nil {
		t.Fatalf("reset database: %v", err)
	}
	if err := store.Seed(t.Context()); err != nil {
		t.Fatalf("seed database: %v", err)
	}
}

func mustStay(t *testing.T, checkIn, checkOut string, guests int) Stay {
	t.Helper()
	stay, err := ParseStay(checkIn, checkOut, guests)
	if err != nil {
		t.Fatalf("parse stay: %v", err)
	}
	return stay
}
