package rollback

import "testing"

func TestListAndApplyUnsafeRollbackRefuses(t *testing.T) {
	state := t.TempDir()
	rec := Record{
		ID:                 "rb-1",
		Node:               "node-1",
		Component:          "firewall",
		ManualInstructions: "use console access",
		SafeAutomatic:      false,
	}
	if err := Save(state, rec); err != nil {
		t.Fatal(err)
	}
	records, err := List(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "rb-1" {
		t.Fatalf("unexpected records: %+v", records)
	}
	if _, err := Apply(nil, state, "rb-1", true, nil); err == nil {
		t.Fatal("expected unsafe rollback refusal")
	}
}
