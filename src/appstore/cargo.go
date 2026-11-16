package appstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Cargo mirrors Rust crates: `cargo vendor` (the image's) fetches the crate and everything it needs from crates.io, checking every package against
// the checksum of the registry index, into <bundle>/vendor. A terminal uses the folders as a replacement of crates.io (see helper), offline. The
// resolver is told to prefer versions whose rust-version this Rust can build (incompatible-rust-versions = fallback), so the newest version that
// works with the image's Rust is the one that is kept, its dependencies too.
type Cargo struct {
	Dir    string // the store
	Lookup MirrorLookup
	Run    runner
}

func (c Cargo) Name() string { return "cargo mirror (crates.io)" }

func (c Cargo) cargo() (string, error) {
	if p := toolPath("cargo", "/root/.cargo/bin/cargo"); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("cargo is not installed on this server")
}

var crateName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (c Cargo) env(home string) []string {
	return []string{"CARGO_HOME=" + home, "CARGO_RESOLVER_INCOMPATIBLE_RUST_VERSIONS=fallback", "CARGO_TERM_COLOR=never", "CARGO_NET_RETRY=3"}
}

// project writes a library project that depends on the crate (at the pinned version, or "*").
func project(dir, crate, pin string) error {
	if !crateName.MatchString(crate) {
		return fmt.Errorf("%q is not a crate name", crate)
	}
	spec := "*"
	if b, ok := upperBound(pin); ok {
		spec = "<" + b // the newest below it (cargo's resolver, with the rust-version fallback, picks it)
	} else if pin != "" {
		spec = "=" + pin
	}
	manifest := fmt.Sprintf("[package]\nname = \"openrepl-mirror\"\nversion = \"0.0.0\"\nedition = \"2021\"\n\n[dependencies]\n%s = %q\n", crate, spec)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), nil, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(manifest), 0644)
}

// lockedVersion reads the version of a crate from a Cargo.lock.
func lockedVersion(lock, crate string) string {
	lines := strings.Split(lock, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == fmt.Sprintf("name = %q", crate) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "version = ") {
			return strings.Trim(strings.TrimPrefix(strings.TrimSpace(lines[i+1]), "version = "), `"`)
		}
	}
	return ""
}

// vendor resolves and vendors the crate into vendorDir; it returns the version.
func (c Cargo) vendor(ctx context.Context, tmp, vendorDir, crate, pin string) (string, error) {
	cargo, err := c.cargo()
	if err != nil {
		return "", err
	}
	if err := project(tmp, crate, pin); err != nil {
		return "", err
	}
	run := orRun(c.Run)
	env := c.env(filepath.Join(tmp, "home"))
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if out, err := run(ctx, tmp, env, cargo, "generate-lockfile"); err != nil {
		return "", fmt.Errorf("cargo cannot resolve %s: %s", crate, tail(strings.TrimSpace(string(out)), 400))
	}
	if out, err := run(ctx, tmp, env, cargo, "vendor", "--locked", vendorDir); err != nil {
		return "", fmt.Errorf("cargo vendor: %s", tail(strings.TrimSpace(string(out)), 400))
	}
	lock, _ := os.ReadFile(filepath.Join(tmp, "Cargo.lock"))
	v := lockedVersion(string(lock), crate)
	if v == "" {
		return "", fmt.Errorf("cargo resolved no version of %s", crate)
	}
	return v, nil
}

func (c Cargo) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	e, err := lookupEntry(c.Lookup, KindCrate, name)
	if err != nil {
		return Resolved{}, err
	}
	if version == "" {
		version = e.Version
	}
	tmp, err := os.MkdirTemp(c.Dir, "cargo-resolve-")
	if err != nil {
		return Resolved{}, err
	}
	defer os.RemoveAll(tmp)
	vd := filepath.Join(tmp, "vendor")
	v, err := c.vendor(ctx, tmp, vd, e.Source(), version)
	if err != nil {
		return Resolved{}, err
	}
	entries, _ := os.ReadDir(vd)
	mb := dirMB(vd)
	return Resolved{Version: v, Packages: len(entries), DownloadMB: mb / 3, DiskMB: mb}, nil
}

func (c Cargo) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	e, err := lookupEntry(c.Lookup, KindCrate, name)
	if err != nil {
		return err
	}
	if version == "" {
		version = e.Version
	}
	tmp, err := os.MkdirTemp(c.Dir, "cargo-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	v, err := c.vendor(ctx, tmp, filepath.Join(tmp, "vendor"), e.Source(), version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	if err := moveDir(filepath.Join(tmp, "vendor"), filepath.Join(dest, "vendor")); err != nil {
		return err
	}
	entries, _ := os.ReadDir(filepath.Join(dest, "vendor"))
	fmt.Fprintf(log, "%s %s: %d crates, %.1f MB\n", e.Source(), v, len(entries), dirMB(dest))
	return os.WriteFile(filepath.Join(dest, "mirror.json"), []byte(fmt.Sprintf("{\"source\":%q,\"version\":%q}\n", e.Source(), v)), 0644)
}

func (c Cargo) Search(ctx context.Context, q, kind string) ([]Found, error) { return nil, nil }

// CargoConfig is the config.toml that makes cargo use the vendored folder in place of crates.io, with no network.
func CargoConfig(vendorDir string) string {
	return fmt.Sprintf("[source.crates-io]\nreplace-with = \"vendored-sources\"\n\n[source.vendored-sources]\ndirectory = %q\n\n[net]\noffline = true\n", vendorDir)
}

// check proves a project that depends on the crate builds with only the mirror.
func (c Cargo) check(ctx context.Context, dest string, e Entry, version string) error {
	cargo, err := c.cargo()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(c.Dir, "cargo-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := project(tmp, e.Source(), version); err != nil {
		return err
	}
	home := filepath.Join(tmp, "home")
	os.MkdirAll(home, 0755)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(CargoConfig(filepath.Join(dest, "vendor"))), 0644); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if out, err := orRun(c.Run)(ctx, tmp, c.env(home), cargo, "check", "--offline"); err != nil {
		return fmt.Errorf("a project that depends on %s does not build with the mirror: %s", e.Source(), tail(strings.TrimSpace(string(out)), 500))
	}
	return nil
}
