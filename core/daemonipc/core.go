package daemonipc

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/lovemilk2333/wifi-stick-usb-switcher/core/typeinject"
)

/*
package struct
NOTE: all data is big-endian

| payload (JSON) |
| $dynamic$      |
*/

type IPCPackageType int

type IPCPackage struct {
	Type    IPCPackageType
	Payload []any
}

type IPCHandlerStatus uint8

const (
	HANDLER_STATUS_PAYLOAD_STRUCT_NOT_FOUND = iota
	HANDLER_STATUS_INVALID_PAYLOAD
	HANDLER_STATUS_INVALID_HANDLER
	HANDLER_STATUS_INVALID_HANDLER_RESULT
)

// func(this *IPCFramework, <any length of args>) (*IPCPackage, error)
type IPCFrameworkHandler any
type IPCFrameworkFallbackHandler func(this *IPCFramework, status IPCHandlerStatus, err error, package_type IPCPackageType, data []byte) (*IPCPackage, error)
type IPCFrameworkPrePackageHandler func(this *IPCFramework, package_type IPCPackageType, payload []reflect.Value) ([]reflect.Value, error)
type IPCFrameworkPostPackageHandler func(this *IPCFramework, pkg *IPCPackage, err error) (*IPCPackage, error)

// https://pkg.go.dev/github.com/james-barrow/golang-ipc#Client
// https://pkg.go.dev/github.com/james-barrow/golang-ipc#Server
type IPCImpl interface {
	Close()
	Read() (*ipc.Message, error)
	Status() string
	StatusCode() ipc.Status
	Write(msgType int, message []byte) error
}

// shared between client and server
var handler_structs = make(map[IPCPackageType]typeinject.DepInjectFieldMetadatas)

type IPCFramework struct {
	handlers map[IPCPackageType]IPCFrameworkHandler

	ipc_impl IPCImpl
	ipc_flag sync.WaitGroup

	fallback_handler     IPCFrameworkFallbackHandler
	pre_package_handler  IPCFrameworkPrePackageHandler
	post_package_handler IPCFrameworkPostPackageHandler
}

func NewIPCFramework() *IPCFramework {
	return &IPCFramework{
		handlers: make(map[IPCPackageType]IPCFrameworkHandler),
	}
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
			// After a Read error the channel is closed; further reads repeat it, so exit.
			log.Printf("WARN: package cannot receive: %v", err)
			break
		}

		if msg.MsgType <= 0 {
			continue
		}

		err = this.handle_data(IPCPackageType(msg.MsgType), msg.Data)
		if err != nil {
			log.Printf("WARN: package handle error: %v", err)
			continue
		}
	}
}

func (this *IPCFramework) get_payload_struct_by_handler(function any) (typeinject.DepInjectFieldMetadatas, error) {
	if function == nil {
		return nil, fmt.Errorf("`function` cannot be nil")
	}

	type_ := reflect.TypeOf(function)
	if type_.Kind() != reflect.Func {
		return nil, fmt.Errorf("`function` is not a function")
	}

	if !type_.In(0).AssignableTo(reflect.TypeFor[*IPCFramework]()) {
		return nil, fmt.Errorf("`function`'s first argv must be `*IPCFramework`")
	}

	metas, err := typeinject.GetStructByFunctionType(type_, reflect.TypeFor[*IPCFramework]())
	if err != nil {
		return nil, err
	}

	// payload metas exclude the static *IPCFramework first argument
	return metas[1:], nil
}

func (this *IPCFramework) parse_data(metas typeinject.DepInjectFieldMetadatas, data []byte) ([]reflect.Value, error) {
	payload, err := typeinject.ParseJsonPayloadWithMetas(metas, data)
	if err != nil {
		return nil, err
	}

	return typeinject.Args2values(payload), nil
}

