package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion_ExplicitVersionWins(t *testing.T) {
	prevVersion := version
	prevReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version = prevVersion
		readBuildInfo = prevReadBuildInfo
	})

	version = "1.2.3"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v9.9.9"},
		}, true
	}

	if got := resolveVersion(); got != "1.2.3" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestResolveVersion_FallsBackToBuildInfo(t *testing.T) {
	prevVersion := version
	prevReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version = prevVersion
		readBuildInfo = prevReadBuildInfo
	})

	version = "dev"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v2.0.0"},
		}, true
	}

	if got := resolveVersion(); got != "v2.0.0" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "v2.0.0")
	}
}

func TestResolveVersion_DefaultDev(t *testing.T) {
	prevVersion := version
	prevReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version = prevVersion
		readBuildInfo = prevReadBuildInfo
	})

	version = "dev"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "(devel)"},
		}, true
	}

	if got := resolveVersion(); got != "dev" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "dev")
	}
}
