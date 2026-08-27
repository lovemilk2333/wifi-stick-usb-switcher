package typeinject

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProcessAnyPayload(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		wantErr bool
	}{
		{"string", "on", false},
		{"int", 1, false},
		{"bool", true, false},
		{"json number", json.Number("1.5"), false},
		{"pointer rejected", new(int), true},
		{"struct rejected", struct{ A int }{1}, true},
		{"nil rejected", nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ProcessAnyPayload(c.input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got value %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.input) {
				t.Fatalf("expected %v, got %v", c.input, got)
			}
		})
	}
}

func TestConvertAnyPayload(t *testing.T) {
	got, err := ConvertAnyPayload([]any{"on", 1, true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 values, got %d", len(got))
	}

	_, err = ConvertAnyPayload([]any{"ok", new(int)})
	if err == nil {
		t.Fatalf("expected error for pointer element")
	}
}

func TestCallFunctionJSONWithAnyParam(t *testing.T) {
	called := false
	cb := func(state any) (string, error) {
		called = true
		s, _ := state.(string)
		return "got:" + s, nil
	}

	results, err := CallFunctionJSON(cb, []byte(`["on"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("callback was not invoked")
	}
	if results[0].Interface().(string) != "got:on" {
		t.Fatalf("unexpected result: %v", results[0].Interface())
	}

	_, err = CallFunctionJSON(func(p any) {}, []byte(`[{"nested":1}]`))
	if err != nil {
		t.Fatalf("unexpected error for map value: %v", err)
	}

	type s struct{ A int }
	_, err = CallFunction(func(p any) {}, []any{s{1}})
	if err == nil {
		t.Fatalf("expected error for struct passed to any param")
	}
}
