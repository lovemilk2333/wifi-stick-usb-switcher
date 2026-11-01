package daemon

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
)

const TLS_SERVICE_DEFAULT_LISTEN = "127.0.0.1:50996"

// TlsService exposes Caddy's on-demand ask and HTTP certificate endpoints.
type TlsService struct {
	listen        string
	daemon        *Daemon
	server        *http.Server
	root_cert     *x509.Certificate
	root_cert_pem []byte
	root_signer   crypto.Signer
	algorithm     string
	leaf_duration time.Duration
	cache_lock    sync.Mutex
	cert_cache    map[string]cached_tls_certificate
}

type cached_tls_certificate struct {
	certificate []byte
	private_key []byte
	expires_at  time.Time
}

// NewTlsService constructs the service and validates its root CA and private key.
func NewTlsService(listen string, daemon *Daemon, rootCertPEM, rootKeyPEM []byte, algorithm string, leafDurations ...time.Duration) (*TlsService, error) {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = TLS_SERVICE_DEFAULT_LISTEN
	}
	algorithm = strings.ToLower(strings.TrimSpace(algorithm))
	if algorithm == "" {
		algorithm = "ecdsa-p384"
	}
	leafDuration := 26 * time.Hour
	if len(leafDurations) > 1 {
		return nil, fmt.Errorf("TLS leaf duration may only be specified once")
	}
	if len(leafDurations) == 1 {
		leafDuration = leafDurations[0]
	}
	if leafDuration <= 0 {
		return nil, fmt.Errorf("TLS leaf duration must be positive")
	}
	if !supported_tls_algorithm(algorithm) {
		return nil, fmt.Errorf("unsupported TLS leaf algorithm %q (supported: ecdsa-p256, ecdsa-p384, ecdsa-p521, or rsa<bits> with bits >= 4096)", algorithm)
	}
	pair, err := tls.X509KeyPair(rootCertPEM, rootKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load TLS root certificate/key: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("TLS root certificate is empty")
	}
	root, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse TLS root certificate: %w", err)
	}
	if !root.IsCA || (root.KeyUsage != 0 && root.KeyUsage&x509.KeyUsageCertSign == 0) {
		return nil, fmt.Errorf("TLS root certificate must be a CA with certificate-signing usage")
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("TLS root private key does not implement crypto.Signer")
	}
	rootBlock, _ := pem.Decode(rootCertPEM)
	if rootBlock == nil || rootBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("TLS root certificate must be PEM encoded")
	}
	// Only return the CA certificate that matches the private key. The input
	// file may contain a bundle of same-subject roots or intermediate certs;
	// forwarding unrelated certificates can confuse strict client path builders.
	rootCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw})
	return &TlsService{
		listen:        listen,
		daemon:        daemon,
		root_cert:     root,
		root_cert_pem: append([]byte(nil), rootCertPEM...),
		root_signer:   signer,
		algorithm:     algorithm,
		leaf_duration: leafDuration,
		cert_cache:    make(map[string]cached_tls_certificate),
	}, nil
}

// Start launches the HTTP server in a background goroutine.
func (this *TlsService) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ask", this.handle_ask)
	mux.HandleFunc("/generate", this.handle_generate)
	this.server = &http.Server{
		Addr:              this.listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("INFO tls-service listening on %s\n", this.listen)
		if err := this.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WARN: tls-service stopped: %v\n", err)
		}
	}()
}

// Stop gracefully shuts down the HTTP server.
func (this *TlsService) Stop() {
	if this.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = this.server.Shutdown(ctx)
}

func (this *TlsService) handle_ask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(r.URL.Query().Get("domain")))
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

