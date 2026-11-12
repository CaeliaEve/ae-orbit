// Package bridgefiles contains only the distributable OC program.
package bridgefiles

import "embed"

// Files excludes tests, live credentials and execution journals.
//
//go:embed lib/aeorbit/*.lua bin/ae-orbit.lua rc.d/ae-orbit.lua config.example.lua install.lua
var Files embed.FS
