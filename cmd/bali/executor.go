package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/balibuild/bali/pack"
	"github.com/balibuild/bali/utilities"
)

// Executor

// Executor todo
type Executor struct {
	de          *utilities.Derivator
	target      string // os
	arch        string
	out         string
	workdir     string
	destination string
	makezip     bool
	makepack    bool
	norename    bool
	cleanup     bool
	environ     []string // initialize environment
	binaries    []string
	linkmap     map[string]string
	bm          Project
	suffix      string
}

func resolveBuildID(cwd string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err == nil {
		commitid := strings.TrimSpace(string(out))
		DbgPrint("BUILD_COMMIT: '%s'", commitid)
		return commitid
	}
	return "None"
}

func resolveBranch(cwd string) string {
	cmd := exec.Command("git", "symbolic-ref", "HEAD")
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err == nil {
		branch := strings.TrimSpace(strings.TrimPrefix(string(out), "refs/heads/"))
		DbgPrint("BUILD_BRANCH: '%s'", branch)
		return branch
	}
	return "None"
}

func resolveGoVersion() string {
	cmd := exec.Command("go", "version")
	if out, err := cmd.CombinedOutput(); err == nil {
		goversion := strings.TrimPrefix(strings.TrimSpace(string(out)), "go version ")
		DbgPrint("Go Version: '%s'", goversion)
		return goversion
	}
	return "None"
}

func resolveDistSupport(target, arch string) bool {
	cmd := exec.Command("go", "tool", "dist", "list")
	if out, err := cmd.CombinedOutput(); err == nil {
		str := utilities.StrCat(target, "/", arch)
		if strings.Contains(string(out), str) {
			return true
		}
	}
	return false
}

// Initialize todo
func (be *Executor) Initialize() error {
	bali := filepath.Join(be.workdir, "bali.json")
	if err := LoadMetadata(bali, &be.bm); err != nil {
		return err
	}
	if len(be.bm.Version) == 0 {
		be.bm.Version = "0.0.1"
	}
	be.de = utilities.NewDerivator()
	// Respect environmental variable settings
	if len(be.target) == 0 {
		if be.target = os.Getenv("GOOS"); len(be.target) == 0 {
			be.target = runtime.GOOS
		}
	}
	if len(be.arch) == 0 {
		if be.arch = os.Getenv("GOARCH"); len(be.arch) == 0 {
			be.arch = runtime.GOARCH
		}
	}
	if !resolveDistSupport(be.target, be.arch) {
		return utilities.ErrorCat("unsupported GOOS/GOARCH pair ", be.target, "/", be.arch)
	}
	_ = be.de.Append("BUILD_COMMIT", resolveBuildID(be.workdir))
	_ = be.de.Append("BUILD_BRANCH", resolveBranch(be.workdir))
	_ = be.de.Append("BUILD_GOVERSION", resolveGoVersion())
	_ = be.de.Append("BUILD_VERSION", "0.0.1")
	t := time.Now()
	_ = be.de.Append("BUILD_TIME", t.Format(time.RFC3339))
	osenv := os.Environ()
	be.environ = make([]string, 0, len(osenv)+3)
	for _, e := range osenv {
		// remove old env
		if !strings.HasPrefix(e, "GOOS=") && !strings.HasPrefix(e, "GOARCH=") {
			be.environ = append(be.environ, e)
		}
	}
	be.environ = append(be.environ, utilities.StrCat("GOOS=", be.target))
	be.environ = append(be.environ, utilities.StrCat("GOARCH=", be.arch))
	be.linkmap = make(map[string]string)
	if be.target == "windows" {
		be.suffix = ".exe"
	}
	return nil
}

// UpdateNow todo
func (be *Executor) UpdateNow(version string) {
	_ = be.de.Append("BUILD_VERSION", version)
	t := time.Now()
	_ = be.de.Append("BUILD_TIME", t.Format(time.RFC3339))
}

// ExpandEnv todo
func (be *Executor) ExpandEnv(s string) string {
	return be.de.ExpandEnv(s)
}

// TargetName todo
func (be *Executor) TargetName(name string) string {
	if be.norename {
		return name
	}
	return utilities.StrCat(name, ".new")
}

// FileName todo
func (be *Executor) FileName(file *File) string {
	if be.norename || file.NoRename {
		return file.Base()
	}
	return utilities.StrCat(file.Base(), ".template")
}

// AddSuffix todo
func (be *Executor) AddSuffix(name string) string {
	if len(be.suffix) == 0 {
		return name
	}
	if strings.HasSuffix(name, be.suffix) {
		return name
	}
	return utilities.StrCat(name, be.suffix)
}

// BinaryName todo
func (be *Executor) BinaryName(dir, name string) string {
	if len(name) == 0 {
		return utilities.StrCat(filepath.Base(dir), be.suffix)
	}
	return utilities.StrCat(name, be.suffix)
}

// PathInArchive todo
func (be *Executor) PathInArchive(destination string) string {
	if be.makepack {
		return destination
	}
	return filepath.Join(utilities.StrCat(be.bm.Name, "-", be.target, "-", be.arch, "-", be.bm.Version), destination)
}

// Build todo
func (be *Executor) Build() error {
	if err := be.bm.FileConfigure(be.workdir, be.out); err != nil {
		return err
	}
	for _, d := range be.bm.Dirs {
		wd := filepath.Join(be.workdir, d)
		if err := be.Compile(wd); err != nil {
			fmt.Fprintf(os.Stderr, "bali compile: \x1b[31m%s\x1b[0m\n", err)
			return err
		}
	}
	return nil
}