func (this *TlsService) handle_generate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	localIP, err := netip.ParseAddr(strings.TrimSpace(r.URL.Query().Get("local_ip")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !this.daemon.rndis_owns_ip(localIP) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	serverName := r.URL.Query().Get("server_name")
	if serverName == "" {
		// IP URL handshakes may omit SNI; the RNDIS local address is then the
		// only requested certificate identity available to the HTTP getter.
		serverName = localIP.String()
	}
	if strings.TrimSpace(serverName) != serverName || !valid_certificate_name(serverName) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if nameIP, err := netip.ParseAddr(serverName); err == nil && !this.daemon.rndis_owns_ip(nameIP) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	certPEM, keyPEM, err := this.get_or_generate_certificate(serverName, r.URL.Query().Get("signature_schemes"))
	if err != nil {
		log.Printf("WARN: cannot generate TLS certificate for %q: %v", serverName, err)
		http.Error(w, "cannot generate certificate", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(certPEM)
	_, _ = w.Write(keyPEM)
}

func (this *TlsService) generate_certificate(serverName, signatureSchemes string) ([]byte, []byte, error) {
	if time.Now().After(this.root_cert.NotAfter) {
		return nil, nil, fmt.Errorf("TLS root certificate has expired")
	}
	leafKey, err := generate_leaf_key(this.algorithm, signatureSchemes)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	now := time.Now()
	notAfter := now.Add(this.leaf_duration)
	if notAfter.After(this.root_cert.NotAfter) {
		notAfter = this.root_cert.NotAfter
	}
	if this.root_cert.NotBefore.After(now) {
		return nil, nil, fmt.Errorf("TLS root certificate is not valid yet")
	}
	notBefore := now.Add(-time.Minute)
	if notBefore.Before(this.root_cert.NotBefore) {
		notBefore = this.root_cert.NotBefore
	}

	leaf := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: serverName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip, err := netip.ParseAddr(serverName); err == nil {
		leaf.IPAddresses = []net.IP{net.IP(ip.AsSlice())}
	} else {
		leaf.DNSNames = []string{serverName}
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, this.root_cert, leafKey.Public(), this.root_signer)
	if err != nil {
		return nil, nil, fmt.Errorf("sign leaf certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = append(certPEM, this.root_cert_pem...)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode leaf private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	log.Printf("INFO tls-service issued certificate: server_name=%q algorithm=%s serial=%s not_after=%s", serverName, this.algorithm, serial.Text(16), notAfter.Format(time.RFC3339))
	return certPEM, keyPEM, nil
}

func (this *TlsService) get_or_generate_certificate(serverName, signatureSchemes string) ([]byte, []byte, error) {
	cacheKey := serverName + "\x00" + signatureSchemes
	this.cache_lock.Lock()
	defer this.cache_lock.Unlock()
	if cached, ok := this.cert_cache[cacheKey]; ok && time.Until(cached.expires_at) > time.Minute {
		return append([]byte(nil), cached.certificate...), append([]byte(nil), cached.private_key...), nil
	}
	certificate, privateKey, err := this.generate_certificate(serverName, signatureSchemes)
	if err != nil {
		return nil, nil, err
	}
	this.cert_cache[cacheKey] = cached_tls_certificate{
		certificate: append([]byte(nil), certificate...),
		private_key: append([]byte(nil), privateKey...),
		expires_at:  min_time(time.Now().Add(this.leaf_duration), this.root_cert.NotAfter),
	}
	return certificate, privateKey, nil
}

func valid_certificate_name(name string) bool {
	if ip, err := netip.ParseAddr(name); err == nil {
		return ip.IsValid() && ip.Zone() == ""
	}
	if len(name) == 0 || len(name) > 253 || strings.ContainsAny(name, " /\\\t\r\n") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func supported_tls_algorithm(algorithm string) bool {
	switch algorithm {
	case "ecdsa-p256", "ecdsa-p384", "ecdsa-p521":
		return true
	}
	if !strings.HasPrefix(algorithm, "rsa") {
		return false
	}
	bits, err := strconv.Atoi(strings.TrimPrefix(algorithm, "rsa"))
	return err == nil && bits >= 4096 && bits%1024 == 0
}

func generate_leaf_key(algorithm, signatureSchemes string) (crypto.Signer, error) {
	schemes := make([]uint16, 0)
	for _, value := range strings.Split(signatureSchemes, ",") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "0x"))
		if parsed, err := strconv.ParseUint(value, 16, 16); err == nil {
			schemes = append(schemes, uint16(parsed))
		}
	}
	if strings.HasPrefix(algorithm, "rsa") {
		bits, err := strconv.Atoi(strings.TrimPrefix(algorithm, "rsa"))
		if err != nil || bits < 4096 || bits%1024 != 0 {
			return nil, fmt.Errorf("RSA TLS leaf key size must be an integer multiple of 1024 and at least 4096 bits")
		}
		if len(schemes) > 0 && !has_rsa_signature_scheme(schemes) {
			return nil, fmt.Errorf("Caddy signature_schemes do not support RSA")
		}
		return rsa.GenerateKey(rand.Reader, bits)
	}
	var curve elliptic.Curve
	var compatibleScheme uint16
	switch algorithm {
	case "ecdsa-p256":
		curve, compatibleScheme = elliptic.P256(), 0x0403
	case "ecdsa-p384":
		curve, compatibleScheme = elliptic.P384(), 0x0503
	case "ecdsa-p521":
		curve, compatibleScheme = elliptic.P521(), 0x0603
	default:
		return nil, fmt.Errorf("unsupported TLS leaf algorithm %q", algorithm)
	}
	if len(schemes) > 0 {
		for _, scheme := range schemes {
			if scheme == compatibleScheme {
				return ecdsa.GenerateKey(curve, rand.Reader)
			}
		}
		return nil, fmt.Errorf("Caddy signature_schemes do not support configured %s", algorithm)
	}
	return ecdsa.GenerateKey(curve, rand.Reader)
}

func has_rsa_signature_scheme(schemes []uint16) bool {
	for _, scheme := range schemes {
		switch scheme {
		case 0x0401, 0x0501, 0x0601, 0x0804, 0x0805, 0x0806:
			return true
		}
	}
	return false
}

func min_time(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func (this *Daemon) rndis_owns_ip(ip netip.Addr) bool {
	return iface_owns_ip(this.rndis_ifname(), ip)
}

func iface_owns_ip(ifname string, ip netip.Addr) bool {
	if ifname == "" {
		return false
	}
	iface, err := net.InterfaceByName(ifname)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		prefix, err := netip.ParsePrefix(a.String())
		if err == nil && prefix.Addr() == ip {
			return true
		}
	}
	return false
}

func (this *Daemon) rndis_ifname() string {
	for _, mode := range this.modes {
		if rndis, ok := mode.(*usb.UsbGadgetRndis); ok {
			return rndis.GetIfname()
		}
	}
	return ""
}

func parse_tls_service_listen(values []string, argv []string) (string, error) {
	if len(values) > 1 {
		return "", fmt.Errorf("--tls-service accepts at most one listen address")
	}
	if len(values) == 1 {
		addr := strings.TrimSpace(values[0])
		if addr == "" {
			return TLS_SERVICE_DEFAULT_LISTEN, nil
		}
		return addr, nil
	}
	for _, arg := range argv {
		switch {
		case arg == "--tls-service" || arg == "--tls-service=":
			return TLS_SERVICE_DEFAULT_LISTEN, nil
		case strings.HasPrefix(arg, "--tls-service="):
			addr := strings.TrimSpace(strings.TrimPrefix(arg, "--tls-service="))
			if addr == "" {
				return TLS_SERVICE_DEFAULT_LISTEN, nil
			}
			return addr, nil
		}
	}
	return "", nil
}

func read_tls_root(certPath, keyPath string) ([]byte, []byte, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read TLS root certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read TLS root key: %w", err)
	}
	return certPEM, keyPEM, nil
}
