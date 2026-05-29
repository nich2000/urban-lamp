package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/index.html static/app.css static/app.js
var assets embed.FS

const (
	defaultAddr       = ":8080"
	defaultDBPath     = "urban-lamp.db"
	pollInterval      = 5 * time.Second
	requestTimeout    = 3 * time.Second
	longPingThreshold = 800
	maxHistoryPoints  = 60
	maxServices       = 20
)

type Service struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Target string `json:"target"`
	Method string `json:"method"`
}

type PingResult struct {
	ServiceID  int64     `json:"serviceId"`
	CheckedAt  time.Time `json:"checkedAt"`
	DurationMS *int64    `json:"durationMs"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
}

type ServiceSnapshot struct {
	Service Service      `json:"service"`
	Latest  *PingResult  `json:"latest"`
	LastOK  *PingResult  `json:"lastOk"`
	History []PingResult `json:"history"`
}

type PageData struct {
	Services          []ServiceSnapshot
	Methods           []string
	LongPingThreshold int
	MaxServices       int
	Error             string
}

type App struct {
	store     *Store
	tmpl      *template.Template
	clientsMu sync.Mutex
	clients   map[chan []byte]struct{}
}

type Store struct {
	dbPath string
	mu     sync.Mutex
}

func main() {
	dbPath := getenv("URBAN_LAMP_DB", defaultDBPath)
	addr := listenAddr()

	store := NewStore(dbPath)
	app, err := NewApp(store)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go app.pollLoop(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/favicon.svg", app.handleFavicon)
	mux.HandleFunc("/events", app.handleEvents)
	mux.HandleFunc("/services", app.handleCreateService)
	mux.HandleFunc("/services/", app.handleServiceAction)
	mux.Handle("/static/", http.FileServer(http.FS(assets)))

	log.Printf("urban-lamp listening on %s", displayURL(addr))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func NewApp(store *Store) (*App, error) {
	if err := store.Migrate(context.Background()); err != nil {
		return nil, err
	}

	tmpl, err := template.New("index.html").Funcs(template.FuncMap{
		"formatResultTime": formatResultTime,
		"formatTime":       formatTime,
		"serviceWord":      serviceWord,
		"statusClass":      statusClass,
		"statusMessage":    statusMessage,
	}).ParseFS(assets, "templates/index.html")
	if err != nil {
		return nil, err
	}

	app := &App{
		store:   store,
		tmpl:    tmpl,
		clients: make(map[chan []byte]struct{}),
	}

	if err := store.SeedDefaults(context.Background()); err != nil {
		return nil, err
	}

	return app, nil
}

func NewStore(dbPath string) *Store {
	return &Store{dbPath: dbPath}
}

func (s *Store) Migrate(ctx context.Context) error {
	return s.exec(ctx, `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS services (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	target TEXT NOT NULL,
	method TEXT NOT NULL CHECK (method IN ('http', 'icmp')),
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ping_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
	checked_at TEXT NOT NULL,
	duration_ms INTEGER,
	ok INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_ping_results_service_time
	ON ping_results(service_id, checked_at DESC);
`)
}

func (s *Store) SeedDefaults(ctx context.Context) error {
	var rows []struct {
		Count int `json:"count"`
	}
	if err := s.query(ctx, `SELECT COUNT(*) AS count FROM services;`, &rows); err != nil {
		return err
	}
	if len(rows) > 0 && rows[0].Count > 0 {
		return nil
	}
	return s.exec(ctx, `
INSERT INTO services (name, target, method)
VALUES
	('Example HTTP', 'https://example.com', 'http'),
	('Cloudflare DNS', '1.1.1.1', 'icmp');
`)
}

func (s *Store) ListServices(ctx context.Context) ([]Service, error) {
	var services []Service
	err := s.query(ctx, `SELECT id, name, target, method FROM services ORDER BY id;`, &services)
	return services, err
}

func (s *Store) CountServices(ctx context.Context) (int, error) {
	var rows []struct {
		Count int `json:"count"`
	}
	if err := s.query(ctx, `SELECT COUNT(*) AS count FROM services;`, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}

func (s *Store) CreateService(ctx context.Context, name, target, method string) error {
	return s.exec(ctx, fmt.Sprintf(
		`INSERT INTO services (name, target, method) VALUES (%s, %s, %s);`,
		sqlQuote(name), sqlQuote(target), sqlQuote(method),
	))
}

func (s *Store) UpdateService(ctx context.Context, id int64, target, method string) error {
	return s.exec(ctx, fmt.Sprintf(
		`UPDATE services SET name = %s, target = %s, method = %s WHERE id = %d;`,
		sqlQuote(target), sqlQuote(target), sqlQuote(method), id,
	))
}

func (s *Store) DeleteService(ctx context.Context, id int64) error {
	return s.exec(ctx, fmt.Sprintf(`PRAGMA foreign_keys = ON; DELETE FROM services WHERE id = %d;`, id))
}

func (s *Store) SaveResult(ctx context.Context, result PingResult) error {
	duration := "NULL"
	if result.DurationMS != nil {
		duration = strconv.FormatInt(*result.DurationMS, 10)
	}
	return s.exec(ctx, fmt.Sprintf(`
INSERT INTO ping_results (service_id, checked_at, duration_ms, ok, error)
VALUES (%d, %s, %s, %d, %s);`,
		result.ServiceID,
		sqlQuote(result.CheckedAt.Format(time.RFC3339Nano)),
		duration,
		boolToInt(result.OK),
		sqlQuote(result.Error),
	))
}

func (s *Store) History(ctx context.Context, serviceID int64) ([]PingResult, error) {
	var rows []struct {
		ServiceID  int64  `json:"serviceId"`
		CheckedAt  string `json:"checkedAt"`
		DurationMS *int64 `json:"durationMs"`
		OK         int    `json:"ok"`
		Error      string `json:"error"`
	}
	err := s.query(ctx, fmt.Sprintf(`
SELECT service_id AS serviceId, checked_at AS checkedAt, duration_ms AS durationMs, ok, error
FROM (
	SELECT service_id, checked_at, duration_ms, ok, error
	FROM ping_results
	WHERE service_id = %d
	ORDER BY checked_at DESC
	LIMIT %d
)
ORDER BY checked_at ASC;`, serviceID, maxHistoryPoints), &rows)
	if err != nil {
		return nil, err
	}

	results := make([]PingResult, 0, len(rows))
	for _, row := range rows {
		checkedAt, err := time.Parse(time.RFC3339Nano, row.CheckedAt)
		if err != nil {
			return nil, err
		}
		results = append(results, PingResult{
			ServiceID:  row.ServiceID,
			CheckedAt:  checkedAt,
			DurationMS: row.DurationMS,
			OK:         row.OK == 1,
			Error:      row.Error,
		})
	}
	return results, nil
}

func (s *Store) exec(ctx context.Context, sql string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.CommandContext(ctx, "sqlite3", s.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite exec: %w: %s", err, firstLine(string(out)))
	}
	return nil
}

func (s *Store) query(ctx context.Context, sql string, dest any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.CommandContext(ctx, "sqlite3", "-json", s.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite query: %w: %s", err, firstLine(string(out)))
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		out = []byte("[]")
	}
	return json.Unmarshal(out, dest)
}

func sqlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshots, err := a.snapshots(r.Context())
	if err != nil {
		log.Printf("load snapshots: %v", err)
		snapshots = nil
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = a.tmpl.ExecuteTemplate(w, "index.html", PageData{
		Services:          snapshots,
		Methods:           []string{"http", "icmp"},
		LongPingThreshold: longPingThreshold,
		MaxServices:       maxServices,
		Error:             firstNonEmpty(r.URL.Query().Get("error"), errorText(err)),
	})
	if err != nil {
		log.Printf("render index: %v", err)
	}
}

func (a *App) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#14213d"/><path d="M18 38h6l5-14 7 24 5-10h5" fill="none" stroke="#fca311" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/><circle cx="49" cy="18" r="6" fill="#2ec4b6"/></svg>`)
}

