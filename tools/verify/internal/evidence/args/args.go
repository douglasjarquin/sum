// Package args parses one command's arguments: options in any position, repeated options, --name=value, required options, choices, and
// either named positionals or a trailing remainder handed to the next parser.
package args

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Option is one --name. Values is how many arguments follow it; zero makes it a switch.
type Option struct {
	Name     string
	Values   int
	Int      bool
	Choices  []string
	Required bool
	Default  string
}

// Spec is what one command accepts.
type Spec struct {
	Options     []Option
	Positionals []string // Names of the required positional arguments.
	Trailing    bool     // Stop at the first positional or "--" and return everything from there as Rest.
}

type Parsed struct {
	spec        Spec
	given       map[string][]string
	Positionals []string
	Rest        []string
}

// ErrHelp reports that -h or --help was given.
var ErrHelp = errors.New("help requested")

// UsageError is an invocation the command does not accept.
type UsageError string

func (e UsageError) Error() string { return string(e) }

var negativeNumber = regexp.MustCompile(`^-(\d+|\d*\.\d+)$`)

func isOption(token string) bool {
	return len(token) > 1 && token[0] == '-' && !negativeNumber.MatchString(token)
}

func (s Spec) option(name string) *Option {
	for i := range s.Options {
		if s.Options[i].Name == name {
			return &s.Options[i]
		}
	}
	return nil
}

func Parse(spec Spec, argv []string) (Parsed, error) {
	parsed := Parsed{spec: spec, given: map[string][]string{}}
	for i := 0; i < len(argv); i++ {
		token := argv[i]
		if token == "--" {
			if spec.Trailing {
				parsed.Rest = argv[i:]
			} else {
				parsed.Positionals = append(parsed.Positionals, argv[i+1:]...)
			}
			break
		}
		if !isOption(token) {
			if spec.Trailing {
				parsed.Rest = argv[i:]
				break
			}
			parsed.Positionals = append(parsed.Positionals, token)
			continue
		}
		if token == "-h" || token == "--help" {
			return parsed, ErrHelp
		}
		name, inline, hasInline := strings.Cut(token, "=")
		opt := spec.option(name)
		if opt == nil {
			return parsed, UsageError("unrecognized arguments: " + token)
		}
		if opt.Values == 0 {
			if hasInline {
				return parsed, UsageError(fmt.Sprintf("argument %s: ignored explicit argument '%s'", name, inline))
			}
			parsed.given[name] = []string{"true"}
			continue
		}
		var values []string
		if hasInline {
			values = append(values, inline)
		}
		for len(values) < opt.Values && i+1 < len(argv) && !isOption(argv[i+1]) {
			i++
			values = append(values, argv[i])
		}
		if len(values) < opt.Values {
			expected := "one argument"
			if opt.Values > 1 {
				expected = strconv.Itoa(opt.Values) + " arguments"
			}
			return parsed, UsageError(fmt.Sprintf("argument %s: expected %s", name, expected))
		}
		for _, value := range values {
			if opt.Int {
				if _, err := strconv.Atoi(strings.TrimSpace(value)); err != nil {
					return parsed, UsageError(fmt.Sprintf("argument %s: invalid int value: '%s'", name, value))
				}
			}
			if opt.Choices != nil && !slices.Contains(opt.Choices, value) {
				return parsed, UsageError(fmt.Sprintf("argument %s: invalid choice: '%s' (choose from %s)", name, value, strings.Join(opt.Choices, ", ")))
			}
		}
		parsed.given[name] = append(parsed.given[name], values...)
	}
	var missing []string
	for _, opt := range spec.Options {
		if opt.Required && !parsed.Has(opt.Name) {
			missing = append(missing, opt.Name)
		}
	}
	if len(parsed.Positionals) < len(spec.Positionals) {
		missing = append(missing, spec.Positionals[len(parsed.Positionals):]...)
	}
	if missing != nil {
		return parsed, UsageError("the following arguments are required: " + strings.Join(missing, ", "))
	}
	if len(parsed.Positionals) > len(spec.Positionals) {
		return parsed, UsageError("unrecognized arguments: " + strings.Join(parsed.Positionals[len(spec.Positionals):], " "))
	}
	return parsed, nil
}

// Has reports whether the option was given on the command line.
func (p Parsed) Has(name string) bool {
	return len(p.given[name]) > 0
}

// String is the last value given, else the option's default.
func (p Parsed) String(name string) string {
	if values := p.given[name]; len(values) > 0 {
		return values[len(values)-1]
	}
	if opt := p.spec.option(name); opt != nil {
		return opt.Default
	}
	return ""
}

// Optional is String as a record value: null when the option was not given and has no default.
func (p Parsed) Optional(name string) any {
	if value := p.String(name); value != "" || p.Has(name) {
		return value
	}
	return nil
}

// Strings is every value given, in order.
func (p Parsed) Strings(name string) []string {
	return p.given[name]
}

func (p Parsed) Int(name string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(p.String(name)))
	return n
}

func (p Parsed) Bool(name string) bool {
	return p.Has(name)
}
