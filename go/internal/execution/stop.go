package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"os"
	"time"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	OutcomeStopped = "stopped"
	OutcomeUnknown = "unknown"
)

type StopProof struct {
	Outcome     string
	Reason      string
	Observation *ordjson.Object

	Orphans []proc.BoundProcess
}

func ObserveStop(s *store.Store, runtimeRoot string, task, attempt *ordjson.Object) StopProof {
	if stringField(attempt, "state") == "released" {
		return StopProof{Outcome: OutcomeStopped, Observation: lastObservation(attempt)}
	}
	kind := stringField(attempt, "kind")
	if kind == "worker" {
		return observeWorker(s, runtimeRoot, task, attempt)
	}
	return observeVerifier(s, attempt)
}

func observeVerifier(s *store.Store, attempt *ordjson.Object) StopProof {
	checkout := stringField(attempt, "checkout")
	if checkout == "" {
		return unknown("Verifier checkout identity is missing; reservation remains held.")
	}
	opPID, hasOp := pidField(attempt, "operation_pid")
	if !hasOp || opPID == 0 {
		return unknown("Verifier process identity is missing; reservation remains held.")
	}
	occupant := asObject(func() any { v, _ := attempt.Get("occupant"); return v }())
	occPID, hasOcc := pidField(occupant, "pid")
	if occupant == nil || !hasOcc || occPID == 0 {
		return unknown("Verifier occupant identity is missing; reservation remains held.")
	}
	argv, _ := occupant.Get("argv")
	if normalizeRecordedArgv(argv) == nil {
		return unknown("Verifier occupant argv is missing; reservation remains held.")
	}
	host, err := s.Machine()
	if err != nil {
		return unknown("Verifier instance cannot be inspected; reservation remains held.")
	}
	if !host.Is(stringField(occupant, "machine")) {
		return unknown("Verifier occupant instance does not match this machine; reservation remains held.")
	}
	occCheckout := stringField(occupant, "checkout")
	if occCheckout == "" || resolve(occCheckout) != resolve(checkout) {
		return unknown("Verifier occupant checkout does not match the attempt; reservation remains held.")
	}
	if proof := inspectRecordedPIDs([]int{opPID, occPID}, occupant, occPID); proof.Outcome != OutcomeStopped {
		return proof
	}
	inside, err := proc.ProcessesBoundTo(checkout, nil)
	if err != nil {
		return unknown("Verifier checkout processes cannot be inspected; reservation remains held. " + err.Error())
	}
	if len(inside) > 0 {
		return unknown(fmt.Sprintf("Verifier still has an owned process bound to its checkout (pid %d); reservation remains held.", inside[0].PID))
	}
	return stopped(func(obs *ordjson.Object) {
		obs.Set("pid", jsonNumber(occPID))
		obs.Set("checkout", checkout)
		_, statErr := os.Stat(checkout)
		obs.Set("checkout_present", statErr == nil)
	})
}

func observeWorker(s *store.Store, runtimeRoot string, task, attempt *ordjson.Object) StopProof {
	endpoint := workerEndpoint(task)
	if err := endpoint.Validate(); err != nil {
		native := asObject(field(attempt, "native_launch"))
		noEffect := native == nil && field(task, "status") == "prepared"
		if native != nil && field(native, "effect") == string(hcclient.NotSubmitted) {
			noEffect = true
		}
		if !noEffect {
			return unknown("Worker native endpoint is missing; reservation remains held.")
		}
		var intent taskcheckout.Intent
		if e := decodeNative(field(task, "checkout_intent"), &intent); e != nil {
			return unknown("Unlaunched worker has no saved Git intent")
		}
		if _, e := taskcheckout.Verify(context.Background(), intent, false); e != nil {
			return unknown(e.Error())
		}
		inside, e := proc.ProcessesBoundTo(intent.Path, nil)
		if e != nil {
			return unknown(e.Error())
		}
		if len(inside) > 0 {
			return unknown("Unlaunched worker checkout has bound processes")
		}
		return stoppedServices(s, task, func(obs *ordjson.Object) {
			obs.Set("checkout", intent.Path)
			obs.Set("reason", "saved prepared attempt has no native launch intent")
		})
	}
	worktree := stringField(task, "worktree")
	if worktree == "" {
		return unknown("Worker checkout is missing")
	}
	if err := s.CheckMachine(task); err != nil {
		return unknown(err.Error())
	}
	if proof := NativeProgramStopped(s, endpoint); proof.Outcome != OutcomeStopped {
		return proof
	}
	host, err := s.Machine()
	if err != nil {
		return unknown(err.Error())
	}
	inside, err := proc.ProcessesBoundTo(worktree, EndpointShells(host, runtimeRoot, task))
	if err != nil {
		return unknown("Worker checkout processes cannot be inspected: " + err.Error())
	}
	if len(inside) > 0 {
		proof := unknown(fmt.Sprintf("Worker still has an owned process bound to its checkout (pid %d); reservation remains held.", inside[0].PID))
		proof.Orphans = inside
		return proof
	}
	return stoppedServices(s, task, func(obs *ordjson.Object) {
		obs.Set("address", endpoint.Address)
		obs.Set("core_id", endpoint.CoreID)
		obs.Set("checkout", worktree)
	})
}

