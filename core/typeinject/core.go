package typeinject

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
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

// ParseJsonPayload parses JSON array data into the function's parameter types
// (with type checking) and returns the converted []any.
// data holds only the payload part (i.e. non-static args).
//
// Numbers are parsed as json.Number and converted/validated to the target type;
// struct args are decoded into the concrete struct and validated via go-validator
// when in VALIDATION_STRUCT mode.
func ParseJsonPayload(function any, data []byte, static_types ...reflect.Type) ([]any, error) {
	fn_type := reflect.TypeOf(function)
	if fn_type == nil || fn_type.Kind() != reflect.Func {
		return nil, fmt.Errorf("`function` must be a function")
	}

	metas, err := GetStructByFunctionType(fn_type, static_types...)
	if err != nil {
		return nil, err
	}
	payload_metas := metas[len(static_types):]

	var raws []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raws); err != nil {
		return nil, fmt.Errorf("cannot decode json payload: %w", err)
	}
	if len(raws) != len(payload_metas) {
		return nil, fmt.Errorf("payload length mismatch: expected %d args, but got %d", len(payload_metas), len(raws))
	}

	result := make([]any, 0, len(payload_metas))
	for i, meta := range payload_metas {
		if meta == nil {
			result = append(result, nil)
			continue
		}

		value, err := decodeJsonArgv(meta, raws[i])
		if err != nil {
			return nil, fmt.Errorf("arg[%d] (%s) invalid: %w", i+len(static_types), meta.Type.Name(), err)
		}
		result = append(result, value)
	}

	return result, nil
}

// decodeJsonArgv decodes one JSON element into meta's type.
func decodeJsonArgv(meta *DepInjectFieldMetadata, raw json.RawMessage) (any, error) {
	if meta.Type.Kind() == reflect.Struct {
		ptr := reflect.New(meta.Type)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if err := decoder.Decode(ptr.Interface()); err != nil {
			return nil, err
		}

		value := ptr.Elem()
		if MetadataHasType(meta.FieldType, FIELD_TYPE_VALIDATION_STRUCT) {
			if err := default_validator.Struct(value.Interface()); err != nil {
				return nil, err
			}
		}

		if meta.IsPointer {
			return ptr.Interface(), nil
		}
		return value.Interface(), nil
	}

	// Basic types: decode with UseNumber, then convert/validate via ConvertArgv.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}

	return ConvertArgv(meta, value)
}

// ParseJsonPayloadWithMetas is like ParseJsonPayload but uses precomputed
// (payload-only) metadatas instead of deriving them from a function.
func ParseJsonPayloadWithMetas(metas DepInjectFieldMetadatas, data []byte) ([]any, error) {
	var raws []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raws); err != nil {
		return nil, fmt.Errorf("cannot decode json payload: %w", err)
	}
	if len(raws) != len(metas) {
		return nil, fmt.Errorf("payload length mismatch: expected %d args, but got %d", len(metas), len(raws))
	}

	result := make([]any, 0, len(metas))
	for i, meta := range metas {
		if meta == nil {
			result = append(result, nil)
			continue
		}

		value, err := decodeJsonArgv(meta, raws[i])
		if err != nil {
			return nil, fmt.Errorf("arg[%d] (%s) invalid: %w", i, meta.Type.Name(), err)
		}
		result = append(result, value)
	}

	return result, nil
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
