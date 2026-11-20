// Package bridgefiles contains only the distributable OC program.
package bridgefiles

import "embed"

// Files excludes tests, live credentials and execution journals.
//
//go:embed lib/aeorbit/*.lua bin/ae-orbit.lua bin/ae-orbit-register.lua bin/ae-orbit-throughput.lua bin/ae-orbit-exact-only.lua bin/ae-orbit-exact-speed.lua bin/ae-orbit-cpu-full.lua bin/ae-orbit-install-recover.lua rc.d/ae-orbit.lua config.example.lua install.lua
var Files embed.FS
