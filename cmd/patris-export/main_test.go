package main

import (
	"testing"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

func TestWatchChangeStateBuildsChangesAfterInitialBaseline(t *testing.T) {
	state := &watchChangeState{}
	initial := recordpipe.Result{
		KeyField: "sku",
		Rows: []map[string]interface{}{
			{"sku": "100", "title": "Bolt", "price": 10},
		},
	}
	if changes := state.Next(initial, "initial", time.Unix(1, 0)); changes != nil {
		t.Fatalf("initial event must carry the full records payload, not a changeset: %#v", changes)
	}

	update := recordpipe.Result{
		KeyField: "sku",
		Rows: []map[string]interface{}{
			{"sku": "100", "title": "Bolt", "price": 12},
			{"sku": "200", "title": "Nut", "price": 5},
		},
	}
	changes := state.Next(update, "update", time.Unix(2, 0))
	if changes == nil {
		t.Fatal("watch update returned a nil changeset")
	}
	added, modified, deleted := changes.Counts()
	if added != 1 || modified != 1 || deleted != 0 {
		t.Fatalf("unexpected watch changes: added=%d modified=%d deleted=%d", added, modified, deleted)
	}
	if changes.Modified[0].Record["sku"] != "100" || changes.Modified[0].Record["title"] != "Bolt" {
		t.Fatalf("modified watch row is incomplete: %#v", changes.Modified[0].Record)
	}
}
