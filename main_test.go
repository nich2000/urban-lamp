package main

import "testing"

func TestValidateService(t *testing.T) {
	tests := []struct {
		target  string
		method  string
		wantErr bool
	}{
		{target: "https://example.com", method: "http"},
		{target: "1.1.1.1", method: "icmp"},
		{target: "", method: "http", wantErr: true},
		{target: "example.com", method: "http", wantErr: true},
		{target: "https://example.com", method: "icmp", wantErr: true},
		{target: "example.com", method: "tcp", wantErr: true},
	}

	for _, tt := range tests {
		err := validateService(tt.target, tt.method)
		if tt.wantErr && err == nil {
			t.Fatalf("validateService(%q, %q) expected error", tt.target, tt.method)
		}
		if !tt.wantErr && err != nil {
			t.Fatalf("validateService(%q, %q) unexpected error: %v", tt.target, tt.method, err)
		}
	}
}

func TestSQLQuote(t *testing.T) {
	got := sqlQuote("service's api")
	want := "'service''s api'"
	if got != want {
		t.Fatalf("sqlQuote() = %q, want %q", got, want)
	}
}

func TestNormalizeTarget(t *testing.T) {
	tests := map[string]struct {
		target string
		method string
		want   string
	}{
		"bare http target": {target: "drift-dynamics.com", method: "http", want: "https://drift-dynamics.com"},
		"full http target": {target: "http://drift-dynamics.com", method: "http", want: "http://drift-dynamics.com"},
		"icmp target":      {target: "drift-dynamics.com", method: "icmp", want: "drift-dynamics.com"},
	}

	for name, tt := range tests {
		if got := normalizeTarget(tt.target, tt.method); got != tt.want {
			t.Fatalf("%s: normalizeTarget() = %q, want %q", name, got, tt.want)
		}
	}
}

func TestDisplayURL(t *testing.T) {
	tests := map[string]string{
		":8080":           "http://localhost:8080",
		"127.0.0.1:18080": "http://127.0.0.1:18080",
	}

	for input, want := range tests {
		if got := displayURL(input); got != want {
			t.Fatalf("displayURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestServiceWord(t *testing.T) {
	tests := map[int]string{
		0:  "сервисов",
		1:  "сервис",
		2:  "сервиса",
		4:  "сервиса",
		5:  "сервисов",
		11: "сервисов",
		21: "сервис",
		22: "сервиса",
		25: "сервисов",
	}

	for count, want := range tests {
		if got := serviceWord(count); got != want {
			t.Fatalf("serviceWord(%d) = %q, want %q", count, got, want)
		}
	}
}
