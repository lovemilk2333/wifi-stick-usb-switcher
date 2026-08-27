package typeinject

import (
	"fmt"
	"reflect"
	"strings"
)

type DepInjectFieldType uint16

const (
	FIELD_TYPE_CHECK DepInjectFieldType = 1 << iota
	FIELD_TYPE_VALIDATION_STRUCT
	FIELD_TYPE_RECURSION
	FIELD_TYPE_VALIDATION_FIELD
)

func MetadataAddType(field_type DepInjectFieldType, target_type DepInjectFieldType) DepInjectFieldType {
	return field_type | target_type
}

func MetadataRemoveType(field_type DepInjectFieldType, target_type DepInjectFieldType) DepInjectFieldType {
	return field_type & ^target_type
}

func MetadataHasType(field_type DepInjectFieldType, target_type DepInjectFieldType) bool {
	return field_type&target_type == target_type
}

type DepInjectFieldMetadatas = []*DepInjectFieldMetadata

type DepInjectFieldMetadata struct {
	Index       int
	FieldType   DepInjectFieldType
	IsPointer   bool
	Type        reflect.Type
	StructField reflect.StructField
	Child       DepInjectFieldMetadatas
}

const VALIDATION_TAG = "validate"
const INJECT_TAG = "inject"
const TAG_SEP = ","

type InjectTagValue = string

const (
	INJECT_TAG_IGNORE_CHECK InjectTagValue = "ignore-check"
)

func ParseInjectTag(tag string, metadata *DepInjectFieldMetadata) error {
	var tag_parts []string

	if !strings.Contains(tag, TAG_SEP) {
		tag_parts = []string{
			tag,
		}
	} else {
		tag_parts = strings.Split(tag, TAG_SEP)
		for index, tag_part := range tag_parts {
			tag_parts[index] = strings.TrimSpace(tag_part)
		}
	}

	for _, tag_part := range tag_parts {
		if tag_part == "" {
			continue
		}
		switch tag_part {
		case INJECT_TAG_IGNORE_CHECK:
			metadata.FieldType = MetadataRemoveType(metadata.FieldType, FIELD_TYPE_CHECK)
		default:
			return fmt.Errorf("invalid inject tag `%s`", tag_part)
		}
	}

	return nil
}

func ParseStructArgv(index int, argv_type reflect.Type) (*DepInjectFieldMetadata, error) {
	argv_count := argv_type.NumField()
	metadata := &DepInjectFieldMetadata{
		Index: index,
		Type:  argv_type,
	}
	field_metadatas := make([]*DepInjectFieldMetadata, 0, argv_count)

	has_inject_tag := false
	for i := 0; i < argv_count; i++ {
		field := argv_type.Field(i)
		if !field.IsExported() {
			continue
		}

		field_type := field.Type
		field_metadata := &DepInjectFieldMetadata{
			Index:       i,
			FieldType:   FIELD_TYPE_CHECK,
			Type:        field_type,
			StructField: field,
		}
		if field_type.Kind() == reflect.Pointer {
			field_metadata.IsPointer = true
			field_metadata.Type = field_type.Elem()
		}

		// check if is `validation` struct
		validate_tag := strings.TrimSpace(field.Tag.Get(VALIDATION_TAG))
		if validate_tag != "" && validate_tag != "-" {
			field_metadata.FieldType = MetadataAddType(field_metadata.FieldType, FIELD_TYPE_VALIDATION_FIELD)
		}

		inject_tag := strings.TrimSpace(field.Tag.Get(INJECT_TAG))
		if inject_tag != "" && inject_tag != "-" {
			if err := ParseInjectTag(inject_tag, field_metadata); err != nil {
				return nil, err
			}
			has_inject_tag = true
		}

		field_metadatas = append(field_metadatas, field_metadata)
	}

	if has_inject_tag { // if has inject tag, use recursion mode
		metadata.FieldType = FIELD_TYPE_RECURSION
		metadata.Child = field_metadatas
	} else { // or, use validation mode to call `go-validation` like package
		metadata.FieldType = FIELD_TYPE_VALIDATION_STRUCT
	}

	return metadata, nil
}

func ParseStaticArgv(index int, argv_type reflect.Type) *DepInjectFieldMetadata {
	return &DepInjectFieldMetadata{
		Index: index,
		Type:  argv_type,
	}
}

func ParseFunctionArgv(index int, argv_type reflect.Type) (*DepInjectFieldMetadata, error) {
	metadata := &DepInjectFieldMetadata{
		Index: index,
		Type:  argv_type,
	}

	if argv_type.Kind() == reflect.Pointer {
		argv_type = argv_type.Elem()
		metadata.Type = argv_type
		metadata.IsPointer = true
	}

	switch argv_type.Kind() {
	case reflect.Func:
		child_struct, err := GetStructByFunctionType(argv_type)
		if err != nil {
			return nil, err
		}

		metadata.FieldType = FIELD_TYPE_RECURSION
		metadata.Child = child_struct
	case reflect.Struct:
		struct_metadata, err := ParseStructArgv(index, argv_type)
		if err != nil {
			return nil, err
		}
		struct_metadata.IsPointer = metadata.IsPointer
		metadata = struct_metadata
	default: // basic types
		metadata.FieldType = FIELD_TYPE_CHECK
	}

	return metadata, nil
}

/*
NOTE: we currently not support "struct in struct" type inject
*/
func GetStructByFunctionType(function_type reflect.Type, static_args ...reflect.Type) (DepInjectFieldMetadatas, error) {
	if function_type.Kind() != reflect.Func {
		return nil, fmt.Errorf("`function` is not a function")
	}

	arg_count := function_type.NumIn()
	static_args_count := len(static_args)
	if arg_count < static_args_count {
		return nil, fmt.Errorf("`function` must have more than (equals) %d args", static_args_count)
	}

	fields_metadatas := make([]*DepInjectFieldMetadata, 0, arg_count)
	for index := 0; index < arg_count; index++ {
		if index < static_args_count {
			fields_metadatas = append(fields_metadatas, ParseStaticArgv(index, static_args[index]))
		} else {
			argv_struct, err := ParseFunctionArgv(index, function_type.In(index))
			if err != nil {
				return nil, err
			}
			fields_metadatas = append(fields_metadatas, argv_struct)
		}
	}

	return fields_metadatas, nil
}
