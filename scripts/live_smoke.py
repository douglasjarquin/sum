#!/usr/bin/env python3
"""Model-free native HC canary in a private core; never uses a user session."""
import json
import sys

from hc_lab import Lab, arguments


def main():
    args = arguments(__doc__)
    with Lab(args.hc, args.artifact) as lab:
        launched, control, receipts = lab.fake("worker", first="first-assignment")
        address = launched["address"]
        assert "terminal" not in launched, launched
        assert launched["first_message"]["sent"] is True, launched
        events = lab.subscribe(terminal=address)
        events.wait_status(address, "done")
        assert json.loads(receipts.read_text().splitlines()[0])["text"] == "first-assignment"
        before = lab.call("terminal.get", terminal=address)["turn"]
        sent = lab.call("terminal.send", terminal=address, text="done-followup")
        events.wait_status(address, "done", after_turn=before)
        assert sent["status"] == "done", sent
        assert [json.loads(line)["text"] for line in receipts.read_text().splitlines()] == ["first-assignment", "done-followup"]
        lab.command(control, "blocked")
        events.wait_status(address, "blocked")
        refused = lab.rpc("terminal.send", terminal=address, text="must-not-answer-dialog")
        assert refused["error"]["data"]["kind"] == "blocked", refused
        assert len(receipts.read_text().splitlines()) == 2
        reconnected = lab.subscribe(labels={"sum.lab": lab.session})
        assert len(reconnected.snapshot["terminals"]) == 1
        assert reconnected.snapshot["terminals"][0]["status"]["state"] == "blocked"
        lab.call("terminal.kill", terminal=address)
        finished = lab.call("terminal.get", terminal=address)
        assert finished["state"] == "finished" and finished.get("status") is None and finished.get("foreground") is None
        assert lab.call("terminal.rm", terminal=address)["removed"] == address
        assert not lab.call("terminal.list", all=True)["terminals"]
        lab.summary.update(first_message=launched["first_message"], followup=sent, blocked_refusal=refused["error"]["data"]["kind"])
    print("PASS: isolated HC 0.1.14 native launch, first assignment, Done follow-up, blocked refusal, event snapshot/reconnect and cleanup.")
    print("Model-free runtime proof; authenticated harness and full Sum lifecycle acceptance remain separate.")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"HC live smoke failed: {exc}", file=sys.stderr)
        sys.exit(1)