func stoppedServices(s *store.Store, task *ordjson.Object, extra func(*ordjson.Object)) StopProof {
	env, err := environment.Read(s, stringField(task, "id"))
	if err != nil {
		return unknown(err.Error())
	}
	if env != nil {
		services, _ := env.Get("services")
		list, _ := services.([]any)
		var active []any
		for _, raw := range list {
			row := asObject(raw)
			state := stringField(row, "state")
			for _, a := range append(environment.ServiceActive, "failed") {
				if state == a {
					id, _ := row.Get("id")
					active = append(active, id)
				}
			}
		}
		if len(active) > 0 {
			return unknown(fmt.Sprintf("Owned service reservations remain unresolved: %v; stop or reconcile them before parking the worker.", active))
		}
	}
	return stopped(extra)
}

func EndpointShells(host machine.Identity, runtimeRoot string, task *ordjson.Object) map[int]bool {
	return map[int]bool{}
}

func nativeAbsent(err error) bool {
	var rpc *hcclient.RPCError
	if !errors.As(err, &rpc) {
		return false
	}
	var data struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(rpc.Data, &data) == nil && data.Kind == "not_found"
}

func unwrapField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	if inner := asObject(func() any { v, _ := o.Get(key); return v }()); inner != nil {
		return inner
	}
	return o
}

func inspectRecordedPIDs(pids []int, occupant *ordjson.Object, occupantPID int) StopProof {
	seen := map[int]bool{}
	for _, pid := range pids {
		if pid == 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		running, err := proc.PIDRunning(pid)
		if err != nil {
			return unknown(err.Error())
		}
		if running {
			if occupant != nil && pid == occupantPID {
				live, argvErr := proc.ProcessArgv(pid)
				if argvErr != nil {
					return unknown("Recorded pid cannot be inspected; reservation remains held. " + argvErr.Error())
				}
				argv, _ := occupant.Get("argv")
				if !proc.ArgvEqual(argv, live) {
					return unknown(fmt.Sprintf("Recorded pid %d was reused or does not match recorded argv; reservation remains held.", pid))
				}
			}
			return unknown(fmt.Sprintf("Recorded pid %d still exists; reservation remains held.", pid))
		}
		kids, err := proc.Descendants(pid)
		if err != nil {
			return unknown("Owned child processes cannot be inspected; reservation remains held. " + err.Error())
		}
		for _, child := range kids {
			childRunning, childErr := proc.PIDRunning(child)
			if childErr != nil {
				return unknown(childErr.Error())
			}
			if childRunning {
				return unknown(fmt.Sprintf("Owned child pid %d still exists; reservation remains held.", child))
			}
		}
	}
	return StopProof{Outcome: OutcomeStopped}
}

func unknown(reason string) StopProof {
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", OutcomeUnknown)
	obs.Set("reason", reason)
	return StopProof{Outcome: OutcomeUnknown, Reason: reason, Observation: obs}
}

func stopped(extra func(*ordjson.Object)) StopProof {
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", OutcomeStopped)
	if extra != nil {
		extra(obs)
	}
	return StopProof{Outcome: OutcomeStopped, Observation: obs}
}

func stringField(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func pidField(o *ordjson.Object, key string) (int, bool) {
	if o == nil {
		return 0, false
	}
	if _, has := o.Get(key); !has {
		return 0, false
	}
	n := intFrom(o, key)
	return n, n > 0
}

func normalizeRecordedArgv(argv any) []string {
	list, ok := argv.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func NativeProgramStopped(s *store.Store, endpoint store.Endpoint) StopProof {
	if err := endpoint.Validate(); err != nil {
		return unknown(err.Error())
	}
	host, err := s.Machine()
	if err != nil {
		return unknown(err.Error())
	}
	if !host.Is(endpoint.Machine) {
		return unknown("Native program belongs to another machine; death cannot be proved locally.")
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return unknown(err.Error())
	}
	var recorded incarnation.NativeEvidence
	if err = decodeNative(field(registration, "incarnation"), &recorded); err != nil || !recorded.Endpoint.SameAuthority(endpoint.Native()) || recorded.PID == 0 || recorded.Foreground == nil || recorded.Foreground.PID <= 0 || len(recorded.Foreground.Argv) == 0 {
		return unknown("Saved native program identity is missing or differs from this binding.")
	}
	observe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(observe, endpoint.Address)
	if err != nil && !nativeAbsent(err) {
		return unknown("Native terminal cannot prove exit: " + err.Error())
	}
	if err == nil {
		if terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.Keep {
			return unknown("Native terminal creation changed or is kept; program death is not proved.")
		}
		if terminal.State != "finished" || terminal.FinishedMS == nil || *terminal.FinishedMS == 0 || (terminal.ExitCode == nil && terminal.Signal == nil) {
			return unknown("Native terminal has no conclusive finished program exit metadata.")
		}
		if endpoint.Cwd != "" && (terminal.Cwd == "" || resolve(endpoint.Cwd) != resolve(terminal.Cwd)) {
			return unknown("Native terminal checkout identity differs from the saved binding.")
		}
	}
	occupant := ordjson.NewObject()
	argv := make([]any, len(recorded.Foreground.Argv))
	for i, arg := range recorded.Foreground.Argv {
		argv[i] = arg
	}
	occupant.Set("argv", argv)
	if proof := inspectRecordedPIDs([]int{int(recorded.PID), int(recorded.Foreground.PID)}, occupant, int(recorded.Foreground.PID)); proof.Outcome != OutcomeStopped {
		return proof
	}
	return stopped(func(observation *ordjson.Object) {
		observation.Set("endpoint", endpoint.Object())
		observation.Set("pid", jsonNumber(int(recorded.PID)))
		observation.Set("reason", "exact saved native terminal and program have exited")
	})
}
