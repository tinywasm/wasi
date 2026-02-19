package wasi

import (
	"reflect"
	"testing"
)

func TestParseRule(t *testing.T) {
	tests := []struct {
		content string
		want    Rule
	}{
		{"*", Rule{All: true}},
		{"", Rule{All: true}},
		{"  ", Rule{All: true}},
		{"users,auth", Rule{Only: []string{"users", "auth"}}},
		{"-auth", Rule{All: true, Except: []string{"auth"}}},
		{"users,-admin", Rule{Only: []string{"users"}, All: true, Except: []string{"admin"}}},
	}

	for _, tt := range tests {
		got := parseRule(tt.content)
		if got.All != tt.want.All || !reflect.DeepEqual(got.Only, tt.want.Only) || !reflect.DeepEqual(got.Except, tt.want.Except) {
			t.Errorf("parseRule(%q) = %+v, want %+v", tt.content, got, tt.want)
		}
	}
}

func TestMiddlewareModule_Matches(t *testing.T) {
	mws := []struct {
		name  string
		rule  Rule
		tests map[string]bool
	}{
		{"all", Rule{All: true}, map[string]bool{"any": true, "other": true}},
		{"only", Rule{Only: []string{"users", "auth"}}, map[string]bool{"users": true, "auth": true, "other": false}},
		{"except", Rule{All: true, Except: []string{"auth"}}, map[string]bool{"users": true, "auth": false, "any": true}},
	}

	for _, tt := range mws {
		mw := &MiddlewareModule{Rule: tt.rule}
		for route, want := range tt.tests {
			if got := mw.Matches(route); got != want {
				t.Errorf("Middleware(%s).Matches(%s) = %v, want %v", tt.name, route, got, want)
			}
		}
	}
}

func TestApplyPipeline(t *testing.T) {
	mws := []*MiddlewareModule{
		{Module: &Module{name: "mw1"}, Rule: Rule{All: true}},
		{Module: &Module{name: "mw2"}, Rule: Rule{Only: []string{"users"}}},
		{Module: &Module{name: "mw3"}, Rule: Rule{All: true, Except: []string{"users"}}},
	}

	// Test for route "users"
	got := applyPipeline("users", mws)
	if len(got) != 2 || got[0].Module.name != "mw1" || got[1].Module.name != "mw2" {
		t.Errorf("Pipeline for 'users' wrong")
	}

	// Test for route "auth"
	got = applyPipeline("auth", mws)
	if len(got) != 2 || got[0].Module.name != "mw1" || got[1].Module.name != "mw3" {
		t.Errorf("Pipeline for 'auth' wrong")
	}
}
