package typeinject

import (
	"reflect"
	"testing"
)

func TestMetadataTypeOps(t *testing.T) {
	base := FIELD_TYPE_CHECK | FIELD_TYPE_RECURSION

	if !MetadataHasType(base, FIELD_TYPE_CHECK) {
		t.Fatal("expected base to have CHECK")
	}
	if !MetadataHasType(base, FIELD_TYPE_RECURSION) {
		t.Fatal("expected base to have RECURSION")
	}
	if MetadataHasType(base, FIELD_TYPE_VALIDATION_STRUCT) {
		t.Fatal("did not expect VALIDATION_STRUCT")
	}

	added := MetadataAddType(base, FIELD_TYPE_VALIDATION_STRUCT)
	if !MetadataHasType(added, FIELD_TYPE_VALIDATION_STRUCT) {
		t.Fatal("add failed")
	}
	if MetadataHasType(added, FIELD_TYPE_CHECK) != MetadataHasType(base, FIELD_TYPE_CHECK) {
		t.Fatal("add should not touch existing bits")
	}

	removed := MetadataRemoveType(added, FIELD_TYPE_RECURSION)
	if MetadataHasType(removed, FIELD_TYPE_RECURSION) {
		t.Fatal("remove failed")
	}
	if !MetadataHasType(removed, FIELD_TYPE_CHECK) {
		t.Fatal("remove should not touch other bits")
	}
}

func TestParseInjectTag(t *testing.T) {
	meta := &DepInjectFieldMetadata{
		FieldType: FIELD_TYPE_CHECK,
	}
	if err := ParseInjectTag("ignore-check", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if MetadataHasType(meta.FieldType, FIELD_TYPE_CHECK) {
		t.Fatal("ignore-check should clear CHECK")
	}

	meta = &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK}
	if err := ParseInjectTag("ignore-check, ", meta); err != nil {
		t.Fatalf("unexpected error with sep: %v", err)
	}
	if MetadataHasType(meta.FieldType, FIELD_TYPE_CHECK) {
		t.Fatal("ignore-check with sep should clear CHECK")
	}

	meta = &DepInjectFieldMetadata{FieldType: FIELD_TYPE_CHECK}
	if err := ParseInjectTag("bogus", meta); err == nil {
		t.Fatal("expected error for unknown tag")
	}
}

type simpleStruct struct {
	Name string `validate:"required"`
	Age  int    `validate:"gte=0"`
}

type injectStruct struct {
	Name  string `inject:"ignore-check"`
	Age   int
	Email string `validate:"required,email"`
}

