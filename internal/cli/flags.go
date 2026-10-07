package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

func newFlagSet(name string, stdout io.Writer, usage string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stdout)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), usage)
		flags.PrintDefaults()
	}
	return flags
}

// parseFlags keeps help on stdout and leaves error reporting to RunVersion.
// Buffer help so a failed write is also reported as an operational failure.
func parseFlags(flags *flag.FlagSet, args []string) error {
	output, usage := flags.Output(), flags.Usage
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	err := parseInterspersed(flags, args)
	flags.SetOutput(output)
	flags.Usage = usage
	if errors.Is(err, flag.ErrHelp) {
		var help bytes.Buffer
		flags.SetOutput(&help)
		flags.Usage()
		flags.SetOutput(output)
		if _, err := io.Copy(output, &help); err != nil {
			return err
		}
		return flag.ErrHelp
	}
	if err != nil {
		return usageError{fmt.Sprintf("%s: %v; run aarde %s --help", flags.Name(), err, flags.Name())}
	}
	return nil
}

// Accept flags before or after positional arguments. A standalone -- ends
// option parsing, allowing paths beginning with a dash.
func parseInterspersed(flags *flag.FlagSet, args []string) error {
	var options, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		options = append(options, arg)
		name, _, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		f := flags.Lookup(name)
		if f == nil {
			return flags.Parse(options)
		}
		boolean, isBoolean := f.Value.(interface{ IsBoolFlag() bool })
		if !hasValue && !(isBoolean && boolean.IsBoolFlag()) {
			if i+1 == len(args) {
				// Do not let the synthetic separator become a missing flag value.
				return flags.Parse(options)
			}
			i++
			options = append(options, args[i])
		}
	}
	return flags.Parse(append(append(options, "--"), positionals...))
}

func requireArguments(flags *flag.FlagSet, names ...string) error {
	if flags.NArg() != len(names) {
		return usageError{fmt.Sprintf("%s expects %d positional argument(s); run aarde %s --help", flags.Name(), len(names), flags.Name())}
	}
	for i, name := range names {
		if strings.TrimSpace(flags.Arg(i)) == "" {
			return usageError{name + " is required"}
		}
	}
	return nil
}
