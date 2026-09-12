package herdrbridge

import (
	"fmt"
	"io"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

var readOnly = map[[2]string]bool{
	{"agent", "list"}: true, {"agent", "get"}: true, {"agent", "read"}: true, {"agent", "wait"}: true,
	{"pane", "get"}: true, {"pane", "read"}: true, {"pane", "list"}: true,
	{"workspace", "list"}: true, {"integration", "status"}: true, {"session", "list"}: true,
}

func Run(runtimeRoot string, s *store.Store, ctx *ordjson.Object, args []string, stdout io.Writer) error {
	if ctx == nil {
		return fmt.Errorf("run this command inside a Herdr pane (HERDR_ENV=1 and HERDR_PANE_ID are required). The bridge never borrows a saved coordinator context.")
	}
	endpoint := store.EndpointFromContext(ctx)
	var registration *ordjson.Object
	if s.Designated() {
		var err error
		registration, err = s.Registration(endpoint)
		if err != nil {
			return err
		}
	}
	if registration == nil {
		pane, _ := ctx.Get("pane")
		session, _ := ctx.Get("session")
		return fmt.Errorf("Pane %v in session %v is not registered with %s. Run ./bin/sumctl init there first; a development checkout gets no access to another instance's panes.", pane, session, s.Home)
	}
	role, _ := registration.Get("role")
	native := args
	if len(native) > 0 && native[0] == "--" {
		native = native[1:]
	}
	if role == "developer" && !isReadOnly(native) {
		return fmt.Errorf("Developer sessions may only observe through the bridge. Coordination commands need the registered coordinator pane.")
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return err
	}
	session, _ := ctx.Get("session")
	sessionStr, _ := session.(string)
	stdoutText, err := herdrclient.CallRaw(herdrPath, sessionStr, 70*time.Second, native...)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, stdoutText)
	return err
}

func isReadOnly(args []string) bool {
	var pair [2]string
	if len(args) > 0 {
		pair[0] = args[0]
	}
	if len(args) > 1 {
		pair[1] = args[1]
	}
	return readOnly[pair]
}
