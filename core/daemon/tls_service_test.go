package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/usb"
)

func TestParseTlsServiceListen(t *testing.T) {
	cases := []struct {
		name    string
		values  []string
		argv    []string
		want    string
		wantErr bool
	}{
		{"absent", nil, []string{"prog"}, "", false},
		{"bare flag", nil, []string{"prog", "--tls-service"}, TLS_SERVICE_DEFAULT_LISTEN, false},
		{"bare equals", nil, []string{"prog", "--tls-service="}, TLS_SERVICE_DEFAULT_LISTEN, false},
		{"equals addr", nil, []string{"prog", "--tls-service=:8080"}, ":8080", false},
		{"go-arg captured", []string{":8080"}, []string{"prog", "--tls-service", ":8080"}, ":8080", false},
		{"go-arg blank", []string{"  "}, nil, TLS_SERVICE_DEFAULT_LISTEN, false},
		{"too many", []string{"a", "b"}, nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse_tls_service_listen(tc.values, tc.argv)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestTlsServiceEndpointsAndCertificateSNI(t *testing.T) {
	rootCertPEM, rootKeyPEM := test_tls_root(t)
	rndis := usb.NewUsbGadgetRndis(netip.MustParsePrefix("127.0.0.1/8"), "", "", "", "lo", "", nil, netip.Addr{}, time.Second, time.Second, "", "", "")
	daemon := &Daemon{modes: []usb.UsbGadgetFunction{rndis}}
	service, err := NewTlsService("127.0.0.1:0", daemon, rootCertPEM, rootKeyPEM, "ecdsa-p256")
	if err != nil {
		t.Fatalf("NewTlsService: %v", err)
	}
	if service.leaf_duration != 26*time.Hour {
		t.Fatalf("default leaf duration = %s, want 26h", service.leaf_duration)
	}

	t.Run("ask accepts RNDIS IP", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ask?domain=127.0.0.1", nil)
		service.handle_ask(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})
	t.Run("ask rejects other IP", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ask?domain=10.22.33.1", nil)
		service.handle_ask(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})
	t.Run("generate returns SNI certificate bundle", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/generate?server_name=example.test&signature_schemes=0403&cipher_suites=1301&local_ip=127.0.0.1", nil)
		service.handle_generate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		pair, err := tls.X509KeyPair(rr.Body.Bytes(), rr.Body.Bytes())
		if err != nil {
			t.Fatalf("Caddy PEM certificate/key bundle is invalid: %v", err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatalf("parse leaf certificate: %v", err)
		}
		if leaf.Subject.CommonName != "example.test" || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "example.test" {
			t.Fatalf("SNI was not written to certificate identity: subject=%q dns=%v", leaf.Subject.CommonName, leaf.DNSNames)
		}
		if len(pair.Certificate) != 2 {
			t.Fatalf("expected only leaf and matching root in returned chain, got %d certificates", len(pair.Certificate))
		}
	})
	t.Run("generate rejects non-RNDIS local IP", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/generate?server_name=example.test&local_ip=10.22.33.1", nil)
		service.handle_generate(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})
	t.Run("generate rejects non-RNDIS IP SNI", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/generate?server_name=10.22.33.1&local_ip=127.0.0.1", nil)
		service.handle_generate(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})
	t.Run("empty SNI falls back to RNDIS IP", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/generate?server_name=&local_ip=127.0.0.1", nil)
		service.handle_generate(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		pair, err := tls.X509KeyPair(rr.Body.Bytes(), rr.Body.Bytes())
		if err != nil {
			t.Fatalf("parse certificate bundle: %v", err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatalf("parse leaf certificate: %v", err)
		}
		if leaf.Subject.CommonName != "127.0.0.1" || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
			t.Fatalf("empty SNI did not fall back to RNDIS IP: subject=%q ips=%v", leaf.Subject.CommonName, leaf.IPAddresses)
		}
	})
}

func TestTlsLeafDuration(t *testing.T) {
	rootCertPEM, rootKeyPEM := test_tls_root(t)
	rndis := usb.NewUsbGadgetRndis(netip.MustParsePrefix("127.0.0.1/8"), "", "", "", "lo", "", nil, netip.Addr{}, time.Second, time.Second, "", "", "")
	service, err := NewTlsService("", &Daemon{modes: []usb.UsbGadgetFunction{rndis}}, rootCertPEM, rootKeyPEM, "ecdsa-p256", 2*time.Hour)
	if err != nil {
		t.Fatalf("NewTlsService with custom leaf duration: %v", err)
	}
	certPEM, _, err := service.generate_certificate("duration.test", "0403")
	if err != nil {
		t.Fatalf("generate certificate: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse generated certificate: %v", err)
	}
	remaining := time.Until(leaf.NotAfter)
	if remaining < 2*time.Hour-time.Minute || remaining > 2*time.Hour {
		t.Fatalf("custom leaf duration = %s, want approximately 2h", remaining)
	}
	if _, err := NewTlsService("", &Daemon{}, rootCertPEM, rootKeyPEM, "ecdsa-p256", 0); err == nil {
		t.Fatal("expected zero leaf duration to be rejected")
	}
}

func TestTlsServiceDefaultsToECDSAP384(t *testing.T) {
	rootCertPEM, rootKeyPEM := test_tls_root(t)
	service, err := NewTlsService("", &Daemon{}, rootCertPEM, rootKeyPEM, "")
	if err != nil {
		t.Fatalf("NewTlsService with default algorithm: %v", err)
	}
	if service.algorithm != "ecdsa-p384" {
		t.Fatalf("default TLS leaf algorithm = %q, want ecdsa-p384", service.algorithm)
	}
}

func TestGenerateLeafKeyUsesCaddySignatureSchemes(t *testing.T) {
	key, err := generate_leaf_key("ecdsa-p256", "0804,0403")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := key.(*ecdsa.PrivateKey); !ok {
		t.Fatalf("expected ECDSA key for first supported scheme, got %T", key)
	}
	if _, err = generate_leaf_key("ecdsa-p256", "0804"); err == nil {
		t.Fatal("expected error when signature schemes exclude the configured ECDSA curve")
	}
	if supported_tls_algorithm("rsa2048") || !supported_tls_algorithm("rsa4096") || !supported_tls_algorithm("rsa8192") || supported_tls_algorithm("rsa4097") {
		t.Fatal("RSA algorithm validation should require at least 4096 bits in 1024-bit increments")
	}
	if _, err = generate_leaf_key("rsa4096", "0403"); err == nil {
		t.Fatal("expected error when signature schemes exclude RSA")
	}
	key, err = generate_leaf_key("rsa4096", "0804")
	if err != nil {
		t.Fatalf("generate rsa4096 leaf key: %v", err)
	}
	if rsaKey, ok := key.(*rsa.PrivateKey); !ok || rsaKey.N.BitLen() < 4096 {
		t.Fatalf("expected RSA key of at least 4096 bits, got %T", key)
	}
}

func test_tls_root(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(72 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
