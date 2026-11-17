package appstore

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Maven mirrors Java libraries: the jar and everything it needs (its runtime dependencies) from Maven Central, with the checksums of every file
// checked (Apache Maven, strict), into <bundle>/jars. A terminal gets them on its CLASSPATH (see Vars), so javac, java and jshell find them with no
// internet. The newest version whose classes the image's Java can load is the one that is kept: the class file version of its jar is read.
type Maven struct {
	Dir     string // the store (the Maven tool is kept in <Dir>/tools)
	Lookup  MirrorLookup
	Central string // the repository ("" is Maven Central)
	Run     runner
}

const mavenVersion = "3.9.9"

func (m Maven) Name() string { return "maven mirror (Maven Central)" }

func (m Maven) central() string {
	if m.Central != "" {
		return strings.TrimSuffix(m.Central, "/")
	}
	return "https://repo.maven.apache.org/maven2"
}

func splitGA(src string) (group, artifact string, err error) {
	i := strings.IndexByte(src, ':')
	if i <= 0 || i == len(src)-1 || strings.Count(src, ":") != 1 {
		return "", "", fmt.Errorf("%q is not group:artifact", src)
	}
	return src[:i], src[i+1:], nil
}

var mavenSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type metadata struct {
	Versions []string `xml:"versioning>versions>version"`
}

// javaMajor is the class file version of the image's Java: 52 for Java 8, 55 for 11, 65 for 21 (0 when there is no java).
func (m Maven) javaMajor(ctx context.Context) int {
	java := toolPath("java")
	if java == "" {
		return 0
	}
	out, err := orRun(m.Run)(ctx, "", nil, java, "-XshowSettings:properties", "-version")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "java.class.version") {
			f := strings.Split(l, "=")
			if len(f) == 2 {
				return atoiPrefix(strings.TrimSpace(f[1]))
			}
		}
	}
	return 0
}

// classMajor reads the class file version of the first class of a jar.
func classMajor(jar []byte) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(jar), int64(len(jar)))
	if err != nil {
		return 0, err
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".class") && !strings.HasPrefix(f.Name, "META-INF/versions/") && !strings.HasSuffix(f.Name, "module-info.class") {
			rc, err := f.Open()
			if err != nil {
				return 0, err
			}
			head := make([]byte, 8)
			_, err = io.ReadFull(rc, head)
			rc.Close()
			if err != nil || head[0] != 0xCA || head[1] != 0xFE || head[2] != 0xBA || head[3] != 0xBE {
				return 0, fmt.Errorf("%s is not a class file", f.Name)
			}
			return int(head[6])<<8 | int(head[7]), nil
		}
	}
	return 0, nil // a jar with no classes (a bundle of resources): any Java will do
}

func (m Maven) versions(ctx context.Context, group, artifact string) ([]string, error) {
	url := fmt.Sprintf("%s/%s/%s/maven-metadata.xml", m.central(), strings.ReplaceAll(group, ".", "/"), artifact)
	raw, err := download(ctx, url, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("Maven Central does not know %s:%s: %v", group, artifact, err)
	}
	var md metadata
	if xml.Unmarshal(raw, &md) != nil || len(md.Versions) == 0 {
		return nil, fmt.Errorf("Maven Central lists no version of %s:%s", group, artifact)
	}
	vs := stable(md.Versions)
	for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
		vs[i], vs[j] = vs[j], vs[i]
	}
	// metadata lists the oldest first; the newest by number is what matters (versions are mostly in order)
	return vs, nil
}

// jarOK downloads the jar of a version, checks its sha1 and reads its class file version.
func (m Maven) jarOK(ctx context.Context, group, artifact, version string, major int) error {
	base := fmt.Sprintf("%s/%s/%s/%s/%s-%s", m.central(), strings.ReplaceAll(group, ".", "/"), artifact, version, artifact, version)
	jar, err := download(ctx, base+".jar", 200<<20)
	if err != nil {
		return err
	}
	if sum, err := download(ctx, base+".jar.sha1", 1024); err == nil {
		want := strings.ToLower(strings.Fields(string(sum) + " x")[0])
		got := sha1.Sum(jar)
		if want != hex.EncodeToString(got[:]) {
			return fmt.Errorf("the checksum of %s-%s.jar does not match", artifact, version)
		}
	} else {
		return fmt.Errorf("no checksum for %s-%s.jar: %v", artifact, version, err)
	}
	cm, err := classMajor(jar)
	if err != nil {
		return err
	}
	if major > 0 && cm > major {
		return fmt.Errorf("%s %s needs Java class version %d, this server has %d", artifact, version, cm, major)
	}
	return nil
}

