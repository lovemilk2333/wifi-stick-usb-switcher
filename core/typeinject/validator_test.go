package typeinject

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestConvertJsonNumber(t *testing.T) {
	cases := []struct {
		name   string
		number json.Number
		target reflect.Type
		ok     bool
	}{
		{"int", json.Number("42"), reflect.TypeFor[int](), true},
		{"int8 overflow", json.Number("1000"), reflect.TypeFor[int8](), false},
		{"negative uint", json.Number("-1"), reflect.TypeFor[uint](), false},
		{"uint ok", json.Number("7"), reflect.TypeFor[uint](), true},
		{"float", json.Number("3.14"), reflect.TypeFor[float64](), true},
		{"non-number", json.Number("abc"), reflect.TypeFor[int](), false},
		{"float to int", json.Number("3.5"), reflect.TypeFor[int](), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := ConvertJsonNumber(c.number, c.target)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got err: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error, got value %v", v)
			}
			if c.ok && v.Type() != c.target {
				t.Fatalf("expected type %v, got %v", c.target, v.Type())
			}
		})
	}
}

func TestConvertArgvCheck(t *testing.T) {
	meta := &DepInjectFieldMetadata{
		FieldType: FIELD_TYPE_CHECK,
		Type:      reflect.TypeFor[int](),
	}
	v, err := ConvertArgv(meta, json.Number("10"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iv, ok := v.(int); !ok || iv != 10 {
		t.Fatalf("expected int 10, got %v (%T)", v, v)
	}

	metaStr := &DepInjectFieldMetadata{
		FieldType: FIELD_TYPE_CHECK,
		Type:      reflect.TypeFor[string](),
	}
	s, err := ConvertArgv(metaStr, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "hello" {
		t.Fatalf("expected hello, got %v", s)
	}

	metaPtr := &DepInjectFieldMetadata{
		FieldType: FIELD_TYPE_CHECK,
		Type:      reflect.TypeFor[int](),
		IsPointer: true,
	}
	pv, err := ConvertArgv(metaPtr, json.Number("5"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ptr, ok := pv.(*int)
	if !ok || *ptr != 5 {
		t.Fatalf("expected *int 5, got %v (%T)", pv, pv)
	}

	_, err = ConvertArgv(meta, "not-a-number")
	if err == nil {
		t.Fatal("expected error converting non-number string to int")
	}
}

func TestConvertArgvValidationStruct(t *testing.T) {
	type req struct {
		Name string `validate:"required"`
	}
	meta := &DepInjectFieldMetadata{
		FieldType: FIELD_TYPE_VALIDATION_STRUCT,
		Type:      reflect.TypeOf(req{}),
	}

	if _, err := ConvertArgv(meta, req{Name: ""}); err == nil {
		t.Fatal("expected validation error for empty Name")
	}

	v, err := ConvertArgv(meta, req{Name: "ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rv, ok := v.(req); !ok || rv.Name != "ok" {
		t.Fatalf("unexpected result %v (%T)", v, v)
	}
}

func TestConvertArgvRecursion(t *testing.T) {
	type inner struct {
		Name  string `inject:"ignore-check"`
		Email string `validate:"required,email"`
		Age   int
	}
	meta, err := ParseStructArgv(0, reflect.TypeOf(inner{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !MetadataHasType(meta.FieldType, FIELD_TYPE_RECURSION) {
		t.Fatalf("expected recursion mode for inject-tagged struct, got %v", meta.FieldType)
	}

	bad := inner{Email: "not-email", Age: 3}
	if _, err := ConvertArgv(meta, bad); err == nil {
		t.Fatal("expected validation error for bad email")
	}

	good := inner{Email: "a@b.com", Age: 3}
	v, err := ConvertArgv(meta, good)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args, ok := v.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", v)
	}
	// Name is ignore-check (skipped), Email + Age are CHECK => 2 values
	if len(args) != 2 {
		t.Fatalf("expected 2 child values, got %d", len(args))
	}
}

func TestConvertArgs(t *testing.T) {
	metaInt := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK, Type: reflect.TypeFor[int]()}
	metaStr := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK, Type: reflect.TypeFor[string]()}

	out, err := ConvertArgs(DepInjectFieldMetadatas{metaInt, metaStr}, []any{json.Number("1"), "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0].(int) != 1 || out[1].(string) != "x" {
		t.Fatalf("unexpected output %v", out)
	}

	if _, err := ConvertArgs(DepInjectFieldMetadatas{metaInt}, []any{"a"}); err == nil {
		t.Fatal("expected length mismatch error")
	}

	nilOut, err := ConvertArgs(nil, nil)
	if err != nil || nilOut != nil {
		t.Fatalf("expected nil, nil for empty, got %v %v", nilOut, err)
	}

	// nil arg entries are skipped
	skip := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK, Type: reflect.TypeFor[int]()}
	out, err = ConvertArgs(DepInjectFieldMetadatas{skip}, []any{nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty result when arg is nil, got %v", out)
	}
}

func TestArgs2values(t *testing.T) {
	vals := Args2values([]any{1, "a"})
	if len(vals) != 2 || vals[0].Int() != 1 || vals[1].String() != "a" {
		t.Fatalf("unexpected values %v", vals)
	}
}

func TestConvertJsonNumberOverflow(t *testing.T) {
	// valid int that overflows int8
	if _, err := ConvertJsonNumber(json.Number("200"), reflect.TypeFor[int8]()); err == nil {
		t.Fatal("expected overflow error for int8")
	}
	// valid int8 boundary
	if v, err := ConvertJsonNumber(json.Number("127"), reflect.TypeFor[int8]()); err != nil || v.Int() != 127 {
		t.Fatalf("expected 127, got %v err=%v", v, err)
	}
	// float32 overflow
	if _, err := ConvertJsonNumber(json.Number("1e40"), reflect.TypeFor[float32]()); err == nil {
		t.Fatal("expected overflow error for float32")
	}
	// NaN
	if _, err := ConvertJsonNumber(json.Number("NaN"), reflect.TypeFor[float64]()); err == nil {
		t.Fatal("expected error for NaN")
	}
}

func TestConvertArgvCheckErrors(t *testing.T) {
	intMeta := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK, Type: reflect.TypeFor[int]()}
	uintMeta := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK, Type: reflect.TypeFor[uint]()}

	// negative to uint
	if _, err := ConvertArgv(uintMeta, json.Number("-1")); err == nil {
		t.Fatal("expected error: negative to uint")
	}
	// non-numeric string to int
	if _, err := ConvertArgv(intMeta, "abc"); err == nil {
		t.Fatal("expected error: string to int")
	}
	// string to int
	if _, err := ConvertArgv(intMeta, "42"); err == nil {
		t.Fatal("expected error: non-json.Number string to int")
	}
}

func TestConvertArgvNil(t *testing.T) {
	meta := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK, Type: reflect.TypeFor[int]()}
	// a nil arg itself is rejected by ConvertArgv (ConvertArgs skips nil entries)
	if _, err := ConvertArgv(meta, nil); err == nil {
		t.Fatal("expected error for nil arg in ConvertArgv")
	}
}

func TestConvertArgvUnknownFieldType(t *testing.T) {
	// a meta carrying only VALIDATION_FIELD (no CHECK/VALIDATION_STRUCT/RECURSION)
	// is skipped by the guard and returns nil without error.
	meta := &DepInjectFieldMetadata{FieldType: FIELD_TYPE_VALIDATION_FIELD, Type: reflect.TypeFor[int]()}
	v, err := ConvertArgv(meta, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Fatalf("expected nil result for unsupported FieldType, got %v", v)
	}
}
