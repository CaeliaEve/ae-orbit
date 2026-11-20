package bridgefiles

import (
	"regexp"
	"testing"
)

func TestLifecycleFilesAreBundled(t *testing.T) {
	for _, path := range []string{
		"lib/aeorbit/cancel.lua", "lib/aeorbit/runtime.lua", "lib/aeorbit/service.lua", "rc.d/ae-orbit.lua",
		"bin/ae-orbit-throughput.lua", "bin/ae-orbit-exact-only.lua", "bin/ae-orbit-exact-speed.lua", "bin/ae-orbit-cpu-full.lua", "lib/aeorbit/cpu_packet.lua",
		"bin/ae-orbit-install-recover.lua",
	} {
		data, err := Files.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatalf("installer dependency %s unavailable: %v", path, err)
		}
	}
}

func TestInstallerManifestFilesAreBundled(t *testing.T) {
	installer, err := Files.ReadFile("install.lua")
	if err != nil {
		t.Fatal(err)
	}
	paths := regexp.MustCompile(`\{"([^"]+)", "/[^"]+"\}`).FindAllSubmatch(installer, -1)
	if len(paths) < 20 {
		t.Fatal("installer manifest was not found")
	}
	for _, match := range paths {
		path := string(match[1])
		data, err := Files.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatalf("installer dependency %s unavailable: %v", path, err)
		}
	}
}
