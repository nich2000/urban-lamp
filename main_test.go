package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateService(t *testing.T) {
	tests := []struct {
		target  string
		method  string
		wantErr bool
	}{
		{target: "https://example.com", method: "http"},
		{target: "1.1.1.1", method: "icmp"},
		{target: "", method: "http", wantErr: true},
		{target: "example.com", method: "http", wantErr: true},
		{target: "https://example.com", method: "icmp", wantErr: true},
		{target: "example.com", method: "tcp", wantErr: true},
	}

	for _, tt := range tests {
		err := validateService(tt.target, tt.method)
		if tt.wantErr && err == nil {
			t.Fatalf("validateService(%q, %q) expected error", tt.target, tt.method)
		}
		if !tt.wantErr && err != nil {
			t.Fatalf("validateService(%q, %q) unexpected error: %v", tt.target, tt.method, err)
		}
	}
}

func TestHelpers(t *testing.T) {
	t.Setenv("URBAN_LAMP_ADDR", "")
	t.Setenv("PORT", "9090")

	duration := int64(longPingThreshold + 1)
	checkedAt := time.Date(2026, 5, 29, 12, 34, 56, 0, time.Local)
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "sql quote", got: sqlQuote("service's api"), want: "'service''s api'"},
		{name: "bare http target", got: normalizeTarget("drift-dynamics.com", "http"), want: "https://drift-dynamics.com"},
		{name: "full http target", got: normalizeTarget("http://drift-dynamics.com", "http"), want: "http://drift-dynamics.com"},
		{name: "icmp target", got: normalizeTarget("drift-dynamics.com", "icmp"), want: "drift-dynamics.com"},
		{name: "display url port", got: displayURL(":8080"), want: "http://localhost:8080"},
		{name: "display url host", got: displayURL("127.0.0.1:18080"), want: "http://127.0.0.1:18080"},
		{name: "listen addr from port", got: listenAddr(), want: ":9090"},
		{name: "format time", got: formatTime(checkedAt), want: "12:34:56"},
		{name: "format nil result", got: formatResultTime(nil), want: "—"},
		{name: "format result", got: formatResultTime(&PingResult{CheckedAt: checkedAt}), want: "12:34:56"},
		{name: "status nil", got: statusClass(nil), want: "unknown"},
		{name: "status down", got: statusClass(&PingResult{}), want: "down"},
		{name: "status slow", got: statusClass(&PingResult{OK: true, DurationMS: &duration}), want: "slow"},
		{name: "status ok", got: statusClass(&PingResult{OK: true}), want: "ok"},
		{name: "status message", got: statusMessage(&PingResult{}), want: "нет ответа"},
		{name: "first line", got: firstLine("one\ntwo"), want: "one"},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if boolToInt(true) != 1 || boolToInt(false) != 0 {
		t.Fatal("boolToInt returned unexpected values")
	}

	t.Setenv("URBAN_LAMP_VALUE", " configured ")
	if got := getenv("URBAN_LAMP_VALUE", "fallback"); got != "configured" {
		t.Fatalf("getenv configured = %q", got)
	}
	t.Setenv("URBAN_LAMP_EMPTY", "")
	if got := getenv("URBAN_LAMP_EMPTY", "fallback"); got != "fallback" {
		t.Fatalf("getenv fallback = %q", got)
	}

	t.Setenv("URBAN_LAMP_ADDR", "127.0.0.1:18080")
	if got := listenAddr(); got != "127.0.0.1:18080" {
		t.Fatalf("listenAddr explicit = %q", got)
	}
	t.Setenv("URBAN_LAMP_ADDR", "")
	t.Setenv("PORT", "127.0.0.1:18081")
	if got := listenAddr(); got != "127.0.0.1:18081" {
		t.Fatalf("listenAddr host port = %q", got)
	}

	longLine := firstLine(strings.Repeat("x", 220))
	if len(longLine) != 180 {
		t.Fatalf("firstLine truncation length = %d", len(longLine))
	}

	if got := firstNonEmpty("", " value ", "other"); got != " value " {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty empty = %q", got)
	}
	if got := errorText(nil); got != "" {
		t.Fatalf("errorText nil = %q", got)
	}
	if got := errorText(context.Canceled); got == "" {
		t.Fatal("errorText error should be user-facing")
	}
}

