// Package runtime embeds the Lumen v0.2 C runtime headers so the toolchain
// can produce native executables without depending on files outside the
// installed binary.
package runtime

import _ "embed"

//go:embed lumen.h
var lumenH []byte

// LumenH returns the bytes of runtime/lumen.h.
func LumenH() []byte { return lumenH }
