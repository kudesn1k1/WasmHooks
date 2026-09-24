package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func env(name string) Import  { return Import{Module: "extism:host/env", Name: name} }
func user(name string) Import { return Import{Module: "extism:host/user", Name: name} }

func TestCheckModule(t *testing.T) {
	base := []Import{env("input_length"), env("input_load_u64"), env("alloc"), env("output_set"), env("config_get"), env("log_info")}
	with := func(extra ...Import) []Import { return append(append([]Import{}, base...), extra...) }
	cases := []struct {
		name    string
		info    ModuleInfo
		spec    ModuleSpec
		wantErr string
	}{
		{"ok", ModuleInfo{Imports: base, Exports: []string{"handle"}}, ModuleSpec{}, ""},
		{"missing handle", ModuleInfo{Imports: base, Exports: []string{"run"}}, ModuleSpec{}, `missing export "handle"`},
		{"http denied", ModuleInfo{Imports: with(env("http_request")), Exports: []string{"handle"}}, ModuleSpec{}, "extism:host/env.http_request"},
		{"unknown kernel fn", ModuleInfo{Imports: with(env("something_new")), Exports: []string{"handle"}}, ModuleSpec{}, "extism:host/env.something_new"},
		{"user fn not allowed", ModuleInfo{Imports: with(user("kv_get")), Exports: []string{"handle"}}, ModuleSpec{}, "extism:host/user.kv_get"},
		{"user fn allowed", ModuleInfo{Imports: with(user("kv_get")), Exports: []string{"handle"}}, ModuleSpec{AllowedHostFunctions: []string{"kv_get"}}, ""},
		{"wasi denied", ModuleInfo{Imports: with(Import{"wasi_snapshot_preview1", "fd_write"}), Exports: []string{"handle"}}, ModuleSpec{}, "wasi_snapshot_preview1.fd_write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckModule(tc.info, tc.spec)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidModule) {
				t.Fatalf("want ErrInvalidModule, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckModuleReportsAllViolations(t *testing.T) {
	info := ModuleInfo{Imports: []Import{env("http_request"), user("x")}}
	err := CheckModule(info, ModuleSpec{})
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{`"handle"`, "http_request", "extism:host/user.x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
