// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "os"

func main() {
	os.Stderr.WriteString(`compilebench: stale compilebench

This program is rsc.io/compilebench.
You should be using golang.org/x/tools/cmd/compilebench.
Suggestion:

	rm -r $GOPATH/src/rsc.io/compilebench
	go get -u golang.org/x/tools/cmd/compilebench

`)
	os.Exit(2)
}
