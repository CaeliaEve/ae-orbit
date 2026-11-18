package bridgefiles

import "testing"

func TestLifecycleFilesAreBundled(t *testing.T) {
	for _, path := range []string{
		"lib/aeorbit/cancel.lua", "lib/aeorbit/runtime.lua", "lib/aeorbit/service.lua", "rc.d/ae-orbit.lua",
		"bin/ae-orbit-throughput.lua", "bin/ae-orbit-exact-only.lua", "bin/ae-orbit-exact-speed.lua",
	} {
		data, err := Files.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatalf("installer dependency %s unavailable: %v", path, err)
		}
	}
}
