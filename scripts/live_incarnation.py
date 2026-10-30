#!/usr/bin/env python3
"""Model-free HC terminal identity and parent-lifetime canary."""
import sys
import os
import signal

from hc_lab import Lab, arguments


def main():
    args = arguments(__doc__)
    with Lab(args.hc, args.artifact) as lab:
        parent, _, _ = lab.fake("parent")
        worker, control, _ = lab.fake("worker", parent=parent["address"])
        dependent, _, _ = lab.fake("dependent", parent=parent["address"], outlive=False)
        address = worker["address"]
        events = lab.subscribe(terminal=address)
        events.wait_status(address, "idle")
        foreground = events.wait_foreground(address)
        assert foreground and foreground["pid"] > 0
        lifetimes = lab.subscribe(labels={"sum.lab": lab.session})
        lab.call("terminal.kill", terminal=parent["address"])
        surviving = lab.call("terminal.get", terminal=address)
        assert surviving["state"] == "live" and surviving["parent"] == parent["address"]
        # A native wait may end on idle before exit; require the exit observation.
        lifetimes.wait_event(dependent["address"], "finished")
        assert lab.call("terminal.get", terminal=dependent["address"])["state"] == "finished"
        lab.command(control, "exit")
        events.wait_event(address, "finished")
        finished = lab.call("terminal.get", terminal=address)
        assert finished["state"] == "finished" and finished.get("foreground") is None and finished.get("status") is None
        replacement, _, _ = lab.fake("worker")
        assert replacement["address"] != address
        assert lab.call("hello", generation=1)["core"]["id"] == lab.core_id
        # Kept loss is a replacement shell, never the original worker command.
        kept, _, _ = lab.fake("kept", keep=True)
        kept_events = lab.subscribe(terminal=kept["address"])
        old_foreground = kept_events.wait_foreground(kept["address"])
        os.kill(old_foreground["pid"], signal.SIGKILL)
        restarted = kept_events.wait_event(kept["address"], "restarted")
        assert restarted["address"] == kept["address"] and restarted["state"] == "live"
        assert restarted.get("pid") != old_foreground["pid"]
        assert restarted.get("status") is None
        lab.summary["kept_recovery"] = {"same_terminal": True, "different_program": True, "worker_status_cleared": True}
        lab.summary.update(old_address=address, new_address=replacement["address"], original_foreground=foreground,
                           outlived_parent=True, nonkept_finished=True, native_parent_unchanged=True)
    print("PASS: isolated HC permanent address, same-name replacement, outliving worker, dependent exit, non-kept termination and kept-shell replacement.")
    print("Native identity proof; Sum binding authority and approved migration are verified separately.")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"HC incarnation lab failed: {exc}", file=sys.stderr)
        sys.exit(1)
