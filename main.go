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
// Each compilation actually runs twice, once for timing and again to
// gather allocation statistics.
//
// The options are:
//
//	-compile exe
//		Use exe as the path to the cmd/compile binary.
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
//	-run regexp
//		Only run benchmarks with names matching regexp.
//
// Although -cpuprofile and -memprofile are intended to write a
// combined profile for all the executed benchmarks to file,
// today they write only the profile for the last benchmark executed.
//
package main

import (
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
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
)

var (
	flagCompiler      = flag.String("compile", "", "use `exe` as the cmd/compile binary")
	flagCompilerFlags = flag.String("compileflags", "", "additional `flags` to pass to compile")
	flagRun           = flag.String("run", "", "run benchmarks matching `regexp`")
	flagCount         = flag.Int("count", 1, "run benchmarks `n` times")
	flagCpuprofile    = flag.String("cpuprofile", "", "write CPU profile to `file`")
	flagMemprofile    = flag.String("memprofile", "", "write memory profile to `file`")
)

var tests = []struct {
	name string
	dir  string
}{
	{"BenchmarkTemplate", "html/template"},
	{"BenchmarkGoTypes", "go/types"},
	{"BenchmarkCompiler", "cmd/compile/internal/gc"},
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

	compiler = *flagCompiler
	if compiler == "" {
		out, err := exec.Command("go", "tool", "-n", "compile").CombinedOutput()
		if err != nil {
			log.Fatalf("go tool -n compiler: %v\n%s", err, out)
		}
		compiler = strings.TrimSpace(string(out))
	}

	if *flagRun != "" {
		r, err := regexp.Compile(*flagRun)
		if err != nil {
			log.Fatalf("invalid -run argument: %v", err)
		}
		runRE = r
	}

	for i := 0; i < *flagCount; i++ {
		for _, tt := range tests {
			if runRE == nil || runRE.MatchString(tt.name) {
				runBuild(tt.name, tt.dir)
			}
		}
	}
}

func runCmd(name string, cmd *exec.Cmd) {
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %v\n%s", name, err, out)
	}
	fmt.Printf("Benchmark%s 1 %d ns/op\n", name, time.Since(start).Nanoseconds())
}

func runBuild(name, dir string) {
	pkg, err := build.Import(dir, ".", 0)
	if err != nil {
		log.Fatal(err)
	}
	args := []string{"-o", "_compilebench_.o"}
	args = append(args, strings.Fields(*flagCompilerFlags)...)
	args = append(args, pkg.GoFiles...)
	cmd := exec.Command(compiler, args...)
	cmd.Dir = pkg.Dir
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %v\n%s", name, err, out)
	}
	end := time.Now()

	args = []string{"-o", "_compilebench_.o"}
	args = append(args, strings.Fields(*flagCompilerFlags)...)
	args = append(args, "-memprofile", "_compilebench_.memprof", "-memprofilerate", fmt.Sprint(64*1024))
	if *flagCpuprofile != "" {
		args = append(args, "-cpuprofile", "_compilebench_.cpuprof")
	}
	args = append(args, pkg.GoFiles...)
	cmd = exec.Command(compiler, args...)
	cmd.Dir = pkg.Dir
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("%v: %v\n%s", name, err, out)
	}

	out, err = ioutil.ReadFile(pkg.Dir + "/_compilebench_.memprof")
	if err != nil {
		log.Fatal("cannot find memory profile after compilation")
	}
	var allocs, bytes int64
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

	fmt.Printf("Benchmark%s 1 %d ns/op %d B/op %d allocs/op\n", name, end.Sub(start).Nanoseconds(), bytes, allocs)

	if *flagMemprofile != "" {
		if err := ioutil.WriteFile(*flagMemprofile, out, 0666); err != nil {
			log.Fatal(err)
		}
	}

	if *flagCpuprofile != "" {
		out, err := ioutil.ReadFile(pkg.Dir + "/_compilebench_.cpuprof")
		if err != nil {
			log.Fatal(err)
		}
		if err := ioutil.WriteFile(*flagCpuprofile, out, 0666); err != nil {
			log.Fatal(err)
		}
		os.Remove(pkg.Dir + "/_compilebench_.cpuprof")
	}

	os.Remove(pkg.Dir + "/_compilebench_.o")
	os.Remove(pkg.Dir + "/_compilebench_.memprof")
}
