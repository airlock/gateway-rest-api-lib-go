package exutil

import (
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"slices"
	"strings"
)

// CommonFlags holds the flags every example command shares.
type CommonFlags struct {
	Host      string
	Port      int
	Key       string
	Debug     bool
	Comment   string
	AssumeYes bool
	Activate  bool
}

// RegisterCommonFlags declares the connection and change flags on fs;
// defaultComment is the command's default for -c.
func RegisterCommonFlags(fs *flag.FlagSet, defaultComment string) *CommonFlags {
	c := RegisterConnectFlags(fs)
	c.RegisterChangeFlags(fs, defaultComment)
	return c
}

// RegisterConnectFlags declares only the connection flags on fs, for read-only commands.
func RegisterConnectFlags(fs *flag.FlagSet) *CommonFlags {
	c := &CommonFlags{}
	fs.StringVar(&c.Host, "g", "", "Airlock Gateway hostname (or set GATEWAY_HOST)")
	fs.IntVar(&c.Port, "p", 443, "gateway HTTPS port")
	fs.StringVar(&c.Key, "k", "", "REST API key for Airlock Gateway")
	fs.BoolVar(&c.Debug, "debug", false, "print every HTTP request and response to stderr")
	return c
}

// RegisterChangeFlags declares the save/activate flags on fs.
func (c *CommonFlags) RegisterChangeFlags(fs *flag.FlagSet, defaultComment string) {
	fs.StringVar(&c.Comment, "c", defaultComment, "comment for the configuration change")
	fs.BoolVar(&c.AssumeYes, "y", false, "automatically confirm without prompting")
	fs.BoolVar(&c.Activate, "activate", false, "activate the configuration (default: save only)")
}

// Setup connects using the common flags. See the package-level Setup.
func (c *CommonFlags) Setup() *Session { return Setup(c.Host, c.Key, c.Port, c.Debug) }

// Finish validates and then saves or activates, per -c, -y and --activate.
func (c *CommonFlags) Finish(s *Session) {
	s.ActivateOrSave(c.Comment, c.AssumeYes, c.Activate)
}

// Subcommand returns os.Args[1], which must be one of names, and a FlagSet for the remaining arguments.
func Subcommand(cmd string, names ...string) (string, *flag.FlagSet) {
	if len(os.Args) < 2 || !slices.Contains(names, os.Args[1]) {
		fmt.Fprintf(os.Stderr, "usage: %s <%s> [flags]\n", cmd, strings.Join(names, "|"))
		os.Exit(2)
	}
	op := os.Args[1]
	return op, flag.NewFlagSet(cmd+" "+op, flag.ExitOnError)
}

// MustRegexp compiles the required regex flag -name of fs; it exits if the
// value is empty or invalid.
func MustRegexp(fs *flag.FlagSet, name, expr string) *regexp.Regexp {
	if expr == "" {
		fmt.Fprintf(os.Stderr, "-%s is required\n", name)
		fs.Usage()
		os.Exit(2)
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		log.Fatalf("invalid -%s regex: %v", name, err)
	}
	return re
}