func (a *App) handleCreateService(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	method := strings.TrimSpace(r.FormValue("method"))
	target := normalizeTarget(strings.TrimSpace(r.FormValue("target")), method)
	if err := validateService(target, method); err != nil {
		redirectWithError(w, r, err.Error())
		return
	}
	count, err := a.store.CountServices(r.Context())
	if err != nil {
		log.Printf("count services: %v", err)
		redirectWithError(w, r, "Не удалось проверить количество сервисов.")
		return
	}
	if count >= maxServices {
		redirectWithError(w, r, fmt.Sprintf("Можно добавить не больше %d сервисов.", maxServices))
		return
	}

	if err := a.store.CreateService(r.Context(), target, target, method); err != nil {
		log.Printf("create service: %v", err)
		redirectWithError(w, r, "Не удалось добавить сервис.")
		return
	}

	a.broadcastSnapshot(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/services/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}

	idPart := parts[0]
	id, err := strconv.ParseInt(strings.Trim(idPart, "/"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	switch parts[1] {
	case "delete":
		if err := a.store.DeleteService(r.Context(), id); err != nil {
			log.Printf("delete service: %v", err)
			redirectWithError(w, r, "Не удалось удалить сервис.")
			return
		}
	case "update":
		method := strings.TrimSpace(r.FormValue("method"))
		target := normalizeTarget(strings.TrimSpace(r.FormValue("target")), method)
		if err := validateService(target, method); err != nil {
			redirectWithError(w, r, err.Error())
			return
		}
		if err := a.store.UpdateService(r.Context(), id, target, method); err != nil {
			log.Printf("update service: %v", err)
			redirectWithError(w, r, "Не удалось обновить сервис.")
			return
		}
	default:
		http.NotFound(w, r)
		return
	}

	a.broadcastSnapshot(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func validateService(target, method string) error {
	if target == "" {
		return errors.New("target is required")
	}
	switch method {
	case "http":
		parsed, err := url.ParseRequestURI(target)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return errors.New("http target must be a full URL")
		}
	case "icmp":
		if strings.ContainsAny(target, "/: \t\r\n") {
			return errors.New("icmp target must be a host or IP without scheme")
		}
	default:
		return errors.New("method must be http or icmp")
	}
	return nil
}

func normalizeTarget(target, method string) string {
	if method == "http" && target != "" && !strings.Contains(target, "://") {
		return "https://" + target
	}
	return target
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("15:04:05")
}

func formatResultTime(result *PingResult) string {
	if result == nil {
		return "—"
	}
	return formatTime(result.CheckedAt)
}

func serviceWord(count int) string {
	mod100 := count % 100
	if mod100 >= 11 && mod100 <= 14 {
		return "сервисов"
	}
	switch count % 10 {
	case 1:
		return "сервис"
	case 2, 3, 4:
		return "сервиса"
	default:
		return "сервисов"
	}
}

func statusClass(result *PingResult) string {
	if result == nil {
		return "unknown"
	}
	if !result.OK {
		return "down"
	}
	if result.DurationMS != nil && *result.DurationMS >= longPingThreshold {
		return "slow"
	}
	return "ok"
}

func statusMessage(result *PingResult) string {
	switch statusClass(result) {
	case "ok":
		return "доступен"
	case "slow":
		return "долгий пинг"
	case "down":
		return "нет ответа"
	default:
		return "ожидание"
	}
}

func redirectWithError(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(message), http.StatusSeeOther)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return "Данные временно недоступны. Интерфейс продолжает работать."
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 4)
	a.clientsMu.Lock()
	a.clients[ch] = struct{}{}
	a.clientsMu.Unlock()
	defer func() {
		a.clientsMu.Lock()
		delete(a.clients, ch)
		a.clientsMu.Unlock()
		close(ch)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if payload, err := a.snapshotPayload(r.Context()); err == nil {
		fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", payload)
		flusher.Flush()
	} else {
		log.Printf("initial SSE snapshot: %v", err)
		fmt.Fprintf(w, "event: app-error\ndata: %q\n\n", "Данные временно недоступны.")
		flusher.Flush()
	}

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (a *App) pollLoop(ctx context.Context) {
	a.pollOnce(ctx)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollOnce(ctx)
		}
	}
}

func (a *App) pollOnce(ctx context.Context) {
	services, err := a.store.ListServices(ctx)
	if err != nil {
		log.Printf("list services: %v", err)
		return
	}

	var wg sync.WaitGroup
	for _, svc := range services {
		svc := svc
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := pingService(ctx, svc)
			if err := a.store.SaveResult(ctx, result); err != nil {
				log.Printf("save ping result for %s: %v", svc.Name, err)
			}
		}()
	}
	wg.Wait()

	a.broadcastSnapshot(ctx)
}

