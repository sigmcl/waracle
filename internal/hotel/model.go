package hotel

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrHotelNotFound    = errors.New("hotel not found")
	ErrRoomNotFound     = errors.New("room not found")
	ErrBookingNotFound  = errors.New("booking not found")
	ErrCapacityExceeded = errors.New("room capacity exceeded")
	ErrRoomUnavailable  = errors.New("room unavailable")
	ErrDatabaseNotEmpty = errors.New("database not empty")
)

// Hotel is a property containing the rooms that can be booked.
type Hotel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Room describes one physical room. Capacity, rather than room type, is the
// authoritative limit used when accepting a booking.
type Room struct {
	ID       int64  `json:"id"`
	HotelID  int64  `json:"hotel_id"`
	Number   string `json:"number"`
	Type     string `json:"type"`
	Capacity int    `json:"capacity"`
}

// Booking is the public representation of a confirmed room reservation.
type Booking struct {
	Reference string    `json:"reference"`
	HotelID   int64     `json:"hotel_id"`
	RoomID    int64     `json:"room_id"`
	CheckIn   string    `json:"check_in"`
	CheckOut  string    `json:"check_out"`
	Guests    int       `json:"guests"`
	CreatedAt time.Time `json:"created_at"`
}

// Stay uses check-in-inclusive, checkout-exclusive dates. For example, a stay
// from June 1 to June 2 occupies the room for the night of June 1 only.
type Stay struct {
	CheckIn  time.Time
	CheckOut time.Time
	Guests   int
}

// ParseStay validates API date strings and returns canonical date-only values.
func ParseStay(checkIn, checkOut string, guests int) (Stay, error) {
	if strings.TrimSpace(checkIn) == "" || strings.TrimSpace(checkOut) == "" {
		return Stay{}, errors.New("check_in and check_out are required")
	}
	if guests <= 0 {
		return Stay{}, errors.New("guests must be greater than zero")
	}

	start, err := time.Parse(time.DateOnly, checkIn)
	if err != nil {
		return Stay{}, fmt.Errorf("check_in must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse(time.DateOnly, checkOut)
	if err != nil {
		return Stay{}, fmt.Errorf("check_out must use YYYY-MM-DD: %w", err)
	}
	if !end.After(start) {
		return Stay{}, errors.New("check_out must be after check_in")
	}

	return Stay{CheckIn: start, CheckOut: end, Guests: guests}, nil
}

func formatDate(value time.Time) string {
	return value.Format(time.DateOnly)
}
