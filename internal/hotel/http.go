package hotel

import (
	"encoding/json/v2"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

const maxRequestBody = 64 << 10

// api translates HTTP requests into hotel operations and stable JSON responses.
type api struct {
	store    *Store
	notifier *Notifier
	logger   *slog.Logger
}

// NewHandler constructs the complete HTTP API.
func NewHandler(store *Store, notifier *Notifier, logger *slog.Logger) http.Handler {
	handler := &api{store: store, notifier: notifier, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hotels", handler.findHotels)
	mux.HandleFunc("GET /hotels/{hotelID}/rooms", handler.availableRooms)
	mux.HandleFunc("POST /bookings", handler.createBooking)
	mux.HandleFunc("GET /bookings/{reference}", handler.findBooking)
	mux.HandleFunc("POST /admin/reset", handler.reset)
	mux.HandleFunc("POST /admin/seed", handler.seed)
	return mux
}

func (a *api) findHotels(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "name is required", nil)
		return
	}

	hotels, err := a.store.FindHotels(r.Context(), name)
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"hotels": hotels})
}

func (a *api) availableRooms(w http.ResponseWriter, r *http.Request) {
	hotelID, err := parsePositiveInt64(r.PathValue("hotelID"))
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "hotel ID must be a positive integer", nil)
		return
	}
	guests, err := strconv.Atoi(r.URL.Query().Get("guests"))
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "guests must be a positive integer", nil)
		return
	}
	stay, err := ParseStay(r.URL.Query().Get("check_in"), r.URL.Query().Get("check_out"), guests)
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}

	rooms, err := a.store.AvailableRooms(r.Context(), hotelID, stay)
	if errors.Is(err, ErrHotelNotFound) {
		a.writeError(w, r, http.StatusNotFound, "hotel_not_found", "The hotel was not found.", nil)
		return
	}
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (a *api) createBooking(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RoomID   int64  `json:"room_id"`
		CheckIn  string `json:"check_in"`
		CheckOut string `json:"check_out"`
		Guests   int    `json:"guests"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		status := http.StatusBadRequest
		code := "invalid_request"
		if errors.Is(err, errUnsupportedMediaType) {
			status, code = http.StatusUnsupportedMediaType, "unsupported_media_type"
		} else if errors.Is(err, errRequestTooLarge) {
			status, code = http.StatusRequestEntityTooLarge, "request_too_large"
		}
		a.writeError(w, r, status, code, err.Error(), nil)
		return
	}
	if request.RoomID <= 0 {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "room_id must be a positive integer", nil)
		return
	}
	stay, err := ParseStay(request.CheckIn, request.CheckOut, request.Guests)
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}

	booking, err := a.store.CreateBooking(r.Context(), request.RoomID, stay)
	switch {
	case errors.Is(err, ErrRoomNotFound):
		a.writeError(w, r, http.StatusNotFound, "room_not_found", "The room was not found.", nil)
		return
	case errors.Is(err, ErrCapacityExceeded):
		a.writeError(w, r, http.StatusBadRequest, "capacity_exceeded", "The room cannot hold the requested number of guests.", nil)
		return
	case errors.Is(err, ErrRoomUnavailable):
		a.writeError(w, r, http.StatusConflict, "room_unavailable", "The room is unavailable for the requested dates.", nil)
		return
	case err != nil:
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", err)
		return
	}

	// Notifications are scheduled only after PostgreSQL commits the booking.
	a.notifier.Schedule(booking.Reference)
	w.Header().Set("Location", "/bookings/"+booking.Reference)
	a.writeJSON(w, http.StatusCreated, booking)
}

func (a *api) findBooking(w http.ResponseWriter, r *http.Request) {
	booking, err := a.store.BookingByReference(r.Context(), r.PathValue("reference"))
	if errors.Is(err, ErrBookingNotFound) {
		a.writeError(w, r, http.StatusNotFound, "booking_not_found", "The booking was not found.", nil)
		return
	}
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", err)
		return
	}
	a.writeJSON(w, http.StatusOK, booking)
}

func (a *api) reset(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Reset(r.Context()); err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) seed(w http.ResponseWriter, r *http.Request) {
	err := a.store.Seed(r.Context())
	if errors.Is(err, ErrDatabaseNotEmpty) {
		a.writeError(w, r, http.StatusConflict, "database_not_empty", "Reset the database before seeding it.", nil)
		return
	}
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]int{"hotels": 1, "rooms": 6, "bookings": 0})
}

func (a *api) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, value); err != nil {
		a.logger.Error("write JSON response", "error", err)
	}
}

func (a *api) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, err error) {
	if err != nil {
		a.logger.Error("handle request", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	a.writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

var (
	errUnsupportedMediaType = errors.New("Content-Type must be application/json")
	errRequestTooLarge      = errors.New("request body must not exceed 64 KiB")
)

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errUnsupportedMediaType
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := json.UnmarshalRead(r.Body, destination, json.RejectUnknownMembers(true)); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return errRequestTooLarge
		}
		return errors.New("request body must contain one valid JSON object")
	}
	return nil
}

func parsePositiveInt64(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("value must be a positive integer")
	}
	return parsed, nil
}
