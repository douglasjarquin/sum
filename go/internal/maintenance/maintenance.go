package maintenance

import (
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func Refuse(task *ordjson.Object, action string) error {
	if task == nil {
		return fmt.Errorf("%s requires a current task record", action)
	}
	id, _ := task.Get("id")
	if raw, _ := task.Get("cleanup"); raw != nil {
		if record, ok := raw.(*ordjson.Object); ok && record != nil {
			if intent, _ := record.Get("intent"); intent != nil && intent != false {
				return fmt.Errorf("%s is refused because cleanup intent is active for task %v; no execution side effect occurred.", action, id)
			}
		}
	}
	if raw, _ := task.Get("terminal_maintenance"); raw != nil {
		if record, ok := raw.(*ordjson.Object); ok && record != nil {
			state, _ := record.Get("state")
			if state == "claimed" || state == "uncertain" {
				return fmt.Errorf("%s is refused because terminal maintenance is %v for task %v; reconcile the exact terminal operation before creating another obligation or native effect.", action, state, id)
			}
		}
	}
	return nil
}