func TestServiceWord(t *testing.T) {
	tests := map[int]string{
		0:  "сервисов",
		1:  "сервис",
		2:  "сервиса",
		4:  "сервиса",
		5:  "сервисов",
		11: "сервисов",
		21: "сервис",
		22: "сервиса",
		25: "сервисов",
	}

	for count, want := range tests {
		if got := serviceWord(count); got != want {
			t.Fatalf("serviceWord(%d) = %q, want %q", count, got, want)
		}
	}
}

func TestStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatalf("seed idempotent: %v", err)
	}

	services, err := store.ListServices(ctx)
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	count, err := store.CountServices(ctx)
	if err != nil {
		t.Fatalf("count services: %v", err)
	}
	if count != len(services) {
		t.Fatalf("count services = %d, want %d", count, len(services))
	}
	if len(services) != 2 {
		t.Fatalf("seeded services = %d, want 2", len(services))
	}

	if err := store.CreateService(ctx, "https://example.org", "https://example.org", "http"); err != nil {
		t.Fatalf("create service: %v", err)
	}
	services, _ = store.ListServices(ctx)
	created := services[len(services)-1]
	if err := store.UpdateService(ctx, created.ID, "1.1.1.1", "icmp"); err != nil {
		t.Fatalf("update service: %v", err)
	}

	okDuration := int64(42)
	okTime := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	failTime := okTime.Add(time.Second)
	if err := store.SaveResult(ctx, PingResult{ServiceID: created.ID, CheckedAt: okTime, DurationMS: &okDuration, OK: true}); err != nil {
		t.Fatalf("save ok result: %v", err)
	}
	if err := store.SaveResult(ctx, PingResult{ServiceID: created.ID, CheckedAt: failTime, OK: false, Error: "down"}); err != nil {
		t.Fatalf("save failed result: %v", err)
	}

	history, err := store.History(ctx, created.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 || !history[0].OK || history[1].OK || history[1].Error != "down" {
		t.Fatalf("unexpected history: %#v", history)
	}

	if err := store.DeleteService(ctx, created.ID); err != nil {
		t.Fatalf("delete service: %v", err)
	}
	history, err = store.History(ctx, created.ID)
	if err != nil {
		t.Fatalf("history after delete: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history after delete = %d, want 0", len(history))
	}
}

func TestStoreErrorPaths(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	must(t, store.Migrate(ctx))

	if err := store.exec(ctx, "not valid sql"); err == nil {
		t.Fatal("exec expected SQL error")
	}
	var rows []struct {
		ID int `json:"id"`
	}
	if err := store.query(ctx, "not valid sql", &rows); err == nil {
		t.Fatal("query expected SQL error")
	}
	if err := store.query(ctx, "SELECT 1 AS id WHERE 0;", &rows); err != nil {
		t.Fatalf("empty query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty query rows = %d", len(rows))
	}

	must(t, store.CreateService(ctx, "bad-time", "https://example.com", "http"))
	service := mustServices(t, store)[0]
	must(t, store.exec(ctx, "INSERT INTO ping_results (service_id, checked_at, ok, error) VALUES ("+strconvID(service.ID)+", 'bad-time', 0, 'bad');"))
	if _, err := store.History(ctx, service.ID); err == nil {
		t.Fatal("history expected timestamp parse error")
	}

	badStore := NewStore(filepath.Join(t.TempDir(), "missing", "test.db"))
	if err := badStore.Migrate(ctx); err == nil {
		t.Fatal("migrate expected path error")
	}
	if _, err := NewApp(badStore); err == nil {
		t.Fatal("NewApp expected migrate error")
	}
}