func pingService(ctx context.Context, svc Service) PingResult {
	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var err error
	switch svc.Method {
	case "http":
		err = httpGetPing(pingCtx, svc.Target)
	case "icmp":
		err = icmpPing(pingCtx, svc.Target)
	default:
		err = fmt.Errorf("unsupported method %q", svc.Method)
	}

	elapsed := int64(time.Since(start).Milliseconds())
	result := PingResult{
		ServiceID: svc.ID,
		CheckedAt: time.Now().UTC(),
		OK:        err == nil,
	}
	if err == nil {
		result.DurationMS = &elapsed
	} else {
		result.Error = err.Error()
	}
	return result
}

func httpGetPing(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	return nil
}

func icmpPing(ctx context.Context, host string) error {
	args := []string{"-c", "1"}
	if runtime.GOOS == "darwin" {
		args = append(args, "-W", strconv.Itoa(int(requestTimeout.Milliseconds())))
	} else {
		args = append(args, "-W", strconv.Itoa(int(requestTimeout.Seconds())))
	}
	args = append(args, host)

	out, err := exec.CommandContext(ctx, "ping", args...).CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(firstLine(msg))
	}
	return nil
}

func (a *App) snapshots(ctx context.Context) ([]ServiceSnapshot, error) {
	services, err := a.store.ListServices(ctx)
	if err != nil {
		return nil, err
	}

	snapshots := make([]ServiceSnapshot, 0, len(services))
	for _, svc := range services {
		history, err := a.store.History(ctx, svc.ID)
		if err != nil {
			return nil, err
		}
		snapshot := ServiceSnapshot{Service: svc, History: history}
		if len(history) > 0 {
			latest := history[len(history)-1]
			snapshot.Latest = &latest
		}
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].OK {
				lastOK := history[i]
				snapshot.LastOK = &lastOK
				break
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (a *App) snapshotPayload(ctx context.Context) ([]byte, error) {
	snapshots, err := a.snapshots(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Services          []ServiceSnapshot `json:"services"`
		LongPingThreshold int               `json:"longPingThreshold"`
	}{
		Services:          snapshots,
		LongPingThreshold: longPingThreshold,
	})
}

func (a *App) broadcastSnapshot(ctx context.Context) {
	payload, err := a.snapshotPayload(ctx)
	if err != nil {
		log.Printf("build snapshot: %v", err)
		return
	}

	a.clientsMu.Lock()
	defer a.clientsMu.Unlock()
	for ch := range a.clients {
		select {
		case ch <- payload:
		default:
		}
	}
}

func firstLine(s string) string {
	line := strings.Split(strings.TrimSpace(s), "\n")[0]
	if len(line) > 180 {
		return line[:180]
	}
	return line
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func getenv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func listenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("URBAN_LAMP_ADDR")); addr != "" {
		return addr
	}
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if strings.Contains(port, ":") {
			return port
		}
		return ":" + port
	}
	return defaultAddr
}

func displayURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
