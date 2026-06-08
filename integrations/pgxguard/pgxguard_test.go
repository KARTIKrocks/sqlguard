package pgxguard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KARTIKrocks/sqlguard/analyzer"
	"github.com/KARTIKrocks/sqlguard/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
)

// capture is a thread-safe in-memory Reporter for assertions.
type capture struct {
	mu sync.Mutex
	r  []analyzer.Result
}

func (c *capture) Report(rs []analyzer.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.r = append(c.r, rs...)
}

func (c *capture) snapshot() []analyzer.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]analyzer.Result, len(c.r))
	copy(out, c.r)
	return out
}

func (c *capture) has(rule string) bool {
	for _, r := range c.snapshot() {
		if r.RuleName == rule {
			return true
		}
	}
	return false
}

// stubTracer is a fake existing pgx.QueryTracer used to prove Apply composes
// instead of clobbering.
type stubTracer struct {
	mu     sync.Mutex
	starts int
	ends   int
}

func (s *stubTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	s.mu.Lock()
	s.starts++
	s.mu.Unlock()
	return ctx
}

func (s *stubTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	s.mu.Lock()
	s.ends++
	s.mu.Unlock()
}

func newTracerWithCapture(t *testing.T, opts ...middleware.Option) (*Tracer, *capture) {
	t.Helper()
	cap := &capture{}
	opts = append([]middleware.Option{middleware.WithReporter(cap)}, opts...)
	return NewTracer(opts...), cap
}

// driveQuery runs a full Start→End round trip with no error.
func driveQuery(tr *Tracer, sql string, err error) {
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: sql})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: err})
}

func TestTracer_DetectsSelectStarOnQueryStart(t *testing.T) {
	tr, cap := newTracerWithCapture(t)
	driveQuery(tr, "SELECT * FROM users", nil)
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding, got %+v", cap.snapshot())
	}
}

func TestTracer_RedactsLiteralsByDefault(t *testing.T) {
	tr, cap := newTracerWithCapture(t)
	driveQuery(tr, "SELECT * FROM users WHERE email = 'leak@example.com'", nil)
	results := cap.snapshot()
	if len(results) == 0 {
		t.Fatal("expected at least one finding")
	}
	for _, r := range results {
		if strings.Contains(r.Query, "leak@example.com") {
			t.Errorf("literal leaked into Result.Query: %q", r.Query)
		}
		if r.Fingerprint == "" {
			t.Errorf("Fingerprint must always be populated, got empty for rule %s", r.RuleName)
		}
	}
}

func TestTracer_SlowQueryReportedOnEnd(t *testing.T) {
	tr, cap := newTracerWithCapture(t, middleware.WithSlowQueryThreshold(1*time.Millisecond))
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT id FROM users WHERE id = 1"})
	time.Sleep(5 * time.Millisecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: nil})
	if !cap.has("slow-query") {
		t.Fatalf("expected slow-query finding, got %+v", cap.snapshot())
	}
}

func TestTracer_SlowQuerySuppressedOnError(t *testing.T) {
	tr, cap := newTracerWithCapture(t, middleware.WithSlowQueryThreshold(1*time.Millisecond))
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT id FROM users WHERE id = 1"})
	time.Sleep(5 * time.Millisecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})
	if cap.has("slow-query") {
		t.Fatalf("slow-query should not fire when the query failed; got %+v", cap.snapshot())
	}
}

func TestTracer_NPlusOneAcrossCalls(t *testing.T) {
	tr, cap := newTracerWithCapture(t, middleware.WithN1Detection(3, time.Second))
	for range 3 {
		driveQuery(tr, "SELECT id FROM users WHERE id = 1", nil)
	}
	if !cap.has("n-plus-one") {
		t.Fatalf("expected n-plus-one finding after 3 identical queries, got %+v", cap.snapshot())
	}
}

func TestTracer_ResetN1ClearsState(t *testing.T) {
	tr, cap := newTracerWithCapture(t, middleware.WithN1Detection(3, time.Second))
	for range 2 {
		driveQuery(tr, "SELECT id FROM users WHERE id = 1", nil)
	}
	tr.ResetN1()
	driveQuery(tr, "SELECT id FROM users WHERE id = 1", nil)
	if cap.has("n-plus-one") {
		t.Fatalf("n-plus-one should not fire — reset zeroed the counter; got %+v", cap.snapshot())
	}
}

func TestTracer_BatchQueryAnalyzed(t *testing.T) {
	tr, cap := newTracerWithCapture(t)
	ctx := tr.TraceBatchStart(context.Background(), nil, pgx.TraceBatchStartData{})
	tr.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{SQL: "SELECT * FROM users"})
	tr.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})
	if !cap.has("select-star") {
		t.Fatalf("expected select-star finding from batch path, got %+v", cap.snapshot())
	}
}

func TestApply_NilExistingSetsOursDirectly(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Tracer != nil {
		t.Fatalf("baseline assumption broken: ParseConfig set a tracer (%T)", cfg.Tracer)
	}
	Apply(cfg)
	if _, ok := cfg.Tracer.(*Tracer); !ok {
		t.Fatalf("expected *pgxguard.Tracer, got %T", cfg.Tracer)
	}
}

// TestApply_ComposesWithExistingTracer is the headline community-fitness
// guarantee: if the user has already wired e.g. otelpgx, Apply must NOT
// silently overwrite it.
func TestApply_ComposesWithExistingTracer(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	stub := &stubTracer{}
	cfg.Tracer = stub

	Apply(cfg)

	mt, ok := cfg.Tracer.(*multitracer.Tracer)
	if !ok {
		t.Fatalf("expected *multitracer.Tracer after composition, got %T", cfg.Tracer)
	}

	var sawStub, sawOurs bool
	for _, qt := range mt.QueryTracers {
		switch qt.(type) {
		case *stubTracer:
			sawStub = true
		case *Tracer:
			sawOurs = true
		}
	}
	if !sawStub {
		t.Error("existing tracer was dropped by Apply — community-fitness contract violated")
	}
	if !sawOurs {
		t.Error("our tracer was not installed by Apply")
	}

	// And drive it: the existing stub must still receive Start/End events.
	ctx := cfg.Tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	cfg.Tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.starts != 1 || stub.ends != 1 {
		t.Errorf("existing tracer not driven through composition: starts=%d ends=%d", stub.starts, stub.ends)
	}
}

func TestApplyPool_DelegatesAndComposes(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatalf("pgxpool.ParseConfig: %v", err)
	}
	stub := &stubTracer{}
	cfg.ConnConfig.Tracer = stub

	ApplyPool(cfg)

	if _, ok := cfg.ConnConfig.Tracer.(*multitracer.Tracer); !ok {
		t.Fatalf("ApplyPool did not compose: got %T", cfg.ConnConfig.Tracer)
	}
}

func TestApply_NilConfigPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil *pgx.ConnConfig")
		}
	}()
	Apply(nil)
}

func TestApplyPool_NilConfigPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil *pgxpool.Config")
		}
	}()
	ApplyPool(nil)
}
