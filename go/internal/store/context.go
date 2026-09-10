package store

import (
	"fmt"
	"os"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var (
	sessionNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	socketSessionPattern = regexp.MustCompile(`/sessions/([^/]+)/herdr\.sock$`)
)

func SessionFromEnv() (string, error) {
	value := os.Getenv("SUM_SESSION")
	if value == "" {
		value = os.Getenv("HERDR_SESSION")
	}
	if value == "" {
		if m := socketSessionPattern.FindStringSubmatch(os.Getenv("HERDR_SOCKET_PATH")); m != nil {
			value = m[1]
		}
	}
	if value == "" {
		value = "default"
	}
	if !sessionNamePattern.MatchString(value) {
		return "", fmt.Errorf("cannot identify the Herdr session. Set SUM_SESSION to its explicit name")
	}
	return value, nil
}

func Context(root string) (*ordjson.Object, error) {
	if os.Getenv("HERDR_ENV") != "1" || os.Getenv("HERDR_PANE_ID") == "" {
		return nil, fmt.Errorf("run this command inside a Herdr pane (HERDR_ENV=1 and HERDR_PANE_ID are required)")
	}
	session, err := SessionFromEnv()
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	ctx := ordjson.NewObject()
	ctx.Set("session", session)
	ctx.Set("pane", os.Getenv("HERDR_PANE_ID"))
	ctx.Set("machine", hostname)
	ctx.Set("cwd", root)
	ctx.Set("at", Now())
	return ctx, nil
}

func EndpointFromContext(ctx *ordjson.Object) Endpoint {
	machine, _ := ctx.Get("machine")
	session, _ := ctx.Get("session")
	pane, _ := ctx.Get("pane")
	cwd, _ := ctx.Get("cwd")
	m, _ := machine.(string)
	s, _ := session.(string)
	p, _ := pane.(string)
	c, _ := cwd.(string)
	return Endpoint{Machine: m, Session: s, Pane: p, Cwd: c}
}
