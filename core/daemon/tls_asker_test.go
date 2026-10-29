package daemon

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestParseTlsAskerListen(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		argv    []string
		want    string
		wantErr bool
	}{
		{"absent", nil, []string{"prog"}, "", false},
		{"bare flag", nil, []string{"prog", "--tls-asker"}, TLS_ASKER_DEFAULT_LISTEN, false},
		{"bare equals", nil, []string{"prog", "--tls-asker="}, TLS_ASKER_DEFAULT_LISTEN, false},
		{"equals addr", nil, []string{"prog", "--tls-asker=:8080"}, ":8080", false},
		{"go-arg captured", []string{":8080"}, []string{"prog", "--tls-asker", ":8080"}, ":8080", false},
		{"go-arg blank", []string{"  "}, nil, TLS_ASKER_DEFAULT_LISTEN, false},
		{"too many", []string{"a", "b"}, nil, "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parse_tls_asker_listen(c.values, c.argv)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("expected %q, got %q", c.want, got)
			}
		})
	}
}

func TestTlsAskerHandle(t *testing.T) {
	d := &Daemon{}
	asker := NewTlsAsker("127.0.0.1:0", d)

	// missing domain → 400
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	asker.handle(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing domain: expected 400, got %d", rr.Code)
	}

	// invalid domain → 400
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/?domain=not-an-ip", nil)
	asker.handle(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid domain: expected 400, got %d", rr.Code)
	}

	// no rndis interface → 403
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/?domain=10.22.33.1", nil)
	asker.handle(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no interface: expected 403, got %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if len(body) != 0 {
		t.Fatalf("403 should have empty body, got %q", body)
	}
}

func TestIfaceOwnsIpAgainstLoopback(t *testing.T) {
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("no loopback interface")
	}
	if iface.Flags&net.FlagUp == 0 {
		t.Skip("loopback is down")
	}

	ip := netip.MustParseAddr("127.0.0.1")
	if !iface_owns_ip("lo", ip) {
		t.Fatalf("expected lo to own 127.0.0.1")
	}
	if iface_owns_ip("lo", netip.MustParseAddr("10.22.33.1")) {
		t.Fatalf("lo should not own 10.22.33.1")
	}
	if iface_owns_ip("no-such-iface-xyz", ip) {
		t.Fatalf("missing iface should not own any IP")
	}
	if iface_owns_ip("", ip) {
		t.Fatalf("empty ifname should not own any IP")
	}
}
