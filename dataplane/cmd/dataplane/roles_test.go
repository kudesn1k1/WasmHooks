package main

import "testing"

func TestGatewayDoesNotRequireModuleStorage(t *testing.T) {
	env := func(string) string { return "test-token" }
	args := []string{"-role=gateway", "-snapshot=test.json", "-executors=executors:9090"}
	if _, err := parseFlags(args, env); err != nil {
		t.Fatalf("gateway with discovery and no storage: %v", err)
	}
}

func TestSplitRolesRequireDiscoveryAndAuthentication(t *testing.T) {
	for _, args := range [][]string{
		{"-role=gateway", "-snapshot=test.json"},
		{"-role=gateway", "-snapshot=test.json", "-executors=executors:9090"},
		{"-role=executor", "-snapshot=test.json", "-modules-dir=modules"},
		{"-role=bad-role", "-snapshot=test.json", "-modules-dir=modules"},
	} {
		if _, err := parseFlags(args, func(string) string { return "" }); err == nil {
			t.Errorf("accepted incomplete role config: %v", args)
		}
	}
}
