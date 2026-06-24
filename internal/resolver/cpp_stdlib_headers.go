package resolver

// cppStdlibHeaders is a curated allow-list of C, C++, and common POSIX
// standard-library headers, keyed by the include path exactly as it appears
// between the angle brackets (the extractor has already stripped the `<>`).
//
// It is consulted before the include resolver probes any `-I` directory: a
// known standard header is classified external up front and never probed, so
// a real STL header like `<vector>` can never accidentally bind to an in-tree
// file that happens to share its basename (e.g. a file literally named
// `vector` with no extension, or a different-language `string`). The list is
// advisory for recall (it short-circuits the search) and load-bearing for
// correctness (it is the basename-collision guard for angle includes).
var cppStdlibHeaders = func() map[string]struct{} {
	headers := []string{
		// C standard library (C89 … C23).
		"assert.h", "complex.h", "ctype.h", "errno.h", "fenv.h", "float.h",
		"inttypes.h", "iso646.h", "limits.h", "locale.h", "math.h", "setjmp.h",
		"signal.h", "stdalign.h", "stdarg.h", "stdatomic.h", "stdbit.h",
		"stdbool.h", "stdckdint.h", "stddef.h", "stdint.h", "stdio.h",
		"stdlib.h", "stdnoreturn.h", "string.h", "tgmath.h", "threads.h",
		"time.h", "uchar.h", "wchar.h", "wctype.h",
		// C++ standard library (containers, utilities, concurrency, IO, …).
		"algorithm", "any", "array", "atomic", "barrier", "bit", "bitset",
		"charconv", "chrono", "compare", "complex", "concepts",
		"condition_variable", "coroutine", "deque", "exception", "execution",
		"expected", "filesystem", "format", "forward_list", "fstream",
		"functional", "future", "initializer_list", "iomanip", "ios",
		"iosfwd", "iostream", "istream", "iterator", "latch", "limits",
		"list", "locale", "map", "memory", "memory_resource", "mutex", "new",
		"numbers", "numeric", "optional", "ostream", "queue", "random",
		"ranges", "ratio", "regex", "scoped_allocator", "semaphore",
		"shared_mutex", "source_location", "span", "spanstream", "sstream",
		"stack", "stacktrace", "stdexcept", "stdfloat", "stop_token",
		"streambuf", "string", "string_view", "strstream", "syncstream",
		"system_error", "thread", "tuple", "type_traits", "typeindex",
		"typeinfo", "unordered_map", "unordered_set", "utility", "valarray",
		"variant", "vector", "version",
		// C++ <cXXX> wrappers over the C headers.
		"cassert", "cctype", "cerrno", "cfenv", "cfloat", "cinttypes",
		"ciso646", "climits", "clocale", "cmath", "csetjmp", "csignal",
		"cstdalign", "cstdarg", "cstdbool", "cstddef", "cstdint", "cstdio",
		"cstdlib", "cstring", "ctgmath", "ctime", "cuchar", "cwchar",
		"cwctype",
		// Common POSIX / system headers (the ones an in-tree scan would
		// otherwise be tempted to mis-bind).
		"unistd.h", "pthread.h", "fcntl.h", "dirent.h", "dlfcn.h", "poll.h",
		"sched.h", "semaphore.h", "termios.h", "grp.h", "pwd.h", "syslog.h",
		"glob.h", "fnmatch.h", "ftw.h", "getopt.h", "libgen.h", "strings.h",
		"regex.h", "netdb.h", "ifaddrs.h", "endian.h", "byteswap.h",
		"malloc.h", "alloca.h", "memory.h", "mqueue.h", "aio.h", "spawn.h",
		"utime.h", "wordexp.h", "langinfo.h", "iconv.h", "search.h",
		"ucontext.h", "sys/types.h", "sys/stat.h", "sys/socket.h",
		"sys/wait.h", "sys/mman.h", "sys/time.h", "sys/select.h",
		"sys/ioctl.h", "sys/resource.h", "sys/uio.h", "sys/un.h",
		"sys/epoll.h", "sys/eventfd.h", "sys/sem.h", "sys/shm.h", "sys/msg.h",
		"sys/ipc.h", "sys/file.h", "sys/param.h", "sys/utsname.h",
		"netinet/in.h", "netinet/tcp.h", "arpa/inet.h", "net/if.h",
	}
	set := make(map[string]struct{}, len(headers))
	for _, h := range headers {
		set[h] = struct{}{}
	}
	return set
}()

// IsCppStdlibHeader reports whether name is a C / C++ / POSIX standard-library
// header (the include path between the angle brackets, e.g. "vector",
// "stdio.h", "sys/types.h"). Used both by the include resolver's angle-include
// guard and by the resolution-outcome analyzer.
func IsCppStdlibHeader(name string) bool {
	if name == "" {
		return false
	}
	_, ok := cppStdlibHeaders[name]
	return ok
}
