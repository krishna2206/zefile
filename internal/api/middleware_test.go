package api

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:   "direct public connection uses the peer, ignores forwarded",
			remote: "153.67.85.202:51000",
			headers: map[string]string{
				"X-Forwarded-For": "1.2.3.4",
				"X-Real-Ip":       "5.6.7.8",
			},
			want: "153.67.85.202",
		},
		{
			name:    "behind a proxy, X-Real-Ip is the client",
			remote:  "10.0.1.175:40000",
			headers: map[string]string{"X-Real-Ip": "203.0.113.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "behind a proxy, rightmost X-Forwarded-For wins over spoofed values",
			remote:  "10.0.1.175:40000",
			headers: map[string]string{"X-Forwarded-For": "9.9.9.9, 203.0.113.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "proxy peer but no forwarded header falls back to the peer",
			remote:  "10.0.1.175:40000",
			headers: map[string]string{},
			want:    "10.0.1.175",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tc.remote, Header: http.Header{}}
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