func (this *IPCFramework) check_data(package_type IPCPackageType, data []byte) error {
	payload_struct := this.GetPayloadStruct(package_type)
	if payload_struct == nil {
		return fmt.Errorf("package cannot get payload struct for `%d`", package_type)
	}

	_, err := this.parse_data(payload_struct, data)
	if err != nil {
		return fmt.Errorf("package cannot parse payload: no payload struct for `%d`: %w", package_type, err)
	}

	return nil
}

func (this *IPCFramework) handle_data(package_type IPCPackageType, data []byte) error {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("WARN: package call handler panic: %v", r)
		}
	}()

	fallback_handler := this.fallback_handler
	// if fallback_handler == nil {
	// 	// fallback of `fallback_handler`
	// 	fallback_handler = func(this *IPCFramework, status IPCHandlerStatus, err error, package_type IPCPackageType, data []byte) (*IPCPackage, error) {
	// 		return nil, err
	// 	}
	// }

	// fix: goto handle_resp jumps over variable declaration at line 147
	var (
		resp_package   *IPCPackage
		payload        []reflect.Value
		err            error
		handler        reflect.Value
		handler_result []reflect.Value
	)

	payload_struct := this.GetPayloadStruct(package_type)
	if payload_struct == nil {
		if fallback_handler == nil {
			return fmt.Errorf("package cannot get payload struct for `%d`: %w", package_type, err)
		}

		resp_package, err = fallback_handler(this, HANDLER_STATUS_PAYLOAD_STRUCT_NOT_FOUND, nil, package_type, data)
		if err != nil {
			return fmt.Errorf("package cannot get payload struct for `%d`: %w", package_type, err)
		}

		goto handle_resp
	}

	payload, err = this.parse_data(this.GetPayloadStruct(package_type), data)
	if err != nil {
		if fallback_handler == nil {
			return fmt.Errorf("package cannot parse payload: no payload struct for `%d`: %w", package_type, err)
		}

		resp_package, err = fallback_handler(this, HANDLER_STATUS_INVALID_PAYLOAD, err, package_type, data)
		if err != nil {
			return fmt.Errorf("package cannot parse payload: no payload struct for `%d`: %w", package_type, err)
		}

		goto handle_resp
	}

	handler = reflect.ValueOf(this.GetHandler(package_type))
	if handler.Kind() != reflect.Func {
		if fallback_handler == nil {
			return fmt.Errorf("package cannot handle: invalid handler for `%d`", package_type)
		}

		resp_package, err = fallback_handler(this, HANDLER_STATUS_INVALID_HANDLER, nil, package_type, data)
		if err != nil {
			return fmt.Errorf("package cannot handle: invalid handler for `%d`", package_type)
		}

		goto handle_resp
	}

	if this.pre_package_handler != nil {
		pre_handler_payload, pre_err := this.pre_package_handler(this, package_type, payload)
		if pre_err != nil {
			err = pre_err
			goto handle_resp
		}

		if pre_handler_payload != nil {
			// overwrite payload
			payload = pre_handler_payload
		}
	}

	handler_result = handler.Call(append(
		[]reflect.Value{reflect.ValueOf(this)}, payload...,
	))

	switch len(handler_result) {
	case 0:
		break // no data
	case 2:
		if !handler_result[0].IsNil() { // *IPCPackage
			resp_package = handler_result[0].Interface().(*IPCPackage)
		}

		if !handler_result[1].IsNil() { // err
			err = handler_result[1].Interface().(error)
		}
	default:
		if fallback_handler == nil {
			return fmt.Errorf("package cannot handle: invalid handler result for `%d`", package_type)
		}

		resp_package, err = fallback_handler(this, HANDLER_STATUS_INVALID_HANDLER_RESULT, nil, package_type, data)
		if err != nil {
			return fmt.Errorf("package cannot handle: invalid handler result for `%d`", package_type)
		}

		goto handle_resp
	}

	if this.post_package_handler != nil {
		post_resp, post_err := this.post_package_handler(this, resp_package, err)
		if post_resp != nil {
			resp_package = post_resp
		}

		if post_err != nil {
			err = post_err
		}
	}

