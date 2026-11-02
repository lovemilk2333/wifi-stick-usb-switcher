package daemon

import "testing"

func TestDomainFilterRules(t *testing.T) {
	filter, err := parse_domain_filter([]string{
		"10.22.33.0/24",
		"+.a.com",
		"*.b.com",
		"?.c.com",
		"+*.d.com",
		"**.e.com",
	})
	if err != nil {
		t.Fatalf("parse domain filter: %v", err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"10.22.33.5", true},
		{"10.22.34.5", false},
		{"a.com", false},
		{"x.a.com", true},
		{"x.y.a.com", true},
		{"b.com", true},
		{"x.b.com", true},
		{"x.y.b.com", true},
		{"c.com", false},
		{"x.c.com", true},
		{"xy.c.com", false},
		{"x.y.c.com", false},
		{"xn--bcher-kva.d.com", true},
		{"x.d.com", false},
		{"xn--bcher-kva.x.d.com", false},
		{"x.e.com", true},
		{"x.y.e.com", true},
		{"e.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filter.allows(tc.name); got != tc.want {
				t.Fatalf("domain filter allows %q = %t, want %t", tc.name, got, tc.want)
			}
		})
	}
}

func TestParseDomainFilterRejectsInvalidRules(t *testing.T) {
	for _, invalid := range [][]string{{"10.22.33.1"}, {"*.a.*.com"}, {"?.a.com.extra?"}, {""}} {
		if _, err := parse_domain_filter(invalid); err == nil {
			t.Errorf("parse_domain_filter(%q) expected error", invalid)
		}
	}
}

func TestEmptyDomainFilterAllowsEverything(t *testing.T) {
	filter, err := parse_domain_filter(nil)
	if err != nil {
		t.Fatalf("parse empty domain filter: %v", err)
	}
	if filter.configured() {
		t.Fatal("empty filter should not be configured")
	}
	if !filter.allows("not-filtered.example") {
		t.Fatal("empty filter should preserve the unfiltered behavior")
	}
}