// pick finds the newest version whose jar this Java can load (or the pinned one).
func (m Maven) pick(ctx context.Context, src, pin string) (string, error) {
	group, artifact, err := splitGA(src)
	if err != nil {
		return "", err
	}
	if !mavenSafe.MatchString(artifact) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(group) {
		return "", fmt.Errorf("%q is not a usable group:artifact", src)
	}
	major := m.javaMajor(ctx)
	vs := []string{pin}
	bound, bounded := upperBound(pin)
	if bounded {
		pin = ""
	}
	if pin == "" {
		if vs, err = m.versions(ctx, group, artifact); err != nil {
			return "", err
		}
		if bounded {
			vs = olderThan(vs, bound)
		}
	}
	var last error
	for i, v := range vs {
		if i >= maxCandidates {
			break
		}
		if err := m.jarOK(ctx, group, artifact, v, major); err != nil {
			last = err
			logf(ctx, "look up", "warn", "maven: %s:%s %s did not work: %s", group, artifact, v, tail(err.Error(), 200))
			continue
		}
		return v, nil
	}
	return "", fmt.Errorf("no version of %s works with this server's Java: %v", src, last)
}

// tool makes sure Apache Maven is in <Dir>/tools (downloaded once, its sha512 checked) and returns the mvn program.
func (m Maven) tool(ctx context.Context) (string, error) {
	home := filepath.Join(m.Dir, "tools", "maven-"+mavenVersion)
	mvn := filepath.Join(home, "bin", "mvn")
	if _, err := os.Stat(mvn); err == nil {
		return mvn, nil
	}
	url := fmt.Sprintf("https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/%s/apache-maven-%s-bin.tar.gz", mavenVersion, mavenVersion)
	tgz, err := download(ctx, url, 40<<20)
	if err != nil {
		return "", fmt.Errorf("Apache Maven could not be fetched: %v", err)
	}
	sum, err := download(ctx, url+".sha512", 4096)
	if err != nil {
		return "", fmt.Errorf("the checksum of Apache Maven could not be fetched: %v", err)
	}
	got := sha512.Sum512(tgz)
	if strings.ToLower(strings.Fields(string(sum) + " x")[0]) != hex.EncodeToString(got[:]) {
		return "", fmt.Errorf("the checksum of Apache Maven does not match")
	}
	tmp, err := os.MkdirTemp(filepath.Join(m.Dir), "maven-tool-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := untarGz(tgz, tmp); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(home), 0755); err != nil {
		return "", err
	}
	return mvn, moveDir(filepath.Join(tmp, "apache-maven-"+mavenVersion), home)
}

func untarGz(data []byte, dest string) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, h.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) {
			return fmt.Errorf("%s leaves the folder", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0755|0600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, 200<<20))
			f.Close()
			if err != nil {
				return err
			}
		}
	}
}

func (m Maven) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	e, err := lookupEntry(m.Lookup, KindJar, name)
	if err != nil {
		return Resolved{}, err
	}
	if version == "" {
		version = e.Version
	}
	v, err := m.pick(ctx, e.Source(), version)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Version: v, Packages: 1}, nil // the size is known after the install: the dependencies are resolved by Maven
}

// pom is the project that asks Maven to copy the jar and its runtime dependencies.
func pom(group, artifact, version string) string {
	return fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<groupId>openrepl.local</groupId><artifactId>mirror</artifactId><version>1</version>
<dependencies><dependency><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version></dependency></dependencies></project>`, group, artifact, version)
}

func (m Maven) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	e, err := lookupEntry(m.Lookup, KindJar, name)
	if err != nil {
		return err
	}
	if version == "" {
		version = e.Version
	}
	v, err := m.pick(ctx, e.Source(), version)
	if err != nil {
		return err
	}
	group, artifact, _ := splitGA(e.Source())
	mvn, err := m.tool(ctx)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(m.Dir, "maven-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "pom.xml"), []byte(pom(group, artifact, v)), 0644); err != nil {
		return err
	}
	jars := filepath.Join(dest, "jars")
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	args := []string{"-B", "-q", "-C", "-Dmaven.repo.local=" + filepath.Join(tmp, "repo"), "dependency:copy-dependencies",
		"-DoutputDirectory=" + jars, "-DincludeScope=runtime"}
	out, err := orRun(m.Run)(ctx, tmp, []string{"MAVEN_OPTS=-Xmx256m"}, mvn, args...)
	if err != nil {
		return fmt.Errorf("maven: %s", tail(strings.TrimSpace(string(out)), 500))
	}
	entries, _ := os.ReadDir(jars)
	fmt.Fprintf(log, "%s:%s %s: %d jars, %.1f MB\n", group, artifact, v, len(entries), dirMB(jars))
	return os.WriteFile(filepath.Join(dest, "mirror.json"), []byte(fmt.Sprintf("{\"source\":%q,\"version\":%q}\n", e.Source(), v)), 0644)
}

func (m Maven) Search(ctx context.Context, q, kind string) ([]Found, error) { return nil, nil }

// check proves the jars load: javap finds the class the entry names in them.
func (m Maven) check(ctx context.Context, dest string, e Entry) error {
	jars := filepath.Join(dest, "jars")
	if entries, err := os.ReadDir(jars); err != nil || len(entries) == 0 {
		return fmt.Errorf("the bundle has no jars")
	}
	javap := toolPath("javap")
	if javap == "" || e.CheckArg == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if out, err := orRun(m.Run)(ctx, "", nil, javap, "-cp", filepath.Join(jars, "*"), e.CheckArg); err != nil {
		return fmt.Errorf("the class %s is not in the jars: %s", e.CheckArg, tail(strings.TrimSpace(string(out)), 300))
	}
	return nil
}
