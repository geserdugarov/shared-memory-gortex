package lbug

//go:generate bash ../../../scripts/fetch-lbug.sh

/*
// liblbug is fetched by scripts/fetch-lbug.sh (not committed).
//
// linux + darwin: STATIC — liblbug.a is linked in (only the archive
// lives in lib/static/<os>-<arch>/, so `-llbug` resolves to it) for a
// self-contained binary with no runtime lib to ship. The C++ runtime is
// linked too: libc++ on darwin (system, always present); libstdc++ +
// libgcc statically on linux so the binary doesn't need them at runtime.
//
// windows: DYNAMIC — lbug's windows release is MSVC-built (its C++
// runtime is MSVCP140/VCRUNTIME140), which cannot be statically linked
// into a mingw binary. The .exe links directly against lbug_shared.dll
// (mingw ld reads the DLL's clean C ABI export table via -l:<file>, so
// no import lib / gendef is needed) and ships the DLL — plus the VC++
// runtime — alongside the .exe at runtime.
// FTS extensions + dlopen: liblbug loads its FTS (and other) extensions
// via dlopen at runtime, and those extension .so/.dylibs resolve liblbug's
// C++ symbols (e.g. typeinfo for lbug::catalog::IndexAuxInfo) FROM THE HOST
// PROCESS. When liblbug is a shared lib those symbols are globally visible;
// static-linked, two things must be true at link time:
//
//   1. the symbol must be PRESENT in the binary. Most of the symbols the
//      extension needs are C++ RTTI (typeinfo/vtable) emitted as weak
//      COMDAT data in liblbug.a. gortex's plain-C API calls never trigger
//      RTTI, so nothing in the link references them, so demand-driven
//      archive selection DROPS those object files entirely. -rdynamic
//      cannot export a symbol that was never linked in. --whole-archive
//      around -llbug forces every liblbug object (and thus every weak
//      typeinfo/vtable) into the binary, exactly as a shared liblbug would
//      expose them. --no-whole-archive turns it back off before the system
//      libs so we don't try to whole-archive libstdc++/libm/etc.
//   2. the symbol must be EXPORTED in the dynamic symbol table so the
//      dlopen'd extension can bind to it: -rdynamic (clang -> -export_dynamic,
//      gcc -> --export-dynamic).
//
// darwin doesn't need --whole-archive: ld64 pulls the typeinfo objects in
// on its own, so -rdynamic alone suffices there.
//
// --whole-archive is NOT on cgo's #cgo LDFLAGS allowlist, so the linux
// build paths export CGO_LDFLAGS_ALLOW='-Wl,--(no-)?whole-archive' (Makefile
// / CI test job / release goreleaser env). Without it the linux build fails
// with "invalid flag in #cgo LDFLAGS". -rdynamic IS on the allowlist.
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/static/darwin-amd64 -llbug -lc++ -rdynamic
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/lib/static/darwin-arm64 -llbug -lc++ -rdynamic
// libstdc++ is wrapped in -Wl,-Bstatic/-Bdynamic (NOT -static-libstdc++):
// cgo may link the final binary with the C driver (gcc), which never
// auto-appends libstdc++, so -static-libstdc++ could be a no-op and the
// explicit -lstdc++ would resolve to libstdc++.so.6 at runtime —
// defeating the self-contained goal. -Bstatic forces the .a. libm/dl/
// pthread stay dynamic (system libs always present); libgcc is statically
// linked via -static-libgcc. --export-dynamic exposes liblbug's symbols
// for the dlopen'd FTS extension (see darwin note above).
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/lib/static/linux-amd64 -Wl,--whole-archive -llbug -Wl,--no-whole-archive -Wl,-Bstatic -lstdc++ -Wl,-Bdynamic -lm -ldl -lpthread -static-libgcc -rdynamic
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/lib/static/linux-arm64 -Wl,--whole-archive -llbug -Wl,--no-whole-archive -Wl,-Bstatic -lstdc++ -Wl,-Bdynamic -lm -ldl -lpthread -static-libgcc -rdynamic
#cgo windows LDFLAGS: -L${SRCDIR}/lib/dynamic/windows -l:lbug_shared.dll
#include "lbug.h"
*/
import "C"
