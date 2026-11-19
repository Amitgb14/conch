// Command conch-sandbox makes, starts, stops and deletes conch's sandboxes
// for tools that are not conch — herdr, the herdr app, Orca — which reach
// a machine over ssh and install their own server there. It is built from
// conch's own provider code and nothing else of conch: no server, no
// terminal interface, no agents, and it never installs anything inside a
// sandbox. It shares conch's home (config.Dir()): the same keys, provider
// files and ssh key, so somebody with both sees one set of sandboxes.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/sshkey"
	"github.com/charmbracelet/x/term"
)

// version is set by the build (-ldflags "-X main.version=…"); the launcher
// compares it with the version a plugin pins.
var version = "dev"

const usage = `conch-sandbox makes cloud sandboxes with conch's provider files, for tools
that reach a machine over ssh (herdr, the herdr app, Orca). It never
installs anything inside a sandbox.

Usage:
  conch-sandbox providers [--json]
  conch-sandbox key set PROVIDER            the API key, read from stdin
  conch-sandbox provider install NAME       a provider's file, from the registry
  conch-sandbox create --provider P [--name N] [--repo URL [--branch B] [--ref SHA]]
                       [--region R] [--size S] [--image I] [--tool T] [--yes]
                       [--json | --format orca] [--from-orca]
  conch-sandbox get    SANDBOX [--json]
  conch-sandbox start  SANDBOX [--json | --format orca]
  conch-sandbox stop   SANDBOX [--json]
  conch-sandbox rm     SANDBOX [--json]
  conch-sandbox ssh-alias SANDBOX [--remove] [--yes] [--json]
  conch-sandbox orphans --tool herdr|herdr-app [--json] [--notify]
  conch-sandbox pick   --tool herdr           questions on the terminal, for herdr's New sandbox
  conch-sandbox version
  conch-sandbox VERB --help                 more about one of them

SANDBOX is one of: --provider P ID; --machine ALIAS, one of conch-sandbox's
ssh aliases; --from-orca (ORCA_VM_INSTANCE_ID, with --provider); or
--from-herdr (the machine of the herdr workspace a plugin action runs in).

With --json a command prints one JSON object on stdout ("schema": 1) and
its progress on stderr. Keys, provider files and the ssh key are conch's:
` + "`$CONCH_HOME`" + `, else ~/.config/conch. A provider file is installed with
conch-sandbox provider install NAME (or when a command first names it),
from the provider registry's signed index; or by hand in the providers
folder. providers lists those the registry has as well, installed false.

create writes an ssh alias, conch-PROVIDER-ID, to $CONCH_HOME/ssh/sandboxes,
which ssh and the tools read once ~/.ssh/config includes it: conch-sandbox
asks before adding that line, or adds it with --yes. start writes a fresh
token into an alias that has one; rm takes the alias out.

--tool says who a sandbox is for: herdr, herdr-app, orca (Orca's flags
say so themselves) or cli, the default. A tool's sandbox has its home as
project_root when nothing is cloned, and is only ever that tool's orphan.
With --ref the branch need not be pushed, only the commit; a branch alone
must be. hc is $HC_BIN_PATH, else PATH's; herdr is $HERDR_BIN_PATH, else
PATH's.
`

// app is one run: its streams, so a test can give it its own.
type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// terminal says stdin is a person at a terminal rather than a pipe.
	terminal bool
	// readSecret reads a line from the terminal without echoing it.
	readSecret func() (string, error)
	// wait bounds making or starting a sandbox.
	wait time.Duration
	// line is the command run, after the program's name.
	line []string
	// reader reads stdin a line at a time, for every question asked: one
	// reader, or the first question's would keep the next one's answer.
	reader *bufio.Reader
	// retry is how long to wait between tries of a sandbox's ssh that is
	// not answering yet.
	retry time.Duration
	// grace is how long ssh may keep refusing for good — a key it was
	// not given, a name it cannot find — before that is the answer: a
	// key just handed over may take a moment to arrive. 0 is a minute.
	grace time.Duration
	// dir is where pick looks for the repository to check out; "" is the
	// working directory.
	dir string
}

