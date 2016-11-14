// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Compilebench benchmarks the speed of the Go compiler.
//
// Usage:
//
//	compilebench [options]
//
// It times the compilation of various packages and prints results in
// the format used by package testing (and expected by rsc.io/benchstat).
//
// The options are:
//
//	-alloc
//		Report allocations.
//
//	-toolexec exe
//		Pass exe to the go command's -toolexec flag.
//
//	-compileflags 'list'
//		Pass the space-separated list of flags to the compilation.
//
//	-count n
//		Run each benchmark n times (default 1).
//
//	-cpuprofile file
//		Write a CPU profile of the compiler to file.
//
//	-memprofile file
//		Write a memory profile of the compiler to file.
//
//	-memprofilerate rate
//		Set runtime.MemProfileRate during compilation.
//
//	-run regexp
//		Only run benchmarks with names matching regexp.
//
//	-torture
//		Include benchmarks that stress the compiler.
//		WARNING: Running these can make your computer unstable.
//
// Although -cpuprofile and -memprofile are intended to write a
// combined profile for all the executed benchmarks to file,
// today they write only the profile for the last benchmark executed.
//
// The default memory profiling rate is one profile sample per 512 kB
// allocated (see ``go doc runtime.MemProfileRate'').
// Lowering the rate (for example, -memprofilerate 64000) produces
// a more fine-grained and therefore accurate profile, but it also incurs
// execution cost. For benchmark comparisons, never use timings
// obtained with a low -memprofilerate option.
//
// Example
//
// Assuming the base version of the compiler has been saved with
// ``toolstash save,'' this sequence compares the old and new compiler:
//
//	compilebench -count 10 -toolexec toolstash > old.txt
//	compilebench -count 10 >new.txt
//	benchstat old.txt new.txt
//
package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"go/build"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	goroot   = runtime.GOROOT()
	compiler string
	runRE    *regexp.Regexp
	is6g     bool
)

var (
	flagAlloc          = flag.Bool("alloc", false, "report allocations")
	flagToolexec       = flag.String("toolexec", "", "pass `exe` to cmd/go's -toolexec flag")
	flagCompilerFlags  = flag.String("compileflags", "", "additional `flags` to pass to compile")
	flagRun            = flag.String("run", "", "run benchmarks matching `regexp`")
	flagCount          = flag.Int("count", 1, "run benchmarks `n` times")
	flagCpuprofile     = flag.String("cpuprofile", "", "write CPU profile to `file`")
	flagMemprofile     = flag.String("memprofile", "", "write memory profile to `file`")
	flagMemprofilerate = flag.Int64("memprofilerate", -1, "set memory profile `rate`")
	flagShort          = flag.Bool("short", false, "skip long-running benchmarks")
	flagTorture        = flag.Bool("torture", false, "include compiler torture tests")
)

var tests = []struct {
	name string
	dir  string
	long bool
}{
	{"BenchmarkTemplate", "html/template", false},
	{"BenchmarkUnicode", "unicode", false},
	{"BenchmarkGoTypes", "go/types", false},
	{"BenchmarkCompiler", "cmd/compile/internal/gc", false},
	{"BenchmarkStdCmd", "", true},
	{"BenchmarkHelloSize", "", false},
	{"BenchmarkCmdGoSize", "", true},
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: compilebench [options]\n")
	fmt.Fprintf(os.Stderr, "options:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("compilebench: ")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 0 {
		usage()
	}

	var exe string
	var baseargs []string
	if *flagToolexec != "" {
		exe = *flagToolexec
	} else {
		exe = "go"
		baseargs = []string{"tool"}
	}
	out, err := exec.Command(exe, append(baseargs, "-n", "compile")...).CombinedOutput()
	if err != nil {
		out, err = exec.Command(exe, append(baseargs, "-n", "6g")...).CombinedOutput()
		is6g = true
		if err != nil {
			out, err = exec.Command(exe, append(baseargs, "tool", "-n", "compile")...).CombinedOutput()
			if *flagToolexec != "" {
				log.Fatalf("%s -n compiler: %v\n%s", *flagToolexec, err, out)
			} else {
				log.Fatalf("go tool -n compiler: %v\n%s", err, out)
			}
		}
	}
	compiler = strings.TrimSpace(string(out))

	if *flagRun != "" {
		r, err := regexp.Compile(*flagRun)
		if err != nil {
			log.Fatalf("invalid -run argument: %v", err)
		}
		runRE = r
	}

	for i := 0; i < *flagCount; i++ {
		for _, tt := range tests {
			if tt.long && *flagShort {
				continue
			}
			if runRE == nil || runRE.MatchString(tt.name) {
				runBuild(tt.name, tt.dir, "")
			}
		}
	}

	if *flagTorture {
		// Assume that this code is where go get would put it.
		testdata := filepath.FromSlash(os.ExpandEnv("$GOPATH/src/rsc.io/compilebench/testdata"))
		files, err := filepath.Glob(filepath.Join(testdata, "*.go.gz"))
		if err != nil {
			log.Fatalf("failed to find torture tests: %v", err)
		}
		if len(files) == 0 {
			log.Fatalf("could not find torture tests; looked in %q", testdata)
		}
		var r *gzip.Reader
		for _, file := range files {
			f, err := os.Open(file)
			if err != nil {
				log.Fatal(err)
			}
			if r == nil {
				r, err = gzip.NewReader(f)
			} else {
				err = r.Reset(f)
			}
			if err != nil {
				log.Fatal(err)
			}
			tmp, err := ioutil.TempFile("", "compilebench")
			if err != nil {
				log.Fatal(err)
			}
			_, err = io.Copy(tmp, r)
			if err != nil {
				log.Fatal(err)
			}
			_, name := path.Split(file)
			name = strings.TrimSuffix(name, ".go.gz")
			name = "Benchmark" + strings.Title(name)
			if runRE == nil || runRE.MatchString(name) {
				runBuild(name, testdata, tmp.Name())
			}
			os.Remove(tmp.Name())
		}
	}
}

