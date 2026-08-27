package typeinject

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

var default_validator = validator.New()

func ConvertJsonNumber(value json.Number, target reflect.Type) (reflect.Value, error) {
	kind := target.Kind()
	value_int, int_err := value.Int64()
	valid_int := int_err == nil

	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if !valid_int {
			return reflect.Value{}, fmt.Errorf("value is not integer")
		}
		if target.OverflowInt(value_int) {
			return reflect.Value{}, fmt.Errorf("value overflowed for %s", target.Name())
		}

		return reflect.ValueOf(value_int).Convert(target), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !valid_int {
			return reflect.Value{}, fmt.Errorf("value is not a integer")
		}

		if value_int < 0 {
			return reflect.Value{}, fmt.Errorf("negative value for %s", target.Name())
		}

		if target.OverflowUint(uint64(value_int)) {
			return reflect.Value{}, fmt.Errorf("value overflowed for %s", target.Name())
		}

		return reflect.ValueOf(value_int).Convert(target), nil
	case reflect.Float32, reflect.Float64:
		value_float, _ := value.Float64()
		if math.IsNaN(value_float) || math.IsInf(value_float, 0) {
			return reflect.Value{}, fmt.Errorf("value is inf or nan")
		}

		if target.OverflowFloat(value_float) {
			return reflect.Value{}, fmt.Errorf("value overflowed for %s", target.Name())
		}

		return reflect.ValueOf(value_float).Convert(target), nil
	default:
		return reflect.Value{}, fmt.Errorf("target type is not a number type (got %s)", target.Name())
	}
}

func ConvertArgs(types DepInjectFieldMetadatas, args []any) ([]any, error) {
	types_length := len(types)

	if types_length != len(args) {
		return nil, fmt.Errorf("argv length mismatch")
	}

	if types == nil || types_length == 0 {
		return nil, nil
	}

	result := make([]any, 0, types_length)
	for i, argv := range args {
		meta := types[i]
		if meta == nil || argv == nil {
			continue
		}

		value, err := ConvertArgv(meta, argv)
		if err != nil {
			return nil, fmt.Errorf("arg[%d] validation failed: %w", i, err)
		}

		result = append(result, value)
	}

	return result, nil
}

func Args2values(args []any) []reflect.Value {
	result := make([]reflect.Value, 0, len(args))
	for _, argv := range args {
		result = append(result, reflect.ValueOf(argv))
	}
	return result
}

func ConvertArgv(meta *DepInjectFieldMetadata, argv any) (any, error) {
	if !MetadataHasType(meta.FieldType, FIELD_TYPE_CHECK) &&
		!MetadataHasType(meta.FieldType, FIELD_TYPE_VALIDATION_STRUCT) &&
		!MetadataHasType(meta.FieldType, FIELD_TYPE_RECURSION) {
		return nil, nil
	}

	argv_value := reflect.ValueOf(argv)
	if !argv_value.IsValid() {
		return nil, fmt.Errorf("invalid value type: nil is not `%s`", meta.Type.Name())
	}
	if argv_value.Kind() == reflect.Pointer {
		if argv_value.IsNil() {
			return nil, fmt.Errorf("invalid value type: nil is not `%s`", meta.Type.Name())
		}
		if !meta.IsPointer {
			return nil, fmt.Errorf("invalid value type: got pointer of `%s` but want `%s`", argv_value.Elem().Type().Name(), meta.Type.Name())
		}
		argv_value = argv_value.Elem()
	}

	// function-typed dependencies are injected as-is (the callable itself)
	if meta.Type.Kind() == reflect.Func {
		if meta.IsPointer && argv_value.Kind() != reflect.Pointer {
			ptr := reflect.New(meta.Type)
			ptr.Elem().Set(argv_value)
			return ptr.Interface(), nil
		}
		return argv, nil
	}

	var result any
	var converted reflect.Value

	switch {
	// Mode 1: VALIDATION_STRUCT, validate the whole struct via go-validator
	case MetadataHasType(meta.FieldType, FIELD_TYPE_VALIDATION_STRUCT):
		if !argv_value.CanConvert(meta.Type) {
			return nil, fmt.Errorf("cannot convert `%s` to `%s`", argv_value.Type().Name(), meta.Type.Name())
		}

		converted_value := argv_value.Convert(meta.Type)
		if err := default_validator.Struct(converted_value.Interface()); err != nil {
			return nil, err
		}

		converted = converted_value
		result = converted_value.Interface()
	// Mode 2: RECURSION, process child fields one by one
	case MetadataHasType(meta.FieldType, FIELD_TYPE_RECURSION):
		if argv_value.Kind() != reflect.Struct {
			return nil, fmt.Errorf("invalid value type: expect `Struct`, got `%s`", argv_value.Type().Name())
		}

		args := make([]any, 0, len(meta.Child))
		for _, child_meta := range meta.Child {
			if child_meta == nil || !MetadataHasType(child_meta.FieldType, FIELD_TYPE_CHECK) {
				continue
			}

			field_value := argv_value.Field(child_meta.Index)
			if !field_value.IsValid() || !field_value.CanInterface() {
				continue
			}

			// Per-field validation tag handling
			if MetadataHasType(child_meta.FieldType, FIELD_TYPE_VALIDATION_FIELD) {
				validate_tag := strings.TrimSpace(child_meta.StructField.Tag.Get(VALIDATION_TAG))
				if validate_tag != "" && validate_tag != "-" {
					if err := default_validator.Var(field_value.Interface(), validate_tag); err != nil {
						return nil, fmt.Errorf("field `%s` invalid: %w", child_meta.StructField.Name, err)
					}
				}
			}

			// Deep-recurse child metadata if present, else convert the field
			value, err := ConvertArgv(child_meta, field_value.Interface())
			if err != nil {
				return nil, err
			}

			args = append(args, value)
		}

		result = args
	case MetadataHasType(meta.FieldType, FIELD_TYPE_CHECK):
		// `any` parameter: accept any concrete value but reject pointer/struct
		if meta.Type.Kind() == reflect.Interface {
			if argv_value.Kind() == reflect.Pointer || argv_value.Kind() == reflect.Struct {
				return nil, fmt.Errorf("invalid value type: pointer/struct not allowed for `any` param, got `%s`", argv_value.Type().Name())
			}
			return argv, nil
		}

		if argv, ok := argv.(json.Number); ok {
			r, err := ConvertJsonNumber(argv, meta.Type)
			if err != nil {
				return nil, err
			}
			converted = r
			result = r.Interface()
		} else {
			if !argv_value.CanConvert(meta.Type) {
				return nil, fmt.Errorf("invalid value type: expect `Struct`, got `%s`", argv_value.Type().Name())
			}

			converted = argv_value.Convert(meta.Type)
			result = converted.Interface()
		}
	default:
		return nil, fmt.Errorf("unknown `FieldType` of field metadata: %v", meta.FieldType)
	}

	if meta.IsPointer {
		if converted.IsValid() {
			ptr := reflect.New(meta.Type)
			ptr.Elem().Set(converted)
			return ptr.Interface(), nil
		}
		return &result, nil
	}

	if converted.IsValid() {
		return converted.Interface(), nil
	}
	return result, nil
}