// Compress todo
func (be *Executor) Compress() error {
	if !utilities.PathDirExists(be.destination) {
		if err := os.MkdirAll(be.destination, 0755); err != nil {
			return err
		}
	}
	var outfile string
	var err error
	var fd *os.File
	var pk pack.Packer
	if be.target == "windows" {
		outfile = filepath.Join(be.destination, utilities.StrCat(be.bm.Name, "-", be.target, "-", be.arch, "-", be.bm.Version, ".zip"))
		fd, err = os.Create(outfile)
		if err != nil {
			return err
		}
		pk = pack.NewZipPacker(fd)
	} else {
		outfile = filepath.Join(be.destination, utilities.StrCat(be.bm.Name, "-", be.target, "-", be.arch, "-", be.bm.Version, ".tar.gz"))
		fd, err = os.Create(outfile)
		if err != nil {
			return err
		}
		pk = pack.NewTargzPacker(fd)
	}
	defer fd.Close()
	defer pk.Close()
	for _, b := range be.binaries {
		rel, err := filepath.Rel(be.out, b)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "compress target \x1b[32m%s\x1b[0m\n", rel)
		if err := pk.AddFileEx(b, be.PathInArchive(rel), true); err != nil {
			return err
		}
	}
	for name, lnkName := range be.linkmap {
		nameInArchive := be.PathInArchive(name)
		fmt.Fprintf(os.Stderr, "compress link \x1b[32m%s --> %s\x1b[0m\n", nameInArchive, lnkName)
		if err := pk.AddTargetLink(nameInArchive, lnkName); err != nil {
			return err
		}
	}
	for _, f := range be.bm.Files {
		file := filepath.Join(be.workdir, f.Path)
		rel := filepath.Join(f.Destination, f.Base())
		fmt.Fprintf(os.Stderr, "compress profile \x1b[32m%s\x1b[0m\n", f.Path)
		if err := pk.AddFile(file, be.PathInArchive(rel)); err != nil {
			return err
		}
	}
	return nil
}

// PackWin todo
func (be *Executor) PackWin() error {
	fmt.Fprintf(os.Stderr, "Windows installation package is not yet supported\n")
	return nil
}

// PackUNIX todo
func (be *Executor) PackUNIX() error {
	outfilename := utilities.StrCat(be.bm.Name, "-", be.target, "-", be.arch, "-", be.bm.Version, ".sh")
	outfile := filepath.Join(be.destination, outfilename)
	fd, err := pack.OpenFile(outfile)
	if err != nil {
		return err
	}
	defer fd.Close()
	pk := pack.NewTargzPacker(fd)
	defer pk.Close()
	var rw pack.RespondWriter
	// bali post install script
	if len(be.bm.Respond) != 0 {
		if !utilities.PathExists(be.bm.Respond) {
			return utilities.ErrorCat("respond file ", be.bm.Respond, " not found")
		}
		if err := pk.AddFileEx(be.bm.Respond, "bali_post_install", true); err != nil {
			return err
		}
	} else if !be.norename {
		if err := rw.Initialize(); err != nil {
			return err
		}
		if err := rw.WriteBase(); err != nil {
			return err
		}
	}

	for _, s := range be.binaries {
		rel, err := filepath.Rel(be.out, s)
		if err != nil {
			_ = rw.Close()
			return err
		}
		fmt.Fprintf(os.Stderr, "compress target \x1b[32m%s\x1b[0m\n", rel)
		nameInArchive := be.PathInArchive(rel)
		if !be.norename {
			nameInArchive = utilities.StrCat(nameInArchive, ".new")
		}
		if err := pk.AddFileEx(s, nameInArchive, true); err != nil {
			_ = rw.Close()
			return err
		}
		DbgPrint("Add target %s", rel)
		_ = rw.AddTarget(nameInArchive)
	}
	for name, lnkName := range be.linkmap {
		nameInArchive := be.PathInArchive(name)
		fmt.Fprintf(os.Stderr, "compress link \x1b[32m%s --> %s\x1b[0m\n", nameInArchive, lnkName)
		if err := pk.AddTargetLink(nameInArchive, lnkName); err != nil {
			_ = rw.Close()
			return err
		}
	}
	for _, f := range be.bm.Files {
		file := filepath.Join(be.workdir, f.Path)
		rel := filepath.Join(f.Destination, f.Base())
		fmt.Fprintf(os.Stderr, "compress profile \x1b[32m%s\x1b[0m\n", rel)
		if be.norename || f.NoRename {
			if err := pk.AddFile(file, be.PathInArchive(rel)); err != nil {
				_ = rw.Close()
				return err
			}
			DbgPrint("Add profile %s (no rename)", f.Path)
		} else {
			nameInArchive := utilities.StrCat(be.PathInArchive(rel), ".template")
			if err := pk.AddFile(file, nameInArchive); err != nil {
				_ = rw.Close()
				return err
			}
			DbgPrint("Add profile %s", f.Path)
			_ = rw.AddProfile(nameInArchive)
		}
	}
	_ = rw.Close()
	if len(rw.Path) != 0 {
		DbgPrint("Add post install script %s", rw.Path)
		if err := pk.AddFileEx(rw.Path, pack.RespondName, true); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "create \x1b[32m%s\x1b[0m done\n", outfile)
	fmt.Fprintf(os.Stderr, "Your can run '\x1b[32m./%s --prefix=/path/to/%s\x1b[0m' to install %s\n", outfilename, be.bm.Name, be.bm.Name)
	return nil
}

// Pack todo
func (be *Executor) Pack() error {
	if !utilities.PathDirExists(be.destination) {
		if err := os.MkdirAll(be.destination, 0755); err != nil {
			return err
		}
	}
	return be.PackUNIX()
}
