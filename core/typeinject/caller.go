package typeinject

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Indirect-call helpers: infer arg types from the function signature, convert
// and type-check inputs (or a JSON payload), then call via reflection.

// CallFunction converts args to the function's parameter types (with type
// checking) and invokes it, returning its results.
//
// The first N parameters may be supplied directly via static (e.g. *IPCFramework)
// and are prepended to the call as-is, skipping conversion.
func CallFunction(function any, args []any, static ...any) ([]reflect.Value, error) {
	fn_type := reflect.TypeOf(function)
	if fn_type == nil || fn_type.Kind() != reflect.Func {
		return nil, fmt.Errorf("`function` must be a function")
	}

	static_types := make([]reflect.Type, len(static))
	static_values := make([]reflect.Value, len(static))
	for i, s := range static {
		if s == nil {
			return nil, fmt.Errorf("static arg[%d] cannot be nil", i)
		}
		static_types[i] = reflect.TypeOf(s)
		static_values[i] = reflect.ValueOf(s)
	}

	metas, err := GetStructByFunctionType(fn_type, static_types...)
	if err != nil {
		return nil, err
	}

	payload_metas := metas[len(static_types):]
	converted, err := ConvertArgs(payload_metas, args)
	if err != nil {
		return nil, fmt.Errorf("cannot convert args: %w", err)
	}

	call_args := make([]reflect.Value, 0, len(static_values)+len(converted))
	call_args = append(call_args, static_values...)
	call_args = append(call_args, Args2values(converted)...)

	return reflect.ValueOf(function).Call(call_args), nil
}

// CallFunctionJSON parses JSON array data into the function's parameter types
// (with type checking) and invokes it. data holds only the payload part;
// static supplies leading args to prepend as-is (e.g. *IPCFramework).
func CallFunctionJSON(function any, data []byte, static ...any) ([]reflect.Value, error) {
	fn_type := reflect.TypeOf(function)
	if fn_type == nil || fn_type.Kind() != reflect.Func {
		return nil, fmt.Errorf("`function` must be a function")
	}

	static_types := make([]reflect.Type, len(static))
	static_values := make([]reflect.Value, len(static))
	for i, s := range static {
		if s == nil {
			return nil, fmt.Errorf("static arg[%d] cannot be nil", i)
		}
		static_types[i] = reflect.TypeOf(s)
		static_values[i] = reflect.ValueOf(s)
	}

	converted, err := ParseJsonPayload(function, data, static_types...)
	if err != nil {
		return nil, err
	}

	call_args := make([]reflect.Value, 0, len(static_values)+len(converted))
	call_args = append(call_args, static_values...)
	call_args = append(call_args, Args2values(converted)...)

	return reflect.ValueOf(function).Call(call_args), nil
}

// ProcessAnyPayload validates a single value whose static type is `any`. It matches
// any concrete value but rejects pointers and structs. This mirrors daemonipc's
// per-arg payload-type check, generalized to `any`.
func ProcessAnyPayload(value any) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("payload value cannot be nil")
	}

	kind := reflect.TypeOf(value).Kind()
	if kind == reflect.Pointer || kind == reflect.Struct {
		return nil, fmt.Errorf("payload value of kind %s is not allowed (pointer/struct rejected)", kind)
	}

	return value, nil
}

// ConvertAnyPayload validates each element of payload (static type `any`) using
// ProcessAnyPayload, accumulating errors like daemonipc's parse_payload did.
func ConvertAnyPayload(payload []any) ([]any, error) {
	result := make([]any, 0, len(payload))
	var errs []string
	for i, value := range payload {
		v, err := ProcessAnyPayload(value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("arg[%d]: %v", i, err))
			continue
		}
		result = append(result, v)
	}

	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return result, nil
}
