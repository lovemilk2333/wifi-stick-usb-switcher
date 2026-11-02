//go:build dbus

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDBusPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.d", "wifi-stick.conf")

	changed, err := ensure_dbus_policy(path)
	if err != nil {
		t.Fatalf("install missing policy: %v", err)
	}
	if !changed {
		t.Fatal("expected missing policy to be installed")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed policy: %v", err)
	}
	if string(content) != dbus_policy {
		t.Fatal("installed policy differs from embedded policy")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat installed policy: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Fatalf("policy mode = %04o, want 0644", got)
	}

	changed, err = ensure_dbus_policy(path)
	if err != nil {
		t.Fatalf("check existing policy: %v", err)
	}
	if changed {
		t.Fatal("valid existing policy was unnecessarily rewritten")
	}
}

func TestEnsureDBusPolicyReplacesStaleNamePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.d", "wifi-stick.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`<allow own="org.wifistick.WifiStick"/>`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := ensure_dbus_policy(path)
	if err != nil {
		t.Fatalf("update stale policy: %v", err)
	}
	if !changed {
		t.Fatal("expected stale service-name policy to be replaced")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `<allow own="`+DBUS_BUS_NAME+`"/>`) {
		t.Fatal("updated policy does not authorize the current service name")
	}
}

func TestEmbeddedDBusPolicyMatchesReleaseAsset(t *testing.T) {
	releasePolicy, err := os.ReadFile(filepath.Join("..", "..", "dbus", "io.github.lovemilk2333.wifi-stick-usb-switcher.go.conf"))
	if err != nil {
		t.Fatalf("read release policy asset: %v", err)
	}
	if string(releasePolicy) != dbus_policy {
		t.Fatal("release policy asset differs from embedded policy")
	}
}
