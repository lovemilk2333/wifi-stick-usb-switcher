package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
)

const TLS_ASKER_DEFAULT_LISTEN = "127.0.0.1:50996"

// TlsAsker is a Caddy on_demand TLS ask endpoint: GET /?domain=<ip>
// returns 200 if <ip> is currently assigned to the RNDIS interface,
// 403 (empty body) if the interface is down/missing or the IP does not
// match, 400 if domain is missing/invalid.
type TlsAsker struct {
	listen string
	daemon *Daemon
	server *http.Server
}

// NewTlsAsker builds a TlsAsker for the given listen address (empty → default).
func NewTlsAsker(listen string, daemon *Daemon) *TlsAsker {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = TLS_ASKER_DEFAULT_LISTEN
	}
	return &TlsAsker{listen: listen, daemon: daemon}
}

// Start launches the HTTP server in a background goroutine.
func (this *TlsAsker) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", this.handle)

	this.server = &http.Server{
		Addr:              this.listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("INFO tls-asker listening on %s\n", this.listen)
		if err := this.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WARN: tls-asker stopped: %v\n", err)
		}
	}()
}

// Stop gracefully shuts down the HTTP server.
func (this *TlsAsker) Stop() {
	if this.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = this.server.Shutdown(ctx)
}

func (this *TlsAsker) handle(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ip, err := netip.ParseAddr(domain)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !this.daemon.rndis_owns_ip(ip) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// rndis_owns_ip reports whether ip is currently assigned to the RNDIS
// network interface. Returns false if no RNDIS mode exists, the interface
// is missing/down, or the address is not present on it.
func (this *Daemon) rndis_owns_ip(ip netip.Addr) bool {
	return iface_owns_ip(this.rndis_ifname(), ip)
}

// iface_owns_ip reports whether ifname is up and currently has ip assigned.
func iface_owns_ip(ifname string, ip netip.Addr) bool {
	if ifname == "" {
		return false
	}
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return false
	}
	if iface.Flags&net.FlagUp == 0 {
		return false
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		if prefix.Addr() == ip {
			return true
		}
	}
	return false
}

// rndis_ifname returns the live RNDIS interface name, or "" if none.
func (this *Daemon) rndis_ifname() string {
	for _, mode := range this.modes {
		rndis, ok := mode.(*usb.UsbGadgetRndis)
		if !ok {
			continue
		}
		return rndis.GetIfname()
	}
	return ""
}

// parse_tls_asker_listen resolves --tls-asker into a listen address.
// go-arg cannot distinguish a bare `--tls-asker` from an absent flag for
// []string (both leave nil), so argv is also scanned:
//
//	absent                         → disabled ("")
//	--tls-asker / --tls-asker=     → 127.0.0.1:50996
//	--tls-asker ADDR / --tls-asker=ADDR → ADDR
func parse_tls_asker_listen(values []string, argv []string) (string, error) {
	if len(values) > 1 {
		return "", fmt.Errorf("--tls-asker accepts at most one listen address")
	}
	if len(values) == 1 {
		addr := strings.TrimSpace(values[0])
		if addr == "" {
			return TLS_ASKER_DEFAULT_LISTEN, nil
		}
		return addr, nil
	}

	// values empty/nil: detect bare flag in argv
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--tls-asker" || a == "--tls-asker=":
			return TLS_ASKER_DEFAULT_LISTEN, nil
		case strings.HasPrefix(a, "--tls-asker="):
			addr := strings.TrimSpace(strings.TrimPrefix(a, "--tls-asker="))
			if addr == "" {
				return TLS_ASKER_DEFAULT_LISTEN, nil
			}
			return addr, nil
		}
	}
	return "", nil
}
