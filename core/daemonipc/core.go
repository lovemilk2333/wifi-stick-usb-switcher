package daemonipc

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sync"

	"github.com/go-playground/validator/v10"
	ipc "github.com/james-barrow/golang-ipc"
)

var validate = validator.New(validator.WithRequiredStructEnabled())

/*
package struct
NOTE: all data is big-endian

| payload (JSON) |
| $dynamic$      |
*/

type IPCPackageType int

type IPCPackage struct {
	Type    IPCPackageType
	Payload any // any json
}

type IPCServerHandler func(this *IPCServer, payload any) (*IPCPackage, error)

type IPCServer struct {
	ident  string
	config *ipc.ServerConfig

	handlers        map[IPCPackageType]IPCServerHandler
	handler_structs map[IPCPackageType]reflect.Type

	server      *ipc.Server
	server_flag sync.WaitGroup
}

func NewIPCServer(ident string) *IPCServer {
	return &IPCServer{
		ident: ident,
	}
}

func (this *IPCServer) WithConfig(config *ipc.ServerConfig) *IPCServer {
	this.config = config

	return this
}

func (this *IPCServer) Start() error {
	if this.server != nil {
		return fmt.Errorf("server has already running")
	}

	server, err := ipc.StartServer(
		this.ident,
		this.config,
	)

	if err != nil {
		return err
	}

	this.server_flag.Add(1)

	this.server = server
	go this.mainloop()

	return nil
}

func (this *IPCServer) Stop() {
	this.server.Close()
	// wait goroutine
	this.server_flag.Wait()
	this.server = nil
}

func (this *IPCServer) mainloop() {
	defer this.server_flag.Done()

	for {
		msg, err := this.server.Read()
		if err != nil {
			log.Printf("WARN: package cannot receive: %v", err)
		}

		if msg.MsgType <= 0 {
			continue
		}

		err = this.handle_data(IPCPackageType(msg.MsgType), msg.Data)
		if err != nil {
			log.Printf("WARN: package handle error: %v", err)
		}
	}
}

func (this *IPCServer) parse_package(package_type IPCPackageType, data []byte) (*IPCPackage, error) {
	payload_struct, ok := this.handler_structs[package_type]
	if !ok {
		return nil, fmt.Errorf("package cannot parse payload: no payload struct for `%d`", package_type)
	}

	payload := reflect.New(payload_struct).Interface()

	err := json.Unmarshal(data, payload)
	if err != nil {
		return nil, fmt.Errorf("package cannot parse payload as json: %w", err)
	}

	err = validate.Struct(payload)
	if err != nil {
		return nil, fmt.Errorf("package invalid payload: %w", err)
	}

	return &IPCPackage{
		Type:    package_type,
		Payload: payload,
	}, nil
}

func (this *IPCServer) handle_data(package_type IPCPackageType, data []byte) error {
	ipc_package, err := this.parse_package(package_type, data)
	if err != nil {
		return err
	}

	handler, ok := this.handlers[ipc_package.Type]
	if !ok {
		return fmt.Errorf("package cannot handle: missing handler for `%d`", ipc_package.Type)
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("WARN: package call handler panic: %v", r)
		}
	}()

	resp_package, err := handler(this, ipc_package.Payload)
	if err != nil {
		return fmt.Errorf("package call handler error: %w", err)
	} else if resp_package != nil {
		return this.Send(resp_package)
	}

	return nil
}

func (this *IPCServer) pkg2binary(pkg *IPCPackage) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("package cannot convert to binary: package is nil")
	}

	pkg_binary, err := json.Marshal(pkg.Payload)
	if err != nil {
		return nil, fmt.Errorf("package cannot convert to binary: %w", err)
	}

	return pkg_binary, nil
}

func (this *IPCServer) Send(pkg *IPCPackage) error {
	payload, err := this.pkg2binary(pkg)
	if err != nil {
		return err
	}

	return this.server.Write(
		int(pkg.Type),
		payload,
	)
}

func (this *IPCServer) RegisterHandler(package_type IPCPackageType, payload_struct reflect.Type, handler IPCServerHandler) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	if _, ok := this.handlers[package_type]; !ok {
		return fmt.Errorf("package handler for `%d` already registered", package_type)
	}

	return this.RegisterHandlerReplace(package_type, payload_struct, handler)
}

func (this *IPCServer) RegisterHandlerReplace(package_type IPCPackageType, payload_struct reflect.Type, handler IPCServerHandler) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	this.handlers[package_type] = handler
	this.handler_structs[package_type] = payload_struct
	return nil
}

func (this *IPCServer) RemoveHandler(package_type IPCPackageType) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	delete(this.handlers, package_type)
	delete(this.handler_structs, package_type)
	return nil
}
