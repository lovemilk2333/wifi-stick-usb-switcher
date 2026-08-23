package daemonipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	// "math"
	"reflect"
	"sync"

	ipc "github.com/james-barrow/golang-ipc"
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
var handler_structs = make(map[IPCPackageType][]reflect.Type)

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

func (this *IPCFramework) get_payload_struct_by_handler(function any) ([]reflect.Type, error) {
	if function == nil {
		return nil, fmt.Errorf("`function` cannot be nil")
	}

	type_ := reflect.TypeOf(function)
	if type_.Kind() != reflect.Func {
		return nil, fmt.Errorf("`function` is not a function")
	}

	arg_count := type_.NumIn()
	if arg_count < 1 {
		return nil, fmt.Errorf("`function` must have more than (equals) 1 args")
	}

	if !type_.In(0).AssignableTo(reflect.TypeFor[*IPCFramework]()) {
		return nil, fmt.Errorf("`function`'s first argv must be `*IPCFramework`")
	}

	payload_struct := make([]reflect.Type, 0, arg_count-1)
	for i := 1; i < arg_count; i++ {
		payload_struct = append(payload_struct, type_.In(i))
	}

	return payload_struct, nil
}

const FLOAT64_MIN_EXACT_INT = -1 << 53
const FLOAT64_MAX_EXACT_INT = 1 << 53

// func (this *IPCFramework) payload_handle_number(value float64, target reflect.Type) (reflect.Value, error) {
// 	if math.IsNaN(value) || math.IsInf(value, 0) {
// 		return reflect.Value{}, fmt.Errorf("value is inf or nan")
// 	}

// 	kind := target.Kind()
// 	valid_int := value >= FLOAT64_MIN_EXACT_INT && value <= FLOAT64_MAX_EXACT_INT && math.Trunc(value) == value

// 	switch kind {
// 	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
// 		if !valid_int {
// 			return reflect.Value{}, fmt.Errorf("value is not integer")
// 		}

// 		int_val := int64(value)
// 		if target.OverflowInt(int_val) {
// 			return reflect.Value{}, fmt.Errorf("value overflowed for %s", target.Name())
// 		}

// 		return reflect.ValueOf(int_val).Convert(target), nil
// 	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
// 		if !valid_int {
// 			return reflect.Value{}, fmt.Errorf("value is not a integer")
// 		}

// 		if value < 0 {
// 			return reflect.Value{}, fmt.Errorf("negative value for %s", target.Name())
// 		}

// 		uint_val := uint64(value)
// 		if target.OverflowUint(uint_val) {
// 			return reflect.Value{}, fmt.Errorf("value overflowed for %s", target.Name())
// 		}

// 		return reflect.ValueOf(uint_val).Convert(target), nil
// 	case reflect.Float32, reflect.Float64:
// 		if target.OverflowFloat(value) {
// 			return reflect.Value{}, fmt.Errorf("value overflowed for %s", target.Name())
// 		}

// 		return reflect.ValueOf(value).Convert(target), nil
// 	default:
// 		return reflect.Value{}, fmt.Errorf("target type is not a number type (got %s)", target.Name())
// 	}
// }

func (this *IPCFramework) parse_payload(payload_struct []reflect.Type, payload_original []any) ([]reflect.Value, error) {
	payload_length := len(payload_original)
	if payload_length != len(payload_struct) {
		return nil, fmt.Errorf("invalid payload length: expected %d args, but got %d", len(payload_struct), len(payload_original))
	}

	error_count := 0
	message := ""
	payload := make([]reflect.Value, 0, payload_length)

	for index, type_ := range payload_struct {
		value := payload_original[index]

		if value == nil {
			if type_.Kind() != reflect.Interface && type_.Kind() != reflect.Pointer {
				message += fmt.Sprintf("arg %d: expected %v, but got nil", index, type_)
				error_count++
			}
			continue
		}

		actual_type := reflect.TypeOf(value)

		// if actual_type.Kind() == reflect.Float64 {
		// 	val, err := this.payload_handle_number(value.(float64), type_)
		// 	if err != nil {
		// 		message += err.Error()
		// 		error_count++
		// 		continue
		// 	}
		// 	payload = append(payload, val)
		// } else if !actual_type.AssignableTo(type_) {
		if !actual_type.AssignableTo(type_) {
			message += fmt.Sprintf("invalid payload arg `%d`: excepted type %s, but got %s: %v", index, type_, actual_type, value)
			error_count++
			continue
		} else { // valid type
			payload = append(payload, reflect.ValueOf(value).Convert(type_))
		}
	}

	if error_count > 0 {
		return nil, errors.New(message)
	} else {
		return payload, nil
	}
}

func (this *IPCFramework) parse_data(payload_struct []reflect.Type, data []byte) ([]reflect.Value, error) {
	var payload_original []any

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err := decoder.Decode(&payload_original)
	if err != nil {
		return nil, err
	}

	payload, err := this.parse_payload(payload_struct, payload_original)
	if err != nil {
		return nil, err
	}

	return payload, nil
}

func (this *IPCFramework) check_data(package_type IPCPackageType, data []byte) error {
	payload_struct := this.GetPayloadStruct(package_type)
	if payload_struct != nil {
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

	payload, err = this.parse_data(payload_struct, data)
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
	// TODO handle value by struct types
	buffer := strings.NewReader("")
	decoder := json.NewDecoder(buffer)
	decoder.UseNumber()

	payload := make([]any, len(non_converted_payload))
	for index, value := range non_converted_payload {
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

	if _, ok := this.handlers[package_type]; !ok {
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

func (this *IPCFramework) GetPayloadStruct(package_type IPCPackageType) []reflect.Type {
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
