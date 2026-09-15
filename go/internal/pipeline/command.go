package pipeline

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const Note = "The table is the coordinator's record of which gates have run. It is not a merge decision."

func View(record Record) *ordjson.Object {
	view := record.Object()
	view.Set("table", record.Table())
	view.Set("note", Note)
	return view
}

func Show(s *store.Store, taskID string) (*ordjson.Object, error) {
	if _, err := s.ReadTask(taskID); err != nil {
		return nil, err
	}
	record, err := Load(s, taskID)
	if err != nil {
		return nil, err
	}
	return View(record), nil
}

func RefreshCommand(s *store.Store, ctx *ordjson.Object, taskID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	record, err := Refresh(s, taskID)
	if err != nil {
		return nil, err
	}
	return View(record), nil
}

func PublishCommand(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args PublishArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	args.Trigger = "manual"
	publication, err := Publish(s, ctx, runtimeRoot, args)
	if err != nil {
		return nil, err
	}
	record, err := Load(s, args.Task)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("publication", publication.Row())
	result.Set("pipeline", View(record))
	return result, nil
}
