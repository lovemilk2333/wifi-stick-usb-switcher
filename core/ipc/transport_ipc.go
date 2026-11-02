//go:build !dbus

package ipc

import (
	"fmt"
	"time"

	transport "github.com/james-barrow/golang-ipc"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/base"
	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/daemonipc"
)

type streamClient struct {
	framework *daemonipc.IPCFramework
	responses daemonipc.IPCClientRespChannel
}

func (this *streamClient) Call(package_type daemonipc.IPCPackageType, payload []any, timeout time.Duration) (string, error) {
	if err := this.framework.SendPackage(&daemonipc.IPCPackage{Type: package_type, Payload: payload}); err != nil {
		return "", fmt.Errorf("send IPC request: %w", err)
	}
	select {
	case response := <-this.responses:
		return response, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("IPC response timed out after %s", timeout)
	}
}

func (this *streamClient) Close() error { return nil }

// InitIPCClient connects to the daemon using the legacy stream transport.
func InitIPCClient(connect_timeout, dial_retry time.Duration) (IPCClient, error) {
	daemonipc.InitServer(nil)
	framework, responses := daemonipc.InitClient()
	client, err := transport.StartClient(base.PROJECT_IDENT, &transport.ClientConfig{
		Encryption: false,
		Timeout:    connect_timeout.Seconds(),
		RetryTimer: dial_retry,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to IPC daemon: %w", err)
	}
	if err := framework.Start(client); err != nil {
		client.Close()
		return nil, fmt.Errorf("start IPC client: %w", err)
	}
	deadline := time.Now().Add(connect_timeout)
	for client.StatusCode() != transport.Connected {
		if time.Now().After(deadline) {
			framework.Stop()
			return nil, fmt.Errorf("IPC connection timed out: %s", client.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &streamClient{framework: framework, responses: responses}, nil
}
