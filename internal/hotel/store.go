package hotel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns all database access. PostgreSQL's bookings exclusion constraint,
// rather than an in-process lock, is the final protection against double
// booking and therefore remains correct across multiple API processes.
type Store struct {
	pool *pgxpool.Pool
}

// OpenStore creates and verifies a PostgreSQL connection pool.
func OpenStore(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases all PostgreSQL connections held by the store.
func (s *Store) Close() {
	s.pool.Close()
}

// FindHotels returns hotels whose names exactly match name, ignoring case.
func (s *Store) FindHotels(ctx context.Context, name string) ([]Hotel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name
		FROM hotels
		WHERE LOWER(name) = LOWER($1)
		ORDER BY id`, strings.TrimSpace(name))
	if err != nil {
		return nil, fmt.Errorf("find hotels: %w", err)
	}
	defer rows.Close()

	hotels := make([]Hotel, 0)
	for rows.Next() {
		var hotel Hotel
		if err := rows.Scan(&hotel.ID, &hotel.Name); err != nil {
			return nil, fmt.Errorf("scan hotel: %w", err)
		}
		hotels = append(hotels, hotel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hotels: %w", err)
	}
	return hotels, nil
}

// AvailableRooms returns rooms that can hold the whole party and are free for
// every night of the requested stay. It never combines partial availability
// from different rooms.
func (s *Store) AvailableRooms(ctx context.Context, hotelID int64, stay Stay) ([]Room, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hotels WHERE id = $1)`, hotelID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check hotel: %w", err)
	}
	if !exists {
		return nil, ErrHotelNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.hotel_id, r.number, r.type, r.capacity
		FROM rooms AS r
		WHERE r.hotel_id = $1
		  AND r.capacity >= $2
		  AND NOT EXISTS (
		      SELECT 1
		      FROM bookings AS b
		      WHERE b.room_id = r.id
		        AND b.check_in < $4
		        AND b.check_out > $3
		  )
		ORDER BY r.id`, hotelID, stay.Guests, stay.CheckIn, stay.CheckOut)
	if err != nil {
		return nil, fmt.Errorf("find available rooms: %w", err)
	}
	defer rows.Close()

	rooms := make([]Room, 0)
	for rows.Next() {
		var room Room
		if err := rows.Scan(&room.ID, &room.HotelID, &room.Number, &room.Type, &room.Capacity); err != nil {
			return nil, fmt.Errorf("scan room: %w", err)
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rooms: %w", err)
	}
	return rooms, nil
}

// CreateBooking validates capacity and inserts a confirmed booking. The
// exclusion constraint maps every overlapping concurrent insert to
// ErrRoomUnavailable, even when requests are handled by different processes.
func (s *Store) CreateBooking(ctx context.Context, roomID int64, stay Stay) (Booking, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Booking{}, fmt.Errorf("begin booking transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var hotelID int64
	var capacity int
	err = tx.QueryRow(ctx, `SELECT hotel_id, capacity FROM rooms WHERE id = $1`, roomID).Scan(&hotelID, &capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrRoomNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("find room: %w", err)
	}
	if stay.Guests > capacity {
		return Booking{}, ErrCapacityExceeded
	}

	booking := Booking{
		Reference: uuid.New().String(),
		HotelID:   hotelID,
		RoomID:    roomID,
		CheckIn:   formatDate(stay.CheckIn),
		CheckOut:  formatDate(stay.CheckOut),
		Guests:    stay.Guests,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (reference, room_id, check_in, check_out, guests)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`, booking.Reference, roomID, stay.CheckIn, stay.CheckOut, stay.Guests).
		Scan(&booking.CreatedAt)
	if isPostgresCode(err, "23P01") {
		return Booking{}, ErrRoomUnavailable
	}
	if err != nil {
		return Booking{}, fmt.Errorf("insert booking: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		if isPostgresCode(err, "23P01") {
			return Booking{}, ErrRoomUnavailable
		}
		return Booking{}, fmt.Errorf("commit booking: %w", err)
	}
	return booking, nil
}

// BookingByReference retrieves one confirmed booking.
func (s *Store) BookingByReference(ctx context.Context, reference string) (Booking, error) {
	var booking Booking
	var checkIn, checkOut time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT b.reference, r.hotel_id, b.room_id, b.check_in, b.check_out, b.guests, b.created_at
		FROM bookings AS b
		JOIN rooms AS r ON r.id = b.room_id
		WHERE b.reference = $1`, reference).
		Scan(&booking.Reference, &booking.HotelID, &booking.RoomID, &checkIn, &checkOut, &booking.Guests, &booking.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrBookingNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("find booking: %w", err)
	}
	booking.CheckIn = formatDate(checkIn)
	booking.CheckOut = formatDate(checkOut)
	return booking, nil
}

// Reset removes all business data while retaining the migrated schema.
func (s *Store) Reset(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `TRUNCATE bookings, rooms, hotels RESTART IDENTITY CASCADE`); err != nil {
		return fmt.Errorf("reset database: %w", err)
	}
	return nil
}

// Seed creates one hotel with the challenge's six-room inventory. Refusing to
// seed a nonempty database prevents accidental fixture duplication or data loss.
func (s *Store) Seed(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var nonempty bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM hotels)
		    OR EXISTS (SELECT 1 FROM rooms)
		    OR EXISTS (SELECT 1 FROM bookings)`).Scan(&nonempty)
	if err != nil {
		return fmt.Errorf("check seed state: %w", err)
	}
	if nonempty {
		return ErrDatabaseNotEmpty
	}

	if _, err := tx.Exec(ctx, `INSERT INTO hotels (id, name) VALUES (1, 'Waracle Hotel')`); err != nil {
		return fmt.Errorf("seed hotel: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO rooms (id, hotel_id, number, type, capacity) VALUES
		    (1, 1, '101', 'single', 1),
		    (2, 1, '102', 'single', 1),
		    (3, 1, '201', 'double', 2),
		    (4, 1, '202', 'double', 2),
		    (5, 1, '301', 'deluxe', 4),
		    (6, 1, '302', 'deluxe', 4)`); err != nil {
		return fmt.Errorf("seed rooms: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed: %w", err)
	}
	return nil
}

func isPostgresCode(err error, code string) bool {
	postgresError, ok := errors.AsType[*pgconn.PgError](err)
	return ok && postgresError.Code == code
}
