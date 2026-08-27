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
	if argv_value.Kind() == reflect.Pointer {
		if argv_value.IsNil() {
			return nil, fmt.Errorf("invalid value type: nil is not `%s`", meta.Type.Name())
		}
		if !meta.IsPointer {
			return nil, fmt.Errorf("invalid value type: got pointer of `%s` but want `%s`", argv_value.Elem().Type().Name(), meta.Type.Name())
		}
		argv_value = argv_value.Elem()
	}

	var result any

	switch {
	// 模式 1：FIELD_TYPE_VALIDATION_STRUCT，整个结构体直接丢给 go-validator 校验
	case MetadataHasType(meta.FieldType, FIELD_TYPE_VALIDATION_STRUCT):
		if !argv_value.CanConvert(meta.Type) {
			return nil, fmt.Errorf("cannot convert `%s` to `%s`", argv_value.Type().Name(), meta.Type.Name())
		}

		converted_value := argv_value.Convert(meta.Type)
		if err := default_validator.Struct(converted_value); err != nil {
			return nil, err
		}

		result = converted_value.Interface()
	// 模式 2：FIELD_TYPE_RECURSION，逐个子字段处理
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

			// 单字段具有 validation tag 时的逻辑
			if MetadataHasType(child_meta.FieldType, FIELD_TYPE_VALIDATION_FIELD) {
				validate_tag := strings.TrimSpace(child_meta.StructField.Tag.Get(VALIDATION_TAG))
				if validate_tag != "" && validate_tag != "-" {
					if err := default_validator.Var(field_value.Interface(), validate_tag); err != nil {
						return nil, fmt.Errorf("field `%s` invalid: %w", child_meta.StructField.Name, err)
					}
				}
			}

			// 深度递归处理子元数据（如果存在）
			if len(child_meta.Child) > 0 {
				value, err := ConvertArgv(child_meta, field_value.Interface())
				if err != nil {
					return nil, err
				}

				args = append(args, value)
			}
		}

		result = args
	case MetadataHasType(meta.FieldType, FIELD_TYPE_CHECK):

		if argv, ok := argv.(json.Number); ok {
			r, err := ConvertJsonNumber(argv, meta.Type)
			if err != nil {
				return nil, err
			}
			result = r.Interface()
		} else {
			if !argv_value.CanConvert(meta.Type) {
				return nil, fmt.Errorf("invalid value type: expect `Struct`, got `%s`", argv_value.Type().Name())
			}

			result = argv_value.Convert(meta.Type).Interface()
		}
	default:
		return nil, fmt.Errorf("unknown `FieldType` of field metadata: %v", meta.FieldType)
	}

	if meta.IsPointer {
		return &result, nil
	} else {
		return result, nil
	}
}
