package retrieval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// A delivery is prompt input, not an audit export. The ledger envelope
// (transaction/at/position) and a create row's schema-sized field list say how
// the write was stored, not what changed; a measured 20-create delivery spent
// ~4 KB repeating the same ten column names. An update keeps its field list
// because there it is the news.
func TestDeliveryDropsLedgerEnvelopeAndCreateFieldLists(t *testing.T) {
	s := ledger(t)
	created, err := s.AddFact(model.Fact{Kind: "note", Title: "before"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateFact(created.ID, func(f *model.Fact) { f.Title = "after" }); err != nil {
		t.Fatal(err)
	}
	ctx, err := WorkingContext(s, "commander")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"transaction"`, `"position"`, `"at"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("delivery kept the ledger envelope %s: %s", key, raw)
		}
	}
	var create, update *DeliveryChange
	for i := range ctx.Changes.Changes {
		c := &ctx.Changes.Changes[i]
		if c.ID != created.ID {
			continue
		}
		switch c.Operation {
		case "create":
			create = c
		case "update":
			update = c
		}
	}
	if create == nil || update == nil {
		t.Fatalf("fixture changes missing: %+v", ctx.Changes.Changes)
	}
	if len(create.Fields) != 0 {
		t.Fatalf("create kept its schema-sized field list: %v", create.Fields)
	}
	if len(update.Fields) == 0 {
		t.Fatal("update lost the field list that says what changed")
	}
}