// readLine reads an answer from stdin, without its newline. At the end of
// the input it is what was there, and io.EOF only when nothing was.
func (a *app) readLine() (string, error) {
	if a.reader == nil {
		a.reader = bufio.NewReader(a.stdin)
	}
	s, err := a.reader.ReadString('\n')
	if err == io.EOF && s != "" {
		err = nil
	}
	return strings.TrimRight(s, "\r\n"), err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, wait: 10 * time.Minute, retry: 2 * time.Second,
		terminal: term.IsTerminal(os.Stdin.Fd()),
		readSecret: func() (string, error) {
			b, err := term.ReadPassword(os.Stdin.Fd())
			return string(b), err
		}}
	os.Exit(a.run(ctx, os.Args[1:]))
}

// A provider file whose create body carries {public_key} is given the key
// conch reaches machines with, as conch does.
func init() { sshkey.Use(nil) }

// errUsage is a command given wrongly; its message is the usage to show.
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

// run is the program, returning its exit status: 0 done, 1 failed, 2 a
// command given wrongly.
func (a *app) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage)
		return 2
	}
	verb, rest := args[0], args[1:]
	a.line = args
	// A provider named and not installed is fetched from the registry.
	installer = func(name string) error { return a.install(ctx, name) }
	defer func() { installer = nil }()
	var err error
	switch verb {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(a.stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(a.stdout, version)
		return 0
	case "providers":
		err = a.providers(ctx, rest)
	case "key":
		err = a.key(rest)
	case "provider":
		err = a.provider(ctx, rest)
	case "create":
		err = a.create(ctx, rest)
	case "get":
		err = a.get(ctx, rest)
	case "start":
		err = a.start(ctx, rest)
	case "stop":
		err = a.stop(ctx, rest)
	case "rm":
		err = a.rm(ctx, rest)
	case "ssh-alias":
		err = a.sshAlias(ctx, rest)
	case "orphans":
		err = a.orphans(ctx, rest)
	case "pick":
		err = a.pick(ctx, rest)
	default:
		fmt.Fprintf(a.stderr, "conch-sandbox: unknown command %q\n\n%s", verb, usage)
		return 2
	}
	var u errUsage
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(a.stderr, "conch-sandbox %s: %s\n", verb, u.msg)
		return 2
	}
	fmt.Fprintf(a.stderr, "conch-sandbox %s: %v\n", verb, err)
	return 1
}

// command is one verb's flags, with the usage --help prints.
type command struct {
	*flag.FlagSet
	a     *app
	usage string
}

func (a *app) command(name, usage string) *command {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // said by parse, to the stream that fits
	return &command{FlagSet: fs, a: a, usage: usage}
}

// parse reads flags wherever they are among the arguments — a tool puts
// the ID before --json as often as after — and returns the rest. --help
// prints the usage on stdout and returns flag.ErrHelp.
func (c *command) parse(args []string) ([]string, error) {
	var rest []string
	for {
		if err := c.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintf(c.a.stdout, "Usage: %s\n", c.usage)
				c.SetOutput(c.a.stdout)
				c.PrintDefaults()
				return nil, err
			}
			return nil, errUsage{fmt.Sprintf("%v\nusage: %s", err, c.usage)}
		}
		args = c.Args()
		if len(args) == 0 {
			return rest, nil
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

// wrong is the command given wrongly, with its usage.
func (c *command) wrong(why string) error {
	return errUsage{why + "\nusage: " + c.usage}
}

// checkID refuses what cannot be a sandbox's ID: it goes into the
// provider's paths and, later, an ssh alias.
// An ID is also part of an ssh alias, where * ? ! and , are patterns.
func checkID(id string) error {
	if !idChars.MatchString(id) || len(id) > 128 {
		return fmt.Errorf("%q is not a sandbox ID", id)
	}
	return nil
}

var idChars = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
