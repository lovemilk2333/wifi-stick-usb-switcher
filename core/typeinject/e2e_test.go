package typeinject

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

// invoke parses the function type, converts the given args against it, and
// actually calls the function, returning its results.
func invoke(t *testing.T, fn any, args []any) ([]reflect.Value, error) {
	t.Helper()

	fn_type := reflect.TypeOf(fn)
	metas, err := GetStructByFunctionType(fn_type)
	if err != nil {
		return nil, err
	}

	converted, err := ConvertArgs(metas, args)
	if err != nil {
		return nil, err
	}

	values := Args2values(converted)
	return reflect.ValueOf(fn).Call(values), nil
}

func inject_test_a(a string, b string, c int, d float32, f float64) []any {
	return []any{a, b, c, d, f}
}

func TestDepInjectBasicFunction(t *testing.T) {
	args := []any{"a", "b", 123, 4.5, 6}
	res, err := invoke(t, inject_test_a, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	out := res[0].Interface().([]any)
	if out[0].(string) != "a" || out[1].(string) != "b" || out[2].(int) != 123 {
		t.Fatalf("unexpected args passed to fn: %v", out)
	}
	if out[3].(float32) != 4.5 {
		t.Fatalf("expected float32 4.5, got %v (%T)", out[3], out[3])
	}
	if out[4].(float64) != 6 {
		t.Fatalf("expected float64 6, got %v (%T)", out[4], out[4])
	}
}

func TestDepInjectJsonNumberArgs(t *testing.T) {
	// simulate args decoded from JSON: all numbers are json.Number
	args := []any{
		"a",
		"b",
		json.Number("123"),
		json.Number("4.5"),
		json.Number("6"),
	}
	res, err := invoke(t, inject_test_a, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res[0].Interface().([]any)
	if out[2].(int) != 123 || out[3].(float32) != 4.5 || out[4].(float64) != 6 {
		t.Fatalf("json.Number conversion incorrect: %v", out)
	}
}

func TestDepInjectTypeMismatch(t *testing.T) {
	// c expects int but we send a non-numeric string
	args := []any{"a", "b", "notanint", 4.5, 6}
	if _, err := invoke(t, inject_test_a, args); err == nil {
		t.Fatal("expected conversion error for non-int arg")
	}

	// wrong arg count
	if _, err := invoke(t, inject_test_a, []any{"a", "b", 1, 2.0}); err == nil {
		t.Fatal("expected length mismatch error")
	}
}

func TestDepInjectPointerArgs(t *testing.T) {
	fn := func(a *int, b *string) []any {
		return []any{*a, *b}
	}
	args := []any{json.Number("9"), "hi"}
	res, err := invoke(t, fn, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res[0].Interface().([]any)
	if out[0].(int) != 9 || out[1].(string) != "hi" {
		t.Fatalf("pointer arg injection incorrect: %v", out)
	}
}

type injectStructArg struct {
	Name  string `validate:"required"`
	Email string `validate:"required,email"`
}

func TestDepInjectStructArgValidation(t *testing.T) {
	fn := func(s injectStructArg) string {
		return s.Email
	}

	// valid struct arg
	good := injectStructArg{Name: "x", Email: "a@b.com"}
	res, err := invoke(t, fn, []any{good})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "a@b.com" {
		t.Fatalf("unexpected result %v", res[0].Interface())
	}

	// invalid: bad email should fail validation
	bad := injectStructArg{Name: "x", Email: "not-email"}
	if _, err := invoke(t, fn, []any{bad}); err == nil {
		t.Fatal("expected validation error for bad email")
	}

	// invalid: empty required field
	if _, err := invoke(t, fn, []any{injectStructArg{}}); err == nil {
		t.Fatal("expected validation error for empty required fields")
	}
}

func TestDepInjectFunctionArg(t *testing.T) {
	// a function argument: its parameters become injectable child args
	cb := func(x int, y int) int { return x + y }
	fn := func(callback func(int, int) int, factor int) int {
		return callback(2, 3) * factor
	}

	// the callback arg is provided as a *reflect.Value / function value
	args := []any{cb, 10}
	metas, err := GetStructByFunctionType(reflect.TypeOf(fn))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	converted, err := ConvertArgs(metas, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	values := Args2values(converted)
	res := reflect.ValueOf(fn).Call(values)
	if res[0].Interface().(int) != 50 {
		t.Fatalf("expected 50, got %v", res[0].Interface())
	}
}

func TestDepInjectMultiReturn(t *testing.T) {
	fn := func(a int, b int) (int, int) {
		return a + b, a * b
	}
	args := []any{json.Number("3"), json.Number("4")}
	res, err := invoke(t, fn, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum, prod := res[0].Interface().(int), res[1].Interface().(int)
	if sum != 7 || prod != 12 {
		t.Fatalf("expected sum=7 prod=12, got %d %d", sum, prod)
	}
}

func TestDepInjectPointerStructArg(t *testing.T) {
	fn := func(s *injectStructArg) string {
		return s.Email
	}

	good := &injectStructArg{Name: "x", Email: "a@b.com"}
	res, err := invoke(t, fn, []any{good})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "a@b.com" {
		t.Fatalf("unexpected result %v", res[0].Interface())
	}

	bad := &injectStructArg{Name: "x", Email: "bad"}
	if _, err := invoke(t, fn, []any{bad}); err == nil {
		t.Fatal("expected validation error for bad email on pointer struct")
	}
}

func TestDepInjectSliceAndMapArg(t *testing.T) {
	fn := func(data []int, meta map[string]int) int {
		return len(data) + len(meta)
	}
	args := []any{[]int{1, 2, 3}, map[string]int{"a": 1, "b": 2}}
	res, err := invoke(t, fn, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(int) != 5 {
		t.Fatalf("expected 5, got %v", res[0].Interface())
	}
}

func TestDepInjectNumberOverflow(t *testing.T) {
	fn := func(u uint8, f float32, i int64) (uint8, float32, int64) {
		return u, f, i
	}

	// valid values
	if _, err := invoke(t, fn, []any{json.Number("200"), json.Number("1.5"), json.Number("9000000000")}); err != nil {
		t.Fatalf("unexpected error for valid numbers: %v", err)
	}

	// uint8 overflow
	if _, err := invoke(t, fn, []any{json.Number("300"), json.Number("1.5"), json.Number("1")}); err == nil {
		t.Fatal("expected overflow error for uint8 > 255")
	}

	// negative to uint
	if _, err := invoke(t, fn, []any{json.Number("-1"), json.Number("1.5"), json.Number("1")}); err == nil {
		t.Fatal("expected error for negative uint")
	}

	// float32 overflow (1e40 does not fit float32)
	if _, err := invoke(t, fn, []any{json.Number("1"), json.Number("1e40"), json.Number("1")}); err == nil {
		t.Fatal("expected overflow error for float32")
	}
}

type injectModeStruct struct {
	Name string `inject:"ignore-check"`
	Age  int    `validate:"gte=0"`
}

func TestDepInjectStructRecursionMode(t *testing.T) {
	// a struct arg carrying an inject tag switches to recursion mode;
	// ConvertArgv returns the list of individual (CHECK) child values.
	metas, err := GetStructByFunctionType(reflect.TypeOf(func(s injectModeStruct) int { return s.Age }))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 meta, got %d", len(metas))
	}
	if !MetadataHasType(metas[0].FieldType, FIELD_TYPE_RECURSION) {
		t.Fatalf("expected recursion mode, got %v", metas[0].FieldType)
	}

	converted, err := ConvertArgs(metas, []any{injectModeStruct{Name: "x", Age: 21}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	children, ok := converted[0].([]any)
	if !ok {
		t.Fatalf("expected []any child values, got %T", converted[0])
	}
	// Name is ignore-check (skipped), only Age is emitted
	if len(children) != 1 || children[0].(int) != 21 {
		t.Fatalf("expected [21], got %v", children)
	}
}

func TestDepInjectZeroAndNilArgs(t *testing.T) {
	fn := func(a int, b string) (int, string) {
		return a, b
	}

	// zero values are valid
	if _, err := invoke(t, fn, []any{0, ""}); err != nil {
		t.Fatalf("unexpected error for zero values: %v", err)
	}

	// arg count mismatch
	if _, err := invoke(t, fn, []any{1}); err == nil {
		t.Fatal("expected error for too few args")
	}
	if _, err := invoke(t, fn, []any{1, "a", 3}); err == nil {
		t.Fatal("expected error for too many args")
	}
}

type validationConfig struct {
	Name     string `validate:"required"`
	Email    string `validate:"required,email"`
	Port     int    `validate:"gte=1,lte=65535"`
	Protocol string `validate:"oneof=tcp udp"`
	Alias    string `validate:"-"`
}

func TestDepInjectValidationStructMixedFields(t *testing.T) {
	fn := func(c validationConfig) string {
		return c.Name + "@" + c.Email
	}

	valid := validationConfig{
		Name:     "svc",
		Email:    "a@b.com",
		Port:     8080,
		Protocol: "tcp",
		Alias:    "", // "-" means skipped, empty is allowed
	}
	res, err := invoke(t, fn, []any{valid})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "svc@a@b.com" {
		t.Fatalf("unexpected result %v", res[0].Interface())
	}

	cases := map[string]validationConfig{
		"missing name":    {Email: "a@b.com", Port: 80, Protocol: "tcp"},
		"bad email":       {Name: "x", Email: "nope", Port: 80, Protocol: "tcp"},
		"port too low":    {Name: "x", Email: "a@b.com", Port: 0, Protocol: "tcp"},
		"port too high":   {Name: "x", Email: "a@b.com", Port: 70000, Protocol: "tcp"},
		"invalid protocol": {Name: "x", Email: "a@b.com", Port: 80, Protocol: "icmp"},
	}
	for name, cfg := range cases {
		if _, err := invoke(t, fn, []any{cfg}); err == nil {
			t.Fatalf("expected validation error for: %s", name)
		}
	}
}

type nestedAddr struct {
	Host string `validate:"required"`
	Port int    `validate:"gte=0"`
}

type nestedService struct {
	Name string     `validate:"required"`
	Addr nestedAddr `validate:"required"`
}

func TestDepInjectNestedStructValidation(t *testing.T) {
	fn := func(s nestedService) string {
		return s.Name + ":" + s.Addr.Host
	}

	good := nestedService{Name: "svc", Addr: nestedAddr{Host: "localhost", Port: 80}}
	if _, err := invoke(t, fn, []any{good}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// invalid nested field should be caught by the validator's dive
	bad := nestedService{Name: "svc", Addr: nestedAddr{Host: "", Port: 80}}
	if _, err := invoke(t, fn, []any{bad}); err == nil {
		t.Fatal("expected validation error for empty nested Host")
	}

	// missing top-level required field
	badTop := nestedService{Addr: nestedAddr{Host: "h", Port: 1}}
	if _, err := invoke(t, fn, []any{badTop}); err == nil {
		t.Fatal("expected validation error for empty Name")
	}
}

func TestDepInjectMixedArgs(t *testing.T) {
	var logged string
	fn := func(cfg validationConfig, log func(string), items []int) string {
		log("called")
		return cfg.Name + ":" + strconv.Itoa(len(items))
	}

	valid := validationConfig{Name: "svc", Email: "a@b.com", Port: 80, Protocol: "udp"}
	logger := func(msg string) { logged = msg }
	args := []any{valid, logger, []int{1, 2, 3}}

	res, err := invoke(t, fn, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "svc:3" {
		t.Fatalf("unexpected result %v", res[0].Interface())
	}
	if logged != "called" {
		t.Fatalf("expected logger to be called, got %q", logged)
	}
}

func TestDepInjectRecursionWithFieldValidation(t *testing.T) {
	// struct arg with an inject tag (recursion mode) plus a validated field:
	// the validated field must still be checked, and skipped fields dropped.
	type recValidated struct {
		Skip string `inject:"ignore-check"`
		Code int    `validate:"gte=0"`
	}

	metas, err := GetStructByFunctionType(reflect.TypeOf(func(r recValidated) int { return r.Code }))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// valid: Code >= 0, only Code emitted
	converted, err := ConvertArgs(metas, []any{recValidated{Skip: "x", Code: 5}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	children := converted[0].([]any)
	if len(children) != 1 || children[0].(int) != 5 {
		t.Fatalf("expected [5], got %v", children)
	}

	// invalid: Code < 0 must fail per-field validation
	if _, err := ConvertArgs(metas, []any{recValidated{Skip: "x", Code: -1}}); err == nil {
		t.Fatal("expected validation error for negative Code in recursion mode")
	}
}

func TestDepInjectBoolAndBasicTypes(t *testing.T) {
	fn := func(ok bool, small int8, u uint, big int64, r rune) (bool, int8, uint, int64, rune) {
		return ok, small, u, big, r
	}
	res, err := invoke(t, fn, []any{true, json.Number("7"), json.Number("9"), json.Number("9000000000"), 'x'})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res[0].Interface().(bool) || res[1].Interface().(int8) != 7 ||
		res[2].Interface().(uint) != 9 || res[3].Interface().(int64) != 9000000000 ||
		res[4].Interface().(rune) != 'x' {
		t.Fatalf("unexpected results %v", res)
	}
}

func TestDepInjectPointerBasicArg(t *testing.T) {
	fn := func(p *int) int { return *p }

	v := 42
	res, err := invoke(t, fn, []any{&v})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(int) != 42 {
		t.Fatalf("expected 42, got %v", res[0].Interface())
	}

	// wrong kind: string where *int expected
	if _, err := invoke(t, fn, []any{"nope"}); err == nil {
		t.Fatal("expected error casting string to *int")
	}
}

func TestDepInjectStructAndErrorReturn(t *testing.T) {
	fn := func(c validationConfig, a nestedAddr) (string, error) {
		if c.Name == "boom" {
			return "", errors.New("boom")
		}
		return c.Name + ":" + a.Host, nil
	}

	good := validationConfig{Name: "svc", Email: "a@b.com", Port: 80, Protocol: "tcp"}
	addr := nestedAddr{Host: "h", Port: 1}
	res, err := invoke(t, fn, []any{good, addr})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "svc:h" {
		t.Fatalf("unexpected result %v", res[0].Interface())
	}
	if res[1].Interface() != nil {
		t.Fatalf("expected nil error, got %v", res[1].Interface())
	}

	// function-returned error is propagated through res[1]
	boom := validationConfig{Name: "boom", Email: "a@b.com", Port: 80, Protocol: "tcp"}
	res, err = invoke(t, fn, []any{boom, addr})
	if err != nil {
		t.Fatalf("unexpected error during call: %v", err)
	}
	if res[1].Interface() == nil {
		t.Fatal("expected function-returned error, got nil")
	}
}

type collectionArg struct {
	Items []int            `validate:"min=1,max=3"`
	Tags  map[string]int   `validate:"min=1"`
}

func TestDepInjectSliceMapFieldValidation(t *testing.T) {
	fn := func(c collectionArg) int { return len(c.Items) }

	valid := collectionArg{Items: []int{1, 2}, Tags: map[string]int{"a": 1}}
	if _, err := invoke(t, fn, []any{valid}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cases := map[string]collectionArg{
		"empty items":  {Items: []int{}, Tags: map[string]int{"a": 1}},
		"too many":     {Items: []int{1, 2, 3, 4}, Tags: map[string]int{"a": 1}},
		"empty tags":   {Items: []int{1}, Tags: map[string]int{}},
	}
	for name, cfg := range cases {
		if _, err := invoke(t, fn, []any{cfg}); err == nil {
			t.Fatalf("expected validation error for: %s", name)
		}
	}
}

func TestDepInjectWrongKindArg(t *testing.T) {
	fn := func(i int) int { return i }
	if _, err := invoke(t, fn, []any{"not-a-number"}); err == nil {
		t.Fatal("expected error when passing string where int is required")
	}
}

func TestDepInjectFloatPrecision(t *testing.T) {
	fn := func(f float32, d float64) (float32, float64) { return f, d }
	res, err := invoke(t, fn, []any{json.Number("3.14159"), json.Number("2.718281828")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// float32 truncates precision
	if res[0].Interface().(float32) != float32(3.14159) {
		t.Fatalf("unexpected float32 %v", res[0].Interface())
	}
	if res[1].Interface().(float64) != 2.718281828 {
		t.Fatalf("unexpected float64 %v", res[1].Interface())
	}
}

func TestDepInjectStructReturnedAndPointerStruct(t *testing.T) {
	fn := func(name string) validationConfig {
		return validationConfig{Name: name, Email: "a@b.com", Port: 1, Protocol: "tcp"}
	}
	res, err := invoke(t, fn, []any{"x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res[0].Interface().(validationConfig)
	if out.Name != "x" {
		t.Fatalf("unexpected returned struct %v", out)
	}
}

func TestDepInjectAllNilArgs(t *testing.T) {
	fn := func(a int, b string) (int, string) { return a, b }
	metas, err := GetStructByFunctionType(reflect.TypeOf(fn))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// every arg nil -> ConvertArgs drops them, result is empty
	converted, err := ConvertArgs(metas, []any{nil, nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(converted) != 0 {
		t.Fatalf("expected empty result for all-nil args, got %v", converted)
	}
}

func TestCallFunctionBasic(t *testing.T) {
	fn := func(a string, b int, c float64) string {
		return fmt.Sprintf("%s:%d:%v", a, b, c)
	}
	res, err := CallFunction(fn, []any{"x", json.Number("5"), json.Number("1.5")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "x:5:1.5" {
		t.Fatalf("unexpected %v", res[0].Interface())
	}
}

func TestCallFunctionWithStatic(t *testing.T) {
	fn := func(ctx string, name string) string { return ctx + ":" + name }
	res, err := CallFunction(fn, []any{"bob"}, "ctx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "ctx:bob" {
		t.Fatalf("unexpected %v", res[0].Interface())
	}
}

func TestCallFunctionStructArg(t *testing.T) {
	fn := func(c validationConfig) string { return c.Name }

	// invalid struct (bad email / port out of range) => validation error
	if _, err := CallFunction(fn, []any{validationConfig{Name: "x", Email: "bad", Port: 99999, Protocol: "tcp"}}); err == nil {
		t.Fatal("expected validation error for invalid struct")
	}

	res, err := CallFunction(fn, []any{validationConfig{Name: "x", Email: "a@b.com", Port: 80, Protocol: "tcp"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "x" {
		t.Fatalf("unexpected %v", res[0].Interface())
	}
}

func TestCallFunctionJSONBasic(t *testing.T) {
	fn := func(a string, b int, c float64) (string, int, float64) {
		return a, b, c
	}
	data := []byte(`["hello", 7, 2.25]`)
	res, err := CallFunctionJSON(fn, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "hello" || res[1].Interface().(int) != 7 || res[2].Interface().(float64) != 2.25 {
		t.Fatalf("unexpected %v", res)
	}
}

func TestCallFunctionJSONStructArg(t *testing.T) {
	fn := func(c validationConfig) string { return c.Name }

	good := []byte(`[{"Name":"svc","Email":"a@b.com","Port":80,"Protocol":"tcp"}]`)
	res, err := CallFunctionJSON(fn, good)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "svc" {
		t.Fatalf("unexpected %v", res[0].Interface())
	}

	// invalid struct from JSON object => validation error
	bad := []byte(`[{"Name":"","Email":"bad","Port":99999,"Protocol":"tcp"}]`)
	if _, err := CallFunctionJSON(fn, bad); err == nil {
		t.Fatal("expected validation error for invalid struct from JSON")
	}
}

func TestCallFunctionJSONLengthMismatch(t *testing.T) {
	fn := func(a int) int { return a }
	if _, err := CallFunctionJSON(fn, []byte(`[1, 2]`)); err == nil {
		t.Fatal("expected length mismatch error")
	}
	if _, err := CallFunctionJSON(fn, []byte(`[]`)); err == nil {
		t.Fatal("expected length mismatch error for empty payload")
	}
}

func TestCallFunctionJSONWithStatic(t *testing.T) {
	type ctx struct{ id int }
	fn := func(c *ctx, name string) string { return fmt.Sprintf("%d:%s", c.id, name) }

	data := []byte(`["bob"]`)
	res, err := CallFunctionJSON(fn, data, &ctx{id: 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Interface().(string) != "9:bob" {
		t.Fatalf("unexpected %v", res[0].Interface())
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

	// number type coercion: json.Number "7" -> int8
	out, err = ParseJsonPayload(fn, []byte(`[7, "x"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].(int) != 7 {
		t.Fatalf("unexpected %v", out)
	}
}

