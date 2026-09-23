package environment

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestForegroundLeaderBindsOnlyAProvableGroupLeader(t *testing.T) {
	cases := []struct {
		name string
		info string
		want int
	}{
		{name: "agent alone", info: `{"shell_pid": 10, "foreground_process_group_id": 20, "processes": [{"pid": 20, "argv": ["claude"]}]}`, want: 20},
		{name: "agent with children in its group", info: `{"shell_pid": 10, "foreground_process_group_id": 20, "processes": [{"pid": 20, "argv": ["claude"]}, {"pid": 21, "argv": ["node", "mcp"]}, {"pid": 22, "argv": ["node", "mcp"]}]}`, want: 20},
		{name: "leader exited, children remain", info: `{"shell_pid": 10, "foreground_process_group_id": 20, "processes": [{"pid": 21, "argv": ["node", "mcp"]}]}`},
		{name: "shell holds the foreground", info: `{"shell_pid": 10, "foreground_process_group_id": 10, "processes": [{"pid": 11, "argv": ["sleep", "1"]}]}`},
		{name: "leader observed twice", info: `{"shell_pid": 10, "foreground_process_group_id": 20, "processes": [{"pid": 20, "argv": ["claude"]}, {"pid": 20, "argv": ["other"]}]}`},
		{name: "leader without argv", info: `{"shell_pid": 10, "foreground_process_group_id": 20, "processes": [{"pid": 20, "argv": []}]}`},
		{name: "no group id", info: `{"shell_pid": 10, "foreground_process_group_id": null, "processes": [{"pid": 20, "argv": ["claude"]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := ordjson.Decode([]byte(tc.info))
			if err != nil {
				t.Fatal(err)
			}
			leader := ForegroundLeader(decoded.(*ordjson.Object))
			if tc.want == 0 {
				if leader != nil {
					t.Fatalf("leader = %v, want none", leader)
				}
				return
			}
			if got := intField(leader, "pid"); got != tc.want {
				t.Fatalf("leader pid = %d, want %d", got, tc.want)
			}
		})
	}
}
