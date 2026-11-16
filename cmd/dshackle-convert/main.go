// dshackle-convert turns a dshackle config file into a nodecore config file.
//
//	make convert-dshackle IN=/path/dshackle.yaml OUT=/path/nodecore.yml
//	make convert-dshackle IN=/path/dshackle.yaml OUT=/path/nodecore.yml FORCE=1
//
// The converted config is written to OUT. A report of everything that was not carried over
// as is goes to stderr: skipped upstreams, settings that need a manual step, and settings
// that have no nodecore equivalent. Only the given file is read; files named in
// cluster.include are not followed.
//
// The output is checked with nodecore's own config validation before it is written, so the
// exit code is 0 only when a usable file was produced.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog"
)

const outputFileMode = 0o600 // the file can hold credentials

func main() {
	// nodecore's config code logs its own warnings; they would mix into the report
	zerolog.SetGlobalLevel(zerolog.Disabled)
	// connector validation checks each connector against the chain's method spec
	if err := specs.NewMethodSpecLoader().Load(); err != nil {
		os.Exit(fail(os.Stderr, "unable to load method specs: %v", err))
	}
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run executes one conversion and returns the process exit code. The method specs must be
// loaded before it is called.
func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("dshackle-convert", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inPath := flags.String("in", "", "path to the dshackle config file")
	outPath := flags.String("out", "", "path of the nodecore config file to write")
	force := flags.Bool("force", false, "overwrite the output file if it exists")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *inPath == "" || *outPath == "" {
		flags.Usage()
		return fail(stderr, "both -in and -out are required")
	}
	if _, err := os.Stat(*outPath); err == nil && !*force {
		return fail(stderr, "%s already exists; pass -force to overwrite it", *outPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(stderr, "%v", err)
	}

	input, err := os.ReadFile(*inPath)
	if err != nil {
		return fail(stderr, "%v", err)
	}

	converted, err := convert(input)
	if err != nil {
		converted.report.write(stderr, converted.converted, converted.total, "(nothing written)")
		return fail(stderr, "%v", err)
	}
	data, err := marshalConfig(converted.config)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if err := os.WriteFile(*outPath, data, outputFileMode); err != nil {
		return fail(stderr, "%v", err)
	}
	// WriteFile keeps the mode of a file that already existed
	if err := os.Chmod(*outPath, outputFileMode); err != nil {
		return fail(stderr, "%v", err)
	}
	converted.report.write(stderr, converted.converted, converted.total, *outPath)
	return 0
}

// fail prints one error line and returns the failure exit code.
func fail(stderr io.Writer, format string, args ...any) int {
	_, _ = fmt.Fprintf(stderr, "error: "+format+"\n", args...)
	return 1
}
