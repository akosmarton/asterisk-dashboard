package web

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:    "untrusted peer ignores forwarded headers",
			remote:  "198.51.100.5:1234",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4"},
			want:    "198.51.100.5",
		},
		{
			name:    "trusted proxy walks the chain",
			remote:  "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7, 10.0.0.2"},
			want:    "198.51.100.7",
		},
		{
			name:    "x-real-ip is ignored",
			remote:  "10.0.0.1:1234",
			headers: map[string]string{"X-Real-IP": "198.51.100.9"},
			want:    "10.0.0.1",
		},
		{
			name:    "malformed chain falls back to the peer",
			remote:  "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "not-an-ip"},
			want:    "10.0.0.1",
		},
		{
			name:    "all-trusted chain returns the furthest hop",
			remote:  "10.0.0.1:1234",
			headers: map[string]string{"X-Forwarded-For": "10.0.0.2, 10.0.0.3"},
			want:    "10.0.0.2",
		},
		{
			name:    "ipv6 ula is trusted",
			remote:  "[fd00::1]:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7"},
			want:    "198.51.100.7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}
			if got := clientIP(req); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
