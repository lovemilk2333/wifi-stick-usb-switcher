//go:build !dbus

package daemon

import (
	"log"
	"time"

	transport "github.com/james-barrow/golang-ipc"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
)

func (this *Daemon) init_ipc(allow_other_user bool) error {
	daemonipc.InitClient()
	this.ipc_allow_other_user = allow_other_user
	return nil
}

func (this *Daemon) run_ipc_server() {
	for {
		server, err := transport.StartServer(base.PROJECT_IDENT, &transport.ServerConfig{Encryption: false, UnmaskPermissions: this.ipc_allow_other_user})
		if err != nil {
			log.Printf("WARN: IPC server start: %v", err)
		} else {
			framework := daemonipc.InitServer(this)
			if err := framework.Start(server); err != nil {
				log.Printf("WARN: IPC framework start: %v", err)
				server.Close()
			} else {
				framework.Wait()
				log.Printf("WARN: IPC server disconnected; restarting")
			}
		}
		select {
		case <-this.stop_chan:
			return
		case <-time.After(time.Second):
		}
	}
}
