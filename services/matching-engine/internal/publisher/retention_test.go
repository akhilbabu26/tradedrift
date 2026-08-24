package publisher

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeDBForRetention struct {
	executedQuery string
}

func (f *fakeDBForRetention) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	f.executedQuery = sql
	return pgconn.CommandTag{}, nil
}

func TestRetentionNeverDeletesRecoveryAnchor(t *testing.T) {
	db := &fakeDBForRetention{}
	p := &Publisher{
		db: db,
	}

	err := p.runRetention(context.Background())
	if err != nil {
		t.Fatalf("runRetention failed: %v", err)
	}

	// Invariant Check (Issue #2): SQL query must contain the anchor recovery filter checks.
	// The query must ensure that snapshots <= checkpoint offset are NOT deleted.
	if !strings.Contains(db.executedQuery, "ms.offset <= kc.offset") {
		t.Fatal("retention SQL query lacks recovery anchor offset filter ('ms.offset <= kc.offset')")
	}
	if !strings.Contains(db.executedQuery, "AND NOT EXISTS") {
		t.Fatal("retention SQL query lacks safety exclusions filter ('AND NOT EXISTS')")
	}
}