handle_resp:
	if err != nil {
		return fmt.Errorf("package call handler error: %w", err)
	} else if resp_package != nil {
		return this.SendPackage(resp_package)
	}
	// ignore no resp package

	return nil
}

func (this *IPCFramework) payload2json(payload []any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return data, nil
}

/*
send package with non-convert typed payload, which each payload item is string
*/
func (this *IPCFramework) SendRaw(package_type IPCPackageType, non_converted_payload []string) error {
	buffer := strings.NewReader("")
	decoder := json.NewDecoder(buffer)
	decoder.UseNumber()

	payload_struct := this.GetPayloadStruct(package_type)

	payload := make([]any, len(non_converted_payload))
	for index, value := range non_converted_payload {
		if payload_struct[index].Type.Kind() == reflect.String {
			// keep original if the target type is string
			payload[index] = value
			continue
		}

		buffer.Reset(value)
		err := decoder.Decode(&payload[index])
		if err != nil {
			return err
		}
	}

	data, err := this.payload2json(payload)
	if err != nil {
		return err
	}

	// validate payload struct
	err = this.check_data(package_type, data)
	if err != nil {
		return err
	}

	return this.ipc_impl.Write(
		int(package_type),
		data,
	)
}

/*
reply package with go typed payload
*/
func (this *IPCFramework) SendPackage(pkg *IPCPackage) error {
	data, err := this.payload2json(pkg.Payload)
	if err != nil {
		return err
	}

	// validate payload struct
	err = this.check_data(pkg.Type, data)
	if err != nil {
		return err
	}

	return this.ipc_impl.Write(
		int(pkg.Type),
		data,
	)
}

func (this *IPCFramework) RegisterHandler(package_type IPCPackageType, handler IPCFrameworkHandler) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	if _, ok := this.handlers[package_type]; ok {
		return fmt.Errorf("package handler for `%d` already registered", package_type)
	}

	return this.RegisterHandlerReplace(package_type, handler)
}

func (this *IPCFramework) RegisterHandlerReplace(package_type IPCPackageType, handler IPCFrameworkHandler) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	this.handlers[package_type] = handler
	payload_struct, err := this.get_payload_struct_by_handler(handler)
	if err != nil {
		return err
	}
	handler_structs[package_type] = payload_struct
	return nil
}

func (this *IPCFramework) RemoveHandler(package_type IPCPackageType) error {
	if package_type <= 0 {
		return fmt.Errorf("package type must >= 0, got `%d`", package_type)
	}

	delete(this.handlers, package_type)
	delete(handler_structs, package_type)
	return nil
}

func (this *IPCFramework) GetHandler(package_type IPCPackageType) IPCFrameworkHandler {
	return this.handlers[package_type]
}

func (this *IPCFramework) GetPayloadStruct(package_type IPCPackageType) typeinject.DepInjectFieldMetadatas {
	return handler_structs[package_type]
}

func (this *IPCFramework) GetFallbackHandler() IPCFrameworkFallbackHandler {
	return this.fallback_handler
}

func (this *IPCFramework) SetFallbackHandler(handler IPCFrameworkFallbackHandler) {
	this.fallback_handler = handler
}

func (this *IPCFramework) GetPrePackageHandler() IPCFrameworkPrePackageHandler {
	return this.pre_package_handler
}

func (this *IPCFramework) SetPrePackageHandler(handler IPCFrameworkPrePackageHandler) {
	this.pre_package_handler = handler
}

func (this *IPCFramework) GetPostPackageHandler() IPCFrameworkPostPackageHandler {
	return this.post_package_handler
}

func (this *IPCFramework) SetPostPackageHandler(handler IPCFrameworkPostPackageHandler) {
	this.post_package_handler = handler
}
