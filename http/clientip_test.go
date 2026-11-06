package fbhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "no headers falls back to the connection address",
			remoteAddr: "203.0.113.9:51234",
			want:       "203.0.113.9",
		},
		{
			name:       "public client is picked out of the forwarded chain",
			remoteAddr: "172.17.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9, 10.0.0.2, 172.17.0.1"},
			want:       "203.0.113.9",
		},
		{
			name:       "left-most public entry wins over later ones",
			remoteAddr: "172.17.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9, 198.51.100.7"},
			want:       "203.0.113.9",
		},
		{
			name:       "all-private chain still yields a stable address, never empty",
			remoteAddr: "172.17.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.2, 172.17.0.1"},
			want:       "10.0.0.2",
		},
		{
			name:       "x-real-ip is used when the chain has no public entry",
			remoteAddr: "172.17.0.1:40000",
			headers: map[string]string{
				"X-Forwarded-For": "10.0.0.2",
				"X-Real-Ip":       "203.0.113.9",
			},
			want: "203.0.113.9",
		},
		{
			name:       "entries carrying a port are normalized",
			remoteAddr: "172.17.0.1:40000",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9:1234"},
			want:       "203.0.113.9",
		},
		{
			name:       "bracketed ipv6 is normalized",
			remoteAddr: "[2001:db8::1]:443",
			want:       "2001:db8::1",
		},
		{
			name:       "garbage headers are ignored in favour of the connection",
			remoteAddr: "203.0.113.9:1",
			headers:    map[string]string{"X-Forwarded-For": "not-an-ip, also bad"},
			want:       "203.0.113.9",
		},
		{
			name:       "carrier-grade NAT is not treated as public",
			remoteAddr: "172.17.0.1:1",
			headers:    map[string]string{"X-Forwarded-For": "100.64.0.5, 203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "nothing resolvable yields the placeholder",
			remoteAddr: "",
			want:       unknownIP,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
			r.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := requestIP(r); got != tc.want {
				t.Fatalf("requestIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsPublicIP(t *testing.T) {
	t.Parallel()

	public := []string{"203.0.113.9", "8.8.8.8", "2001:db8::1"}
	private := []string{"10.0.0.1", "172.17.0.1", "192.168.1.1", "127.0.0.1", "::1", "100.64.0.1", "0.0.0.0", "fd00::1", "not-an-ip"}

	for _, ip := range public {
		if !isPublicIP(ip) {
			t.Errorf("%s should be public", ip)
		}
	}
	for _, ip := range private {
		if isPublicIP(ip) {
			t.Errorf("%s should not be public", ip)
		}
	}
}
