package hotel

import "testing"

func TestParseStay(t *testing.T) {
	tests := []struct {
		name              string
		checkIn, checkOut string
		guests            int
		wantError         bool
	}{
		{name: "one night", checkIn: "2028-02-28", checkOut: "2028-02-29", guests: 1},
		{name: "leap day", checkIn: "2028-02-29", checkOut: "2028-03-01", guests: 2},
		{name: "impossible date", checkIn: "2027-02-29", checkOut: "2027-03-01", guests: 1, wantError: true},
		{name: "equal dates", checkIn: "2027-01-01", checkOut: "2027-01-01", guests: 1, wantError: true},
		{name: "reversed dates", checkIn: "2027-01-02", checkOut: "2027-01-01", guests: 1, wantError: true},
		{name: "no guests", checkIn: "2027-01-01", checkOut: "2027-01-02", guests: 0, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseStay(test.checkIn, test.checkOut, test.guests)
			if (err != nil) != test.wantError {
				t.Fatalf("ParseStay() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}