func runCmd(name string, cmd *exec.Cmd) {
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("%v: %v\n%s", name, err, out)
		return
	}
	fmt.Printf("%s 1 %d ns/op\n", name, time.Since(start).Nanoseconds())
}

func runStdCmd() {
	args := []string{"build", "-a"}
	if *flagToolexec != "" {
		args = append(args, "-toolexec", *flagToolexec)
	}
	args = append(args, "std", "cmd")
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join(runtime.GOROOT(), "src")
	runCmd("BenchmarkStdCmd", cmd)
}

// path is either a path to a file ("$GOROOT/test/helloworld.go") or a package path ("cmd/go").
func runSize(name, path string) {
	args := []string{"build", "-o", "_compilebenchout_"}
	if *flagToolexec != "" {
		args = append(args, "-toolexec", *flagToolexec)
	}
	args = append(args, path)
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Print(err)
		return
	}
	defer os.Remove("_compilebenchout_")
	info, err := os.Stat("_compilebenchout_")
	if err != nil {
		log.Print(err)
		return
	}
	out, err := exec.Command("size", "_compilebenchout_").CombinedOutput()
	if err != nil {
		log.Printf("size: %v\n%s", err, out)
		return
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		log.Printf("not enough output from size: %s", out)
		return
	}
	f := strings.Fields(lines[1])
	if strings.HasPrefix(lines[0], "__TEXT") && len(f) >= 2 { // OS X
		fmt.Printf("%s 1 %s text-bytes %s data-bytes %v exe-bytes\n", name, f[0], f[1], info.Size())
	} else if strings.Contains(lines[0], "bss") && len(f) >= 3 {
		fmt.Printf("%s 1 %s text-bytes %s data-bytes %s bss-bytes %v exe-bytes\n", name, f[0], f[1], f[2], info.Size())
	}
}

func runBuild(name, dir, file string) {
	switch name {
	case "BenchmarkStdCmd":
		runStdCmd()
		return
	case "BenchmarkCmdGoSize":
		runSize("BenchmarkCmdGoSize", "cmd/go")
		return
	case "BenchmarkHelloSize":
		runSize("BenchmarkHelloSize", filepath.Join(runtime.GOROOT(), "test/helloworld.go"))
		return
	}

	var files []string
	var pkgdir string
	switch {
	case file != "":
		files = []string{file}
		pkgdir = dir
	case dir != "":
		pkg, err := build.Import(dir, ".", 0)
		if err != nil {
			log.Print(err)
			return
		}
		files = pkg.GoFiles
		pkgdir = pkg.Dir
	default:
		log.Fatal("internal error: dir or file must be set")
	}
	args := []string{"-o", "_compilebench_.o"}
	if is6g {
		*flagMemprofilerate = -1
		*flagAlloc = false
		*flagCpuprofile = ""
		*flagMemprofile = ""
	}
	if *flagMemprofilerate >= 0 {
		args = append(args, "-memprofilerate", fmt.Sprint(*flagMemprofilerate))
	}
	args = append(args, strings.Fields(*flagCompilerFlags)...)
	if *flagAlloc || *flagCpuprofile != "" || *flagMemprofile != "" {
		if *flagAlloc || *flagMemprofile != "" {
			args = append(args, "-memprofile", "_compilebench_.memprof")
		}
		if *flagCpuprofile != "" {
			args = append(args, "-cpuprofile", "_compilebench_.cpuprof")
		}
	}
	args = append(args, files...)
	cmd := exec.Command(compiler, args...)
	cmd.Dir = pkgdir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		log.Printf("%v: %v", name, err)
		return
	}
	end := time.Now()

	var allocs, bytes int64
	if *flagAlloc || *flagMemprofile != "" {
		out, err := ioutil.ReadFile(pkgdir + "/_compilebench_.memprof")
		if err != nil {
			log.Print("cannot find memory profile after compilation")
		}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 4 || f[0] != "#" || f[2] != "=" {
				continue
			}
			val, err := strconv.ParseInt(f[3], 0, 64)
			if err != nil {
				continue
			}
			switch f[1] {
			case "TotalAlloc":
				bytes = val
			case "Mallocs":
				allocs = val
			}
		}

		if *flagMemprofile != "" {
			if err := ioutil.WriteFile(*flagMemprofile, out, 0666); err != nil {
				log.Print(err)
			}
		}
		os.Remove(pkgdir + "/_compilebench_.memprof")
	}

	if *flagCpuprofile != "" {
		out, err := ioutil.ReadFile(pkgdir + "/_compilebench_.cpuprof")
		if err != nil {
			log.Print(err)
		}
		if err := ioutil.WriteFile(*flagCpuprofile, out, 0666); err != nil {
			log.Print(err)
		}
		os.Remove(pkgdir + "/_compilebench_.cpuprof")
	}

	wallns := end.Sub(start).Nanoseconds()
	userns := cmd.ProcessState.UserTime().Nanoseconds()

	if *flagAlloc {
		fmt.Printf("%s 1 %d ns/op %d user-ns/op %d B/op %d allocs/op\n", name, wallns, userns, bytes, allocs)
	} else {
		fmt.Printf("%s 1 %d ns/op %d user-ns/op\n", name, wallns, userns)
	}

	os.Remove(pkgdir + "/_compilebench_.o")
}
