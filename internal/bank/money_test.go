package bank

import "testing"

func TestParseRubles(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"300000.00", 30_000_000},
		{"300000", 30_000_000},
		{"300000.5", 30_000_050},
		{"0.05", 5},
		{"14674.60", 1_467_460},
	}
	for _, tt := range tests {
		got, err := ParseRubles(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParseRubles(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "-1.00", "1.234", "1.", ".5", "1,00", " 1.00", "1e3", "abc", "1234567890123456"} {
		if _, err := ParseRubles(bad); err == nil {
			t.Errorf("ParseRubles(%q) must fail", bad)
		}
	}
}

func TestFormatRubles(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{30_000_000, "300000.00"},
		{30_000_050, "300000.50"},
		{5, "0.05"},
		{0, "0.00"},
		{1_467_460, "14674.60"},
	}
	for _, tt := range tests {
		if got := FormatRubles(tt.in); got != tt.want {
			t.Errorf("FormatRubles(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParsePercent(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"15.90", 1590},
		{"15.9", 1590},
		{"15", 1500},
		{"0.5", 50},
	}
	for _, tt := range tests {
		got, err := ParsePercent(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParsePercent(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "15.901", "-1", "15%"} {
		if _, err := ParsePercent(bad); err == nil {
			t.Errorf("ParsePercent(%q) must fail", bad)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	for bps, want := range map[int]string{1590: "15.90", 1490: "14.90", 50: "0.50"} {
		if got := FormatPercent(bps); got != want {
			t.Errorf("FormatPercent(%d) = %q, want %q", bps, got, want)
		}
	}
}
