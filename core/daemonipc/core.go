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

type IPCFrameworkHandler func(this *IPCFramework, payload any) (*IPCPackage, error)
type IPCFrameworkFallbackHandler func(this *IPCFramework, package_type IPCPackageType, data []byte) (*IPCPackage, error)

// https://pkg.go.dev/github.com/james-barrow/golang-ipc#Client
// https://pkg.go.dev/github.com/james-barrow/golang-ipc#Server
type IPCImpl interface {
	Close()
	Read() (*ipc.Message, error)
	Status() string
	StatusCode() ipc.Status
	Write(msgType int, message []byte) error
}

type IPCFramework struct {
	handlers        map[IPCPackageType]IPCFrameworkHandler
	handler_structs map[IPCPackageType]reflect.Type

	ipc_impl IPCImpl
	ipc_flag sync.WaitGroup

	fallback_handler IPCFrameworkFallbackHandler
}

func NewIPCFramework() *IPCFramework {
	return &IPCFramework{}
}

func (this *IPCFramework) Start(ipc_impl IPCImpl) error {
	if this.ipc_impl != nil {
		return fmt.Errorf("ipc server has already running")
	}

	this.ipc_impl = ipc_impl
	this.ipc_flag.Add(1)

	go this.mainloop()

	return nil
}

func (this *IPCFramework) Stop() {
	this.ipc_impl.Close()
	// wait goroutine
	this.ipc_flag.Wait()
	this.ipc_impl = nil
}

func (this *IPCFramework) Wait() {
	this.ipc_flag.Wait()
}

func (this *IPCFramework) mainloop() {
	defer this.ipc_flag.Done()

	for {
		msg, err := this.ipc_impl.Read()
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

func (this *IPCFramework) parse_package(package_type IPCPackageType, payload_struct reflect.Type, data []byte) (*IPCPackage, error) {
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

func (this *IPCFramework) handle_data(package_type IPCPackageType, data []byte) error {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("WARN: package call handler panic: %v", r)
		}
	}()

	// fix: goto handle_resp jumps over variable declaration at line 147
	var (
		resp_package *IPCPackage
		ipc_package  *IPCPackage
		err          error
		handler      IPCFrameworkHandler
		ok           bool
	)

	payload_struct, ok := this.handler_structs[package_type]
	if !ok {
		resp_package, err = this.fallback_handler(this, package_type, data)
		if err != nil {
			return fmt.Errorf("package cannot get payload struct for `%d`: %w", package_type, err)
		}

		goto handle_resp
	}

	ipc_package, err = this.parse_package(package_type, payload_struct, data)
	if err != nil {
		resp_package, err = this.fallback_handler(this, package_type, data)
		if err != nil {
			return fmt.Errorf("package cannot parse payload: no payload struct for `%d`: %w", package_type, err)
		}

		goto handle_resp
	}

	handler, ok = this.handlers[ipc_package.Type]
	if !ok {
		return fmt.Errorf("package cannot handle: missing handler for `%d`", ipc_package.Type)
	}
	resp_package, err = handler(this, ipc_package.Payload)

handle_resp:
	if err != nil {
		return fmt.Errorf("package call handler error: %w", err)
	} else if resp_package != nil {
		return this.Send(resp_package)
	}

	return nil
}

func (this *IPCFramework) pkg2payload(pkg *IPCPackage) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("package cannot convert to binary: package is nil")
	}

	payload, err := json.Marshal(pkg.Payload)
	if err != nil {
		return nil, fmt.Errorf("package cannot convert to binary: %w", err)
	}

	return payload, nil
}

func (this *IPCFramework) Send(pkg *IPCPackage) error {
	payload, err := this.pkg2payload(pkg)
	if err != nil {
		return err
	}

	return this.ipc_impl.Write(
		int(pkg.Type),
		payload,
	)
}

func (this *IPCFramework) RegisterHandler(package_type IPCPackageType, payload_struct reflect.Type, handler IPCFrameworkHandler) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	if _, ok := this.handlers[package_type]; !ok {
		return fmt.Errorf("package handler for `%d` already registered", package_type)
	}

	return this.RegisterHandlerReplace(package_type, payload_struct, handler)
}

func (this *IPCFramework) RegisterHandlerReplace(package_type IPCPackageType, payload_struct reflect.Type, handler IPCFrameworkHandler) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	this.handlers[package_type] = handler
	this.handler_structs[package_type] = payload_struct
	return nil
}

func (this *IPCFramework) RemoveHandler(package_type IPCPackageType) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	delete(this.handlers, package_type)
	delete(this.handler_structs, package_type)
	return nil
}

func (this *IPCFramework) GetFallbackHandler() IPCFrameworkFallbackHandler {
	return this.fallback_handler
}

func (this *IPCFramework) SetFallbackHandler(handler IPCFrameworkFallbackHandler) {
	this.fallback_handler = handler
}
