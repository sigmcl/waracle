package hotel

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPWorkflow(t *testing.T) {
	store := newTestStore(t)
	confirmations := make(chan string, 1)
	notifier := NewNotifier(0, func(reference string) { confirmations <- reference })
	t.Cleanup(notifier.Close)

	server := httptest.NewServer(NewHandler(store, notifier, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)

	assertStatusAndClose(t, request(t, http.MethodPost, server.URL+"/admin/seed", nil, ""), http.StatusCreated)
	assertStatusAndClose(t, request(t, http.MethodPost, server.URL+"/admin/seed", nil, ""), http.StatusConflict)

	response := request(t, http.MethodGet, server.URL+"/hotels?name=waracle%20hotel", nil, "")
	assertStatus(t, response, http.StatusOK)
	var hotelResult struct {
		Hotels []Hotel `json:"hotels"`
	}
	decodeResponse(t, response, &hotelResult)
	if len(hotelResult.Hotels) != 1 || hotelResult.Hotels[0].ID != 1 {
		t.Fatalf("hotel search = %#v, want seeded hotel", hotelResult.Hotels)
	}

	response = request(t, http.MethodGet, server.URL+"/hotels/1/rooms?check_in=2027-09-01&check_out=2027-09-03&guests=2", nil, "")
	assertStatus(t, response, http.StatusOK)
	var roomResult struct {
		Rooms []Room `json:"rooms"`
	}
	decodeResponse(t, response, &roomResult)
	if len(roomResult.Rooms) != 4 {
		t.Fatalf("availability returned %d rooms, want 4", len(roomResult.Rooms))
	}

	body := []byte(`{"room_id":3,"check_in":"2027-09-01","check_out":"2027-09-03","guests":2}`)
	response = request(t, http.MethodPost, server.URL+"/bookings", body, "application/json")
	assertStatus(t, response, http.StatusCreated)
	location := response.Header.Get("Location")
	var created Booking
	decodeResponse(t, response, &created)
	if location != "/bookings/"+created.Reference {
		t.Fatalf("Location = %q, want booking URL", location)
	}

	select {
	case reference := <-confirmations:
		if reference != created.Reference {
			t.Fatalf("confirmation reference = %q, want %q", reference, created.Reference)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation was not emitted asynchronously")
	}

	response = request(t, http.MethodGet, server.URL+location, nil, "")
	assertStatus(t, response, http.StatusOK)
	var found Booking
	decodeResponse(t, response, &found)
	if found.Reference != created.Reference || found.RoomID != 3 {
		t.Fatalf("retrieved booking = %#v, want created booking", found)
	}

	// Reusing a stale availability result is rejected by PostgreSQL.
	assertStatusAndClose(t, request(t, http.MethodPost, server.URL+"/bookings", body, "application/json"), http.StatusConflict)
	unknownField := []byte(`{"room_id":4,"check_in":"2027-09-01","check_out":"2027-09-03","guests":2,"status":"confirmed"}`)
	assertStatusAndClose(t, request(t, http.MethodPost, server.URL+"/bookings", unknownField, "application/json"), http.StatusBadRequest)
	assertStatusAndClose(t, request(t, http.MethodPost, server.URL+"/bookings", body, "text/plain"), http.StatusUnsupportedMediaType)
}

func request(t *testing.T, method, url string, body []byte, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func assertStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("status = %d, want %d; body: %s", response.StatusCode, want, body)
	}
	if response.StatusCode == http.StatusNoContent {
		response.Body.Close()
	}
}

func assertStatusAndClose(t *testing.T, response *http.Response, want int) {
	t.Helper()
	defer response.Body.Close()
	assertStatus(t, response, want)
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.UnmarshalRead(response.Body, destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