func TestParseStructArgvValidationMode(t *testing.T) {
	meta, err := ParseStructArgv(0, reflect.TypeOf(simpleStruct{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !MetadataHasType(meta.FieldType, FIELD_TYPE_VALIDATION_STRUCT) {
		t.Fatalf("expected VALIDATION_STRUCT, got %v", meta.FieldType)
	}
	if MetadataHasType(meta.FieldType, FIELD_TYPE_RECURSION) {
		t.Fatal("did not expect RECURSION in validation mode")
	}
}

func TestParseStructArgvRecursionMode(t *testing.T) {
	meta, err := ParseStructArgv(0, reflect.TypeOf(injectStruct{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !MetadataHasType(meta.FieldType, FIELD_TYPE_RECURSION) {
		t.Fatalf("expected RECURSION, got %v", meta.FieldType)
	}

	var ignoreMeta *DepInjectFieldMetadata
	var emailMeta *DepInjectFieldMetadata
	for _, child := range meta.Child {
		switch child.StructField.Name {
		case "Name":
			ignoreMeta = child
		case "Email":
			emailMeta = child
		}
	}

	if ignoreMeta == nil || MetadataHasType(ignoreMeta.FieldType, FIELD_TYPE_CHECK) {
		t.Fatal("Name field should have CHECK cleared via ignore-check")
	}
	if emailMeta == nil || !MetadataHasType(emailMeta.FieldType, FIELD_TYPE_VALIDATION_FIELD) {
		t.Fatal("Email field should carry VALIDATION_FIELD")
	}
}

func TestParseStaticArgv(t *testing.T) {
	typ := reflect.TypeFor[int]()
	meta := ParseStaticArgv(3, typ)
	if meta.Index != 3 {
		t.Fatalf("expected index 3, got %d", meta.Index)
	}
	if meta.Type != typ {
		t.Fatal("type mismatch")
	}
}

type sampleFunc func(int, string)

func TestParseFunctionArgv(t *testing.T) {
	typ := reflect.TypeOf(sampleFunc(nil))
	meta, err := ParseFunctionArgv(0, typ)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !MetadataHasType(meta.FieldType, FIELD_TYPE_RECURSION) {
		t.Fatalf("expected RECURSION for func, got %v", meta.FieldType)
	}
	if len(meta.Child) != 2 {
		t.Fatalf("expected 2 child args, got %d", len(meta.Child))
	}

	ptrTyp := reflect.TypeOf((*simpleStruct)(nil))
	ptrMeta, err := ParseFunctionArgv(0, ptrTyp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ptrMeta.IsPointer {
		t.Fatal("expected IsPointer for pointer arg")
	}
}

type nestedOuter struct {
	inner innerNested
	Name  string
}

type innerNested struct {
	Value int
}

func TestParseStructArgvUnexportedSkipped(t *testing.T) {
	type withUnexported struct {
		Exported   string `inject:"ignore-check"`
		unexported string
	}
	meta, err := ParseStructArgv(0, reflect.TypeOf(withUnexported{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta.Child) != 1 {
		t.Fatalf("expected only the exported field, got %d", len(meta.Child))
	}
	if meta.Child[0].StructField.Name != "Exported" {
		t.Fatalf("unexpected child %s", meta.Child[0].StructField.Name)
	}
}

func TestParseStructArgvNestedStruct(t *testing.T) {
	meta, err := ParseStructArgv(0, reflect.TypeOf(nestedOuter{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// nested struct field is present but no per-field recursion (unsupported)
	for _, c := range meta.Child {
		if c.StructField.Name == "inner" {
			if len(c.Child) != 0 {
				t.Fatal("nested struct child should not be recursively expanded")
			}
			if !MetadataHasType(c.FieldType, FIELD_TYPE_CHECK) {
				t.Fatal("nested struct field should be CHECK")
			}
		}
	}
}

func TestParseFunctionArgvStructPointer(t *testing.T) {
	typ := reflect.TypeOf((*simpleStruct)(nil))
	meta, err := ParseFunctionArgv(0, typ)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.IsPointer {
		t.Fatal("expected IsPointer for *struct arg")
	}
	if !MetadataHasType(meta.FieldType, FIELD_TYPE_VALIDATION_STRUCT) {
		t.Fatalf("expected VALIDATION_STRUCT for plain *struct, got %v", meta.FieldType)
	}
}

func TestGetStructByFunctionType(t *testing.T) {
	typ := reflect.TypeOf(sampleFunc(nil))
	metas, err := GetStructByFunctionType(typ)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 metas, got %d", len(metas))
	}

	if _, err := GetStructByFunctionType(reflect.TypeFor[int]()); err == nil {
		t.Fatal("expected error for non-func type")
	}

	metas, err = GetStructByFunctionType(typ, reflect.TypeFor[string]())
	if err != nil {
		t.Fatalf("unexpected error with static args: %v", err)
	}
	if len(metas) != 2 || metas[0].Type != reflect.TypeFor[string]() {
		t.Fatal("static args handling incorrect")
	}
}

func TestParseJsonPayload(t *testing.T) {
	fn := func(a int, b string) (int, string) { return a, b }
	out, err := ParseJsonPayload(fn, []byte(`[42, "hi"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].(int) != 42 || out[1].(string) != "hi" {
		t.Fatalf("unexpected %v", out)
	}

	// number type coercion: json.Number "7" -> int
	out, err = ParseJsonPayload(fn, []byte(`[7, "x"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].(int) != 7 {
		t.Fatalf("unexpected %v", out)
	}
}

func TestParseJsonPayloadWithMetas(t *testing.T) {
	typ := reflect.TypeOf(func(a int, b string) {})
	metas, err := GetStructByFunctionType(typ)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := ParseJsonPayloadWithMetas(metas, []byte(`[9, "yo"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].(int) != 9 || out[1].(string) != "yo" {
		t.Fatalf("unexpected %v", out)
	}

	// length mismatch is rejected
	if _, err := ParseJsonPayloadWithMetas(metas, []byte(`[9]`)); err == nil {
		t.Fatal("expected length mismatch error")
	}
}
