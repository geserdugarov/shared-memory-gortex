package resolver

import "testing"

func TestIsCppStdlibHeader(t *testing.T) {
	stdlib := []string{
		"stdio.h", "stdlib.h", "string.h", "stdatomic.h", // C
		"vector", "memory", "string", "unordered_map", "filesystem", // C++
		"cstdio", "cstring", "cstdint", // <cXXX> wrappers
		"unistd.h", "pthread.h", "sys/types.h", "arpa/inet.h", // POSIX
	}
	for _, h := range stdlib {
		if !IsCppStdlibHeader(h) {
			t.Errorf("IsCppStdlibHeader(%q) = false, want true", h)
		}
	}
	notStdlib := []string{
		"", "proj/api.h", "myheader.h", "vector.h", "foo", "config.h", "string_view.h",
	}
	for _, h := range notStdlib {
		if IsCppStdlibHeader(h) {
			t.Errorf("IsCppStdlibHeader(%q) = true, want false", h)
		}
	}
}
