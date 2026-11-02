//go:build dbus

package daemon

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
)

const (
	DBUS_OBJECT_PATH = dbus.ObjectPath("/io/github/lovemilk2333/wifi_stick_usb_switcher/go/Control")
	DBUS_INTERFACE   = "io.github.lovemilk2333.wifi_stick_usb_switcher.go.Control"
	DBUS_BUS_NAME    = "io.github.lovemilk2333.wifi-stick-usb-switcher.go"
	DBUS_POLICY_PATH = "/etc/dbus-1/system.d/io.github.lovemilk2333.wifi-stick-usb-switcher.go.conf"
)

//go:embed dbus_policy.conf
var dbus_policy string

type dbusService struct {
	connection       *dbus.Conn
	allow_other_user bool
	framework        *daemonipc.IPCFramework
	request_mutex    sync.Mutex
}

func (this *dbusService) Request(sender dbus.Sender, package_type uint32, payload_json string) (uint32, string, *dbus.Error) {
	this.request_mutex.Lock()
	defer this.request_mutex.Unlock()

	if !this.allow_other_user {
		var uid uint32
		if err := this.connection.BusObject().Call("org.freedesktop.DBus.GetConnectionUnixUser", 0, string(sender)).Store(&uid); err != nil {
			return 0, "", dbus.MakeFailedError(fmt.Errorf("identify D-Bus caller: %w", err))
		}
		if uint32(os.Getuid()) != uid {
			return 0, "", dbus.MakeFailedError(fmt.Errorf("D-Bus access denied for uid %d", uid))
		}
	}
	response, err := this.framework.HandleRequest(daemonipc.IPCPackageType(package_type), []byte(payload_json))
	if err != nil {
		return 0, "", dbus.MakeFailedError(err)
	}
	if response == nil {
		return 0, "", nil
	}
	data, err := json.Marshal(response.Payload)
	if err != nil {
		return 0, "", dbus.MakeFailedError(err)
	}
	return uint32(response.Type), string(data), nil
}

func (this *Daemon) init_ipc(allow_other_user bool) error {
	this.ipc_allow_other_user = allow_other_user
	return nil
}

func (this *Daemon) run_ipc_server() {
	for {
		if err := this.serve_dbus(); err != nil {
			log.Printf("WARN: D-Bus control service stopped: %v", err)
		}
		select {
		case <-this.stop_chan:
			return
		case <-time.After(time.Second):
		}
	}
}

func (this *Daemon) serve_dbus() error {
	policy_changed, err := ensure_dbus_policy(DBUS_POLICY_PATH)
	if err != nil {
		return fmt.Errorf("install D-Bus system policy: %w (ensure the service unit permits writes to /etc/dbus-1/system.d)", err)
	}
	if policy_changed {
		log.Printf("INFO installed D-Bus system policy: %s", DBUS_POLICY_PATH)
	}

	connection, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("system bus connection failed: %w", err)
	}
	defer connection.Close()
	if policy_changed {
		if err := connection.BusObject().Call("org.freedesktop.DBus.ReloadConfig", 0).Err; err != nil {
			return fmt.Errorf("reload D-Bus configuration after installing %s: %w", DBUS_POLICY_PATH, err)
		}
		log.Printf("INFO reloaded system-bus policy")
	}
	reply, err := connection.RequestName(DBUS_BUS_NAME, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("request D-Bus name %s as uid %d: %w (the system-bus policy must allow this uid to own the name; install the matching %s.conf policy in /etc/dbus-1/system.d and restart the system bus)", DBUS_BUS_NAME, os.Geteuid(), err, DBUS_BUS_NAME)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("cannot own D-Bus name %s as uid %d: name is already owned or unavailable (reply %d)", DBUS_BUS_NAME, os.Geteuid(), reply)
	}
	service := &dbusService{
		connection:       connection,
		allow_other_user: this.ipc_allow_other_user,
		framework:        daemonipc.InitServer(this),
	}
	if err := connection.Export(service, DBUS_OBJECT_PATH, DBUS_INTERFACE); err != nil {
		return fmt.Errorf("export D-Bus control service: %w", err)
	}
	log.Printf("INFO D-Bus control service ready: %s", DBUS_BUS_NAME)
	<-this.stop_chan
	return nil
}

func ensure_dbus_policy(path string) (bool, error) {
	expectedRule := `<allow own="` + DBUS_BUS_NAME + `"/>`
	existing, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(existing), expectedRule) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(directory, ".wifi-stick-dbus-policy-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0644); err != nil {
		temporary.Close()
		return false, err
	}
	if _, err := temporary.WriteString(dbus_policy); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, err
	}
	return true, nil
}
