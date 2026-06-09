#!/usr/bin/env bash
# Prepare per-target cross-compile inputs for `go build` / `goreleaser build`.
#
# Usage: scripts/setup-cross.sh <target>
#   target: linux/amd64 | linux/arm64 | windows/amd64 | windows/arm64 | darwin/arm64
#
# Outputs (under .cross/<target>/):
#   include/  — headers for the target (linux only)
#   lib/      — static libraries for the target (linux only)
#
# For windows targets, also pre-generates MinGW import libraries in zig's lib
# dir (lld needs .a, but zig only bundles .def files).
#
# Idempotent: re-runs skip work if outputs already exist.

set -euo pipefail

ALSA_VERSION="1.2.12"
ALSA_URL="https://www.alsa-project.org/files/pub/lib/alsa-lib-${ALSA_VERSION}.tar.bz2"

target="${1:-}"
if [ -z "$target" ]; then
    echo "usage: $0 <target>" >&2
    echo "  target: linux/{amd64,arm64} | windows/{amd64,arm64} | darwin/arm64" >&2
    exit 2
fi

root="$(cd "$(dirname "$0")/.." && pwd)"
out_dir="$root/.cross/${target//\//_}"
mkdir -p "$out_dir"

build_alsa() {
    local zig_target="$1" host="$2"
    local prefix="$out_dir"
    local stamp="$prefix/.alsa-${ALSA_VERSION}.stamp"

    if [ -f "$stamp" ] && [ -f "$prefix/lib/libasound.a" ]; then
        echo "[$target] alsa-lib ${ALSA_VERSION} already built"
        return
    fi

    echo "[$target] building alsa-lib ${ALSA_VERSION} with zig cc -target $zig_target"

    local work
    work="$(mktemp -d)"
    trap "rm -rf '$work'" RETURN

    (
        cd "$work"
        curl -fsSL "$ALSA_URL" | tar xjf -
        cd "alsa-lib-${ALSA_VERSION}"

        # zig cc wraps clang and accepts a -target triple; we make it look like
        # a single binary to configure scripts that don't quote $CC.
        local cc_wrap="$work/zigcc"
        cat > "$cc_wrap" <<EOF
#!/usr/bin/env bash
exec zig cc -target $zig_target "\$@"
EOF
        chmod +x "$cc_wrap"

        CC="$cc_wrap" \
        AR="zig ar" \
        RANLIB="zig ranlib" \
        CFLAGS="-O2 -fPIC" \
            ./configure \
                --host="$host" \
                --prefix="$prefix" \
                --enable-static --disable-shared \
                --disable-python --disable-alisp --disable-aload \
                --disable-old-symbols --disable-topology --without-debug

        # alsa-lib's parallel build occasionally races on header generation;
        # one retry with -j1 is cheaper than serial-only every time.
        make -j"$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)" \
            || make -j1
        make install
    )

    touch "$stamp"
    echo "[$target] alsa-lib installed to $prefix"
}

generate_mingw_libs() {
    local machine="$1"
    local zig_lib def_dir
    # zig 0.14 outputs JSON ("lib_dir": "..."); zig 0.16 outputs ZON
    # (.lib_dir = "..."). The [":= ]* class spans both separators so the capture
    # starts at the value, not the JSON key's closing quote.
    zig_lib="$(zig env | sed -n 's/.*lib_dir[":= ]*\([^"]*\)".*/\1/p')"
    def_dir="$zig_lib/libc/mingw/lib-common"

    if [ -z "$zig_lib" ] || [ ! -d "$def_dir" ]; then
        echo "[$target] could not locate zig mingw lib dir (zig_lib='$zig_lib', def_dir='$def_dir')" >&2
        return 1
    fi

    local mingw_out="$out_dir/mingw_lib"
    mkdir -p "$mingw_out"
    local stamp="$mingw_out/.generated.stamp"

    if [ -f "$stamp" ]; then
        echo "[$target] MinGW import libs already generated"
        return
    fi

    # Go's compiled objects embed COFF /DEFAULTLIB directives (dbghelp, bcrypt,
    # ...) that lld resolves directly, bypassing zig's lazy .def→.a generation.
    # Generate into a per-arch directory so windows/amd64 and windows/arm64 can
    # build in the same goreleaser run without clobbering each other.
    local generated=0
    for def in "$def_dir"/*.def; do
        local lib
        lib="$(basename "$def" .def)"
        if zig dlltool -d "$def" -l "$mingw_out/lib${lib}.a" -m "$machine" 2>/dev/null; then
            generated=$((generated + 1))
        fi
    done
    touch "$stamp"
    echo "[$target] generated $generated MinGW import libs in $mingw_out"
}

case "$target" in
    linux/amd64)
        build_alsa "x86_64-linux-gnu.2.28" "x86_64-linux-gnu"
        ;;
    linux/arm64)
        build_alsa "aarch64-linux-gnu.2.28" "aarch64-linux-gnu"
        ;;
    windows/amd64)
        generate_mingw_libs "i386:x86-64"
        ;;
    windows/arm64)
        generate_mingw_libs "arm64"
        ;;
    darwin/arm64|darwin/amd64)
        echo "[$target] no cross setup needed (native build)"
        ;;
    *)
        echo "unknown target: $target" >&2
        exit 2
        ;;
esac
