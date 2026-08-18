//go:build mage

// Command mage is the build system for livekit-cli. Run targets with
// `go tool mage <target>` (mage is pinned as a go.mod tool dependency, so no
// separate install is needed), e.g. `go tool mage build` or `go tool mage generate`.
//
// The Makefile is retained as a thin shim that delegates here: GitHub's default
// CodeQL setup uses C/C++ autobuild, which detects the Makefile and runs it,
// tracing the cgo compilation of the vendored PortAudio + WebRTC APM. Keeping
// `make` -> `mage build` preserves that while making mage the source of truth.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Default target when `mage` is run with no arguments.
var Default = Build

const binDir = "bin"

// Build compiles the lk binary. This is a cgo build (CGO_ENABLED=1) that links
// the vendored PortAudio + WebRTC APM C/C++, and is the same artifact CodeQL's
// autobuild traces via the Makefile shim.
func Build() error {
	// The PortAudio C source lives in a submodule; init it so fresh clones (and
	// CodeQL checkouts) build.
	if err := run(nil, "git", "submodule", "update", "--init", "--recursive"); err != nil {
		return err
	}
	return run([]string{"CGO_ENABLED=1"}, "go", "build", "-o", filepath.Join(binDir, exe("lk")), "./cmd/lk")
}

// Install builds lk and installs it to GOBIN, with a `livekit-cli` alias for the
// legacy binary name.
func Install() error {
	if err := Build(); err != nil {
		return err
	}
	gobin, err := goBin()
	if err != nil {
		return err
	}
	dst := filepath.Join(gobin, exe("lk"))
	if err := copyFile(filepath.Join(binDir, exe("lk")), dst); err != nil {
		return err
	}
	alias := filepath.Join(gobin, exe("livekit-cli"))
	_ = os.Remove(alias)
	if runtime.GOOS == "windows" {
		return copyFile(dst, alias)
	}
	return os.Symlink(dst, alias)
}

// Test runs the Go test suite.
func Test() error { return run(nil, "go", "test", "./...") }

// Clean removes build artifacts.
func Clean() error { return os.RemoveAll(binDir) }

// Generate regenerates the LiveKit Public API Connect client from the protobufs
// under protobufs/livekit/publicapi into pkg/gen. It runs protoc with the
// protoc-gen-go and protoc-gen-connect-go plugins pinned via go.mod tool
// directives (built on the fly into bin/), mirroring the public-api-server's
// buf setup so the generated package layout matches the future published Go
// library. The livekit/protocol proto imports (used by the simulations service)
// are resolved from the github.com/livekit/protocol module in go.mod, so they
// always match the generated Go types the CLI already depends on.
//
// Requires a local `protoc` (e.g. `brew install protobuf`); the generated Go is
// committed, so only regeneration needs it.
func Generate() error {
	if _, err := exec.LookPath("protoc"); err != nil {
		return fmt.Errorf("protoc not found on PATH; install it (e.g. `brew install protobuf`)")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	// Build the codegen plugins from the pinned tool dependencies into bin/.
	for _, pkg := range []string{
		"google.golang.org/protobuf/cmd/protoc-gen-go",
		"connectrpc.com/connect/cmd/protoc-gen-connect-go",
	} {
		if err := run(nil, "go", "build", "-o", filepath.Join(binDir, exe(filepath.Base(pkg))), pkg); err != nil {
			return fmt.Errorf("build plugin %s: %w", pkg, err)
		}
	}

	// Resolve third-party proto import roots from their Go modules (version-matched
	// to go.mod), rather than vendoring them: livekit/protocol (livekit_*.proto,
	// used by projects/simulations) and livekit/cloud-protocol (pii.proto, used by
	// projects). Both are imported flat, so the module dir goes straight on -I.
	protoModDir, err := output("go", "list", "-m", "-f", "{{.Dir}}", "github.com/livekit/protocol")
	if err != nil {
		return fmt.Errorf("locate github.com/livekit/protocol: %w", err)
	}
	protocolProtos := filepath.Join(protoModDir, "protobufs")

	cloudProtoDir, err := output("go", "list", "-m", "-f", "{{.Dir}}", "github.com/livekit/cloud-protocol")
	if err != nil {
		return fmt.Errorf("locate github.com/livekit/cloud-protocol: %w", err)
	}

	// go_package mappings for the publicapi protos (they carry no go_package
	// option — managed by buf upstream — so map each here to our pkg/gen path,
	// matching the server's <domain>v1 / <domain>v1connect package names).
	const goPrefix = "github.com/livekit/livekit-cli/v2/pkg/gen/livekit/publicapi"
	domains := []string{"common", "projects", "users", "workspaces", "analytics", "simulations"}
	mappings := make([]string, 0, len(domains))
	for _, d := range domains {
		mappings = append(mappings, fmt.Sprintf("Mlivekit/publicapi/%s/v1/%s.proto=%s/%s/v1;%sv1", d, d, goPrefix, d, d))
	}
	mopt := strings.Join(mappings, ",")

	protos, err := filepath.Glob("protobufs/livekit/publicapi/*/v1/*.proto")
	if err != nil {
		return err
	}
	if len(protos) == 0 {
		return fmt.Errorf("no protos found under protobufs/livekit/publicapi")
	}

	if err := os.RemoveAll("pkg/gen"); err != nil {
		return err
	}
	if err := os.MkdirAll("pkg/gen", 0o755); err != nil {
		return err
	}

	absBin, err := filepath.Abs(binDir)
	if err != nil {
		return err
	}
	args := []string{
		"-I", "protobufs",
		"-I", protocolProtos,
		"-I", cloudProtoDir,
		"--go_out=pkg/gen", "--go_opt=paths=source_relative," + mopt,
		"--connect-go_out=pkg/gen", "--connect-go_opt=paths=source_relative," + mopt,
	}
	args = append(args, protos...)

	fmt.Println("generating Connect client: protobufs/livekit/publicapi -> pkg/gen ...")
	// Put bin/ first on PATH so protoc finds the pinned plugins we just built.
	return run([]string{"PATH=" + absBin + string(os.PathListSeparator) + os.Getenv("PATH")}, "protoc", args...)
}

// --- helpers ---

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func run(extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	return cmd.Run()
}

func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func goBin() (string, error) {
	gobin, err := output("go", "env", "GOBIN")
	if err != nil {
		return "", err
	}
	if gobin != "" {
		return gobin, nil
	}
	gopath, err := output("go", "env", "GOPATH")
	if err != nil {
		return "", err
	}
	return filepath.Join(gopath, "bin"), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