func TestSnapshotsAndPayloadUseLastOK(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	services, err := app.store.ListServices(ctx)
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	serviceID := services[0].ID

	okDuration := int64(25)
	okTime := time.Date(2026, 5, 29, 11, 0, 0, 0, time.UTC)
	failTime := okTime.Add(time.Second)
	must(t, app.store.SaveResult(ctx, PingResult{ServiceID: serviceID, CheckedAt: okTime, DurationMS: &okDuration, OK: true}))
	must(t, app.store.SaveResult(ctx, PingResult{ServiceID: serviceID, CheckedAt: failTime, OK: false, Error: "timeout"}))

	snapshots, err := app.snapshots(ctx)
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	if snapshots[0].Latest == nil || snapshots[0].Latest.OK {
		t.Fatalf("latest should be failed result: %#v", snapshots[0].Latest)
	}
	if snapshots[0].LastOK == nil || !snapshots[0].LastOK.CheckedAt.Equal(okTime) {
		t.Fatalf("last OK should be positive result: %#v", snapshots[0].LastOK)
	}

	payload, err := app.snapshotPayload(ctx)
	if err != nil {
		t.Fatalf("snapshot payload: %v", err)
	}
	var decoded struct {
		Services []ServiceSnapshot `json:"services"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded.Services[0].LastOK == nil {
		t.Fatal("payload does not include lastOk")
	}
}

func TestHandlers(t *testing.T) {
	app := newTestApp(t)

	assertStatus(t, app.handleIndex, http.MethodGet, "/", nil, http.StatusOK)
	assertStatus(t, app.handleIndex, http.MethodGet, "/missing", nil, http.StatusNotFound)
	assertStatus(t, app.handleIndex, http.MethodPost, "/", nil, http.StatusMethodNotAllowed)
	assertStatus(t, app.handleFavicon, http.MethodGet, "/favicon.svg", nil, http.StatusOK)
	assertStatus(t, app.handleFavicon, http.MethodPost, "/favicon.svg", nil, http.StatusMethodNotAllowed)
	assertStatus(t, app.handleEvents, http.MethodPost, "/events", nil, http.StatusMethodNotAllowed)
	assertStatus(t, app.handleCreateService, http.MethodGet, "/services", nil, http.StatusSeeOther)
	assertStatus(t, app.handleCreateService, http.MethodPut, "/services", nil, http.StatusMethodNotAllowed)
	assertStatus(t, app.handleCreateService, http.MethodPost, "/services", url.Values{
		"target": {"bad target"},
		"method": {"http"},
	}, http.StatusSeeOther)

	rec := assertStatus(t, app.handleCreateService, http.MethodPost, "/services", url.Values{
		"target": {"example.net"},
		"method": {"http"},
	}, http.StatusSeeOther)
	if rec.Header().Get("Location") != "/" {
		t.Fatalf("create redirect location = %q", rec.Header().Get("Location"))
	}

	services, err := app.store.ListServices(context.Background())
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	id := services[len(services)-1].ID
	assertStatus(t, app.handleServiceAction, http.MethodGet, "/services/1/update", nil, http.StatusMethodNotAllowed)
	assertStatus(t, app.handleServiceAction, http.MethodPost, "/services/bad/update", nil, http.StatusNotFound)
	assertStatus(t, app.handleServiceAction, http.MethodPost, "/services/1", nil, http.StatusNotFound)
	assertStatus(t, app.handleServiceAction, http.MethodPost, "/services/1/unknown", nil, http.StatusNotFound)
	assertStatus(t, app.handleServiceAction, http.MethodPost, "/services/"+strconvID(id)+"/update", url.Values{
		"target": {"1.1.1.1"},
		"method": {"icmp"},
	}, http.StatusSeeOther)
	assertStatus(t, app.handleServiceAction, http.MethodPost, "/services/"+strconvID(id)+"/update", url.Values{
		"target": {"https://bad.example"},
		"method": {"icmp"},
	}, http.StatusSeeOther)
	assertStatus(t, app.handleServiceAction, http.MethodPost, "/services/"+strconvID(id)+"/delete", nil, http.StatusSeeOther)
}

func TestCreateServiceLimit(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	for _, svc := range mustServices(t, app.store) {
		must(t, app.store.DeleteService(ctx, svc.ID))
	}
	for i := 0; i < maxServices; i++ {
		target := "https://example-" + strconv.Itoa(i) + ".com"
		must(t, app.store.CreateService(ctx, target, target, "http"))
	}

	rec := assertStatus(t, app.handleCreateService, http.MethodPost, "/services", url.Values{
		"target": {"example-over-limit.com"},
		"method": {"http"},
	}, http.StatusSeeOther)
	if !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Fatalf("limit redirect should include error, got %q", rec.Header().Get("Location"))
	}
	count, err := app.store.CountServices(ctx)
	if err != nil {
		t.Fatalf("count services: %v", err)
	}
	if count != maxServices {
		t.Fatalf("service count after limit create = %d, want %d", count, maxServices)
	}
}

func TestEventsInitialSnapshot(t *testing.T) {
	app := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		app.handleEvents(rec, req)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(rec.Body.String(), "event: snapshot") {
			cancel()
			<-done
			if rec.Code != http.StatusOK {
				t.Fatalf("events status = %d", rec.Code)
			}
			return
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("timed out waiting for initial SSE snapshot: %q", rec.Body.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestPingServiceHTTP(t *testing.T) {
	restoreHTTPClient := replaceHTTPTransport(func(req *http.Request) (*http.Response, error) {
		status := http.StatusNoContent
		if strings.Contains(req.URL.Host, "fail") {
			status = http.StatusInternalServerError
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	defer restoreHTTPClient()

	if err := httpGetPing(context.Background(), "https://ok.example"); err != nil {
		t.Fatalf("httpGetPing ok: %v", err)
	}
	if err := httpGetPing(context.Background(), "https://fail.example"); err == nil {
		t.Fatal("httpGetPing expected status error")
	}
	if err := httpGetPing(context.Background(), "://bad"); err == nil {
		t.Fatal("httpGetPing expected request error")
	}

	result := pingService(context.Background(), Service{ID: 10, Target: "https://ok.example", Method: "http"})
	if !result.OK || result.DurationMS == nil || result.ServiceID != 10 {
		t.Fatalf("unexpected successful ping result: %#v", result)
	}
	result = pingService(context.Background(), Service{ID: 10, Target: "https://ok.example", Method: "unknown"})
	if result.OK || !strings.Contains(result.Error, "unsupported") {
		t.Fatalf("unexpected unsupported ping result: %#v", result)
	}
}

func TestPollOnceStoresResult(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	for _, svc := range mustServices(t, app.store) {
		must(t, app.store.DeleteService(ctx, svc.ID))
	}

	restoreHTTPClient := replaceHTTPTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	defer restoreHTTPClient()

	must(t, app.store.CreateService(ctx, "https://ok.example", "https://ok.example", "http"))

	app.pollOnce(ctx)
	services := mustServices(t, app.store)
	history, err := app.store.History(ctx, services[0].ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || !history[0].OK {
		t.Fatalf("poll history = %#v, want one OK result", history)
	}
}

func TestPollLoopAndBroadcast(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	for _, svc := range mustServices(t, app.store) {
		must(t, app.store.DeleteService(ctx, svc.ID))
	}

	ready := make(chan struct{})
	done := make(chan struct{})
	loopCtx, cancel := context.WithCancel(context.Background())
	go func() {
		close(ready)
		app.pollLoop(loopCtx)
		close(done)
	}()
	<-ready
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollLoop did not stop after context cancel")
	}

	open := make(chan []byte, 1)
	full := make(chan []byte, 1)
	full <- []byte("stale")
	app.clientsMu.Lock()
	app.clients[open] = struct{}{}
	app.clients[full] = struct{}{}
	app.clientsMu.Unlock()
	app.broadcastSnapshot(context.Background())
	select {
	case <-open:
	case <-time.After(time.Second):
		t.Fatal("broadcast did not deliver to open channel")
	}
}

func TestICMPPingCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := icmpPing(ctx, "127.0.0.1")
	if err == nil {
		t.Fatal("icmpPing expected canceled context error")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "test.db"))
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(newTestStore(t))
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

func assertStatus(t *testing.T, handler http.HandlerFunc, method, target string, form url.Values, want int) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s status = %d, want %d; body=%q", method, target, rec.Code, want, rec.Body.String())
	}
	return rec
}

func mustServices(t *testing.T, store *Store) []Service {
	t.Helper()
	services, err := store.ListServices(context.Background())
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	return services
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func strconvID(id int64) string {
	return strconv.FormatInt(id, 10)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func replaceHTTPTransport(fn roundTripFunc) func() {
	original := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fn}
	return func() {
		http.DefaultClient = original
	}
}
