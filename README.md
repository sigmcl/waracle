# Waracle Hotel Booking API

A REST API for hotel room availability and booking, built with Go 1.27,
PostgreSQL, `golang-migrate`, and Testcontainers.

## Run locally

Docker and Docker Compose are required.

```sh
docker compose up --build
curl -X POST http://localhost:8080/admin/seed
```

The API runs at `http://localhost:8080`. Compose runs the database migrations
in a one-shot container before starting the API. To use another port, run
`HTTP_PORT=18080 docker compose up --build`.

### Try the API with `curl`

Run these calls in order after starting the application:

```sh
curl -X POST http://localhost:8080/admin/reset
curl -X POST http://localhost:8080/admin/seed

curl 'http://localhost:8080/hotels?name=Waracle%20Hotel'
curl 'http://localhost:8080/hotels/1/rooms?check_in=2027-06-10&check_out=2027-06-12&guests=2'

curl -X POST http://localhost:8080/bookings \
  -H 'Content-Type: application/json' \
  -d '{"room_id":3,"check_in":"2027-06-10","check_out":"2027-06-12","guests":2}'

curl http://localhost:8080/bookings/REPLACE_WITH_REFERENCE
```

[`requests.http`](requests.http) contains the same workflow for HTTP clients.

Remove the containers and persisted database with:

```sh
docker compose down --volumes
```

## API

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/hotels?name=Waracle%20Hotel` | Find hotels by exact, case-insensitive name |
| `GET` | `/hotels/{id}/rooms?check_in=YYYY-MM-DD&check_out=YYYY-MM-DD&guests=2` | Find rooms available for the complete stay |
| `POST` | `/bookings` | Create a booking |
| `GET` | `/bookings/{reference}` | Retrieve a booking |
| `POST` | `/admin/reset` | Delete all application data |
| `POST` | `/admin/seed` | Seed one hotel and six rooms |

Example booking request:

```json
{
  "room_id": 3,
  "check_in": "2027-06-10",
  "check_out": "2027-06-12",
  "guests": 2
}
```

Errors have the form `{"error":{"code":"room_unavailable","message":"..."}}`.
The API uses `400` for invalid input, `404` for missing resources, `409` for
booking conflicts, `413` for oversized bodies, and `415` for non-JSON requests.

## Booking rules

- Dates use strict `YYYY-MM-DD` format.
- Check-in is included and checkout is excluded. A room can therefore be
  booked again on the checkout date.
- One room must accommodate the whole party for the complete stay.
- Capacities are single: 1, double: 2, and deluxe: 4.
- The seed creates two rooms of each type at `Waracle Hotel`.
- Search results do not reserve inventory; booking always revalidates it.
- Past dates are accepted because no booking horizon is specified.

PostgreSQL prevents double booking with this exclusion constraint:

```sql
EXCLUDE USING gist (
    room_id WITH =,
    daterange(check_in, check_out, '[)') WITH &&
)
```

It rejects overlapping stays for the same room, including concurrent requests
from different API processes. The API returns `409 room_unavailable` when the
constraint is violated.

## Development

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

Docker must be running for the tests. Testcontainers starts PostgreSQL 18,
applies the real migrations, and removes the container afterward. Tests cover
date boundaries, capacity, continuous room availability, reset and seed,
HTTP behavior, and concurrent booking conflicts.

The main code lives in `cmd/api` and `internal/hotel`; migrations are under
`internal/hotel/migrations`. Confirmation logging is best-effort and runs in a
background goroutine after a booking commits. The unauthenticated admin
endpoints are intended only for this exercise.

## AI disclosure

AI generated the initial implementation, tests, and documentation under my
direction. I reviewed and tested the solution and can explain its design and
trade-offs.
