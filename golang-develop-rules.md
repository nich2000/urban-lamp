# Go Engineering Rules for Codex

Документ предназначен для Codex/AI-агента, который пишет, рефакторит и готовит к выкатке проекты на Go.
Цель: получать простой, поддерживаемый, идиоматичный Go-код с предсказуемой структурой проекта, тестами, проверками и release-процессом.

## 0. Приоритеты

При конфликте правил используй такой порядок:

1. Корректность и безопасность данных.
2. Простота и читаемость.
3. Идиоматичность Go.
4. Наблюдаемость и диагностируемость.
5. Производительность, только если есть измеримая причина.
6. Красота архитектуры — только если она уменьшает сложность, а не добавляет её.

Главный принцип: не тащи framework-style архитектуру без необходимости. В Go хорошая архитектура обычно растёт из маленьких пакетов, явных интерфейсов и простых зависимостей.

### 0.1 Scope работы

Перед любыми командами определи рабочий scope:

- найди ближайший `go.mod` для изменяемого кода;
- в monorepo, workspace или каталоге с nested modules работай внутри конкретного модуля, если задача не требует большего;
- не форматируй, не tidy'ь и не тестируй весь родительский каталог, если он не является целевым Go-модулем;
- generated, vendor, old/backup и сторонние каталоги трогай только при явном требовании или если они входят в контракт задачи;
- если scope неясен и есть риск затронуть чужие изменения, сначала уточни.

---

## 1. Базовые правила Go-кода

### 1.1 Форматирование

Для изменённых Go-файлов обязательно:

```bash
gofmt -w path/to/file.go
```

Для всего целевого модуля, если широкий форматный diff приемлем:

```bash
go list -f '{{.Dir}}' ./... | xargs gofmt -w
```

Правила:

- Не спорь с `gofmt`.
- Не выравнивай код вручную, если это не делает `gofmt`.
- Импорты сортируй через `goimports` или IDE.
- В документации, Makefile и CI предпочитай `gofmt -w`/`gofmt -l`, а не `go fmt`, чтобы явно контролировать список файлов.
- В PR не должно быть форматных изменений, смешанных с логическими, если это можно разделить.

### 1.2 Имена

Используй короткие имена там, где область видимости маленькая:

```go
for i, item := range items {
    _ = i
    _ = item
}
```

Используй более говорящие имена там, где объект живёт долго:

```go
type TelemetryPacketReader struct {
    source io.Reader
}
```

Правила:

- Не используй Java-style имена: `TelemetryPacketReaderInterface`, `BaseServiceImpl`, `ManagerFactory` без крайней необходимости.
- Не добавляй тип в имя переменной, если он очевиден: `userMap map[string]User` хуже, чем `users map[string]User`.
- Интерфейс с одним методом обычно называется по действию: `Reader`, `Writer`, `Closer`, `Validator`, `Encoder`.
- Экспортируемые имена должны быть понятны вне пакета.
- Пакеты называй коротко, строчными буквами, без подчёркиваний: `telemetry`, `serial`, `config`, `storage`.

### 1.3 Комментарии

Комментируй:

- экспортируемые сущности, которые являются публичным API пакета или требуют пояснения;
- неочевидные бизнес-правила;
- протоколы, бинарные форматы, magic numbers;
- причины нестандартных решений.

Не комментируй очевидное:

```go
// increment i
i++
```

Хороший комментарий объясняет «почему», а не «что»:

```go
// Keep the timeout below the radio retry window, otherwise the base station
// may resend a packet while the previous ACK is still in flight.
const ackTimeout = 80 * time.Millisecond
```

Doc comment для экспортируемого имени начинается с этого имени:

```go
// PacketReader reads telemetry packets from a byte stream.
type PacketReader struct {}
```

Не добавляй бессмысленный комментарий только ради комментария. Если exported symbol очевиден и используется только внутри внутреннего пакета, лучше оставить код чистым, чем писать дублирующий текст.

---

## 2. Ошибки

### 2.1 Возврат ошибок

В Go ошибки — обычные значения. Не прячь их.

```go
if err != nil {
    return fmt.Errorf("read telemetry packet: %w", err)
}
```

Правила:

- Добавляй контекст при передаче ошибки выше.
- Используй `%w`, если вызывающий код может проверять ошибку через `errors.Is` или `errors.As`.
- Не логируй и не возвращай одну и ту же ошибку на каждом уровне. Выбери границу логирования.
- Не используй `panic` для штатных ошибок ввода, сети, файлов, БД, внешних сервисов.
- `panic` допустим для невозможного состояния, ошибки программиста или fail-fast инициализации, если приложение не может работать дальше.

### 2.2 Sentinel errors

```go
var ErrPacketChecksum = errors.New("packet checksum mismatch")
```

Используй sentinel errors, если вызывающий код должен принимать решение по типу ошибки.

```go
if errors.Is(err, ErrPacketChecksum) {
    metrics.BadChecksum.Add(1)
    continue
}
```

### 2.3 Typed errors

Используй типизированные ошибки, если нужны дополнительные поля:

```go
type ProtocolError struct {
    Offset int
    Reason string
}

func (e *ProtocolError) Error() string {
    return fmt.Sprintf("protocol error at offset %d: %s", e.Offset, e.Reason)
}
```

---

## 3. Context, timeout, cancellation

Правила:

- `context.Context` передавай первым аргументом.
- Не храни `context.Context` в struct, кроме специальных случаев.
- Все сетевые, файловые, RPC, DB и long-running операции должны поддерживать отмену или timeout.
- Не передавай `nil` context. Используй `context.Background()` или `context.TODO()`.
- В большинстве случаев timeout задаётся на boundary уровня: HTTP/gRPC handler, CLI command, worker loop, message handler.
- Внутренние функции обычно используют уже полученный `ctx`, а не создают новый `context.WithTimeout` на каждом слое.

```go
func (s *Service) SendPacket(ctx context.Context, packet Packet) error {
    // Use this only if SendPacket owns the external I/O boundary.
    ctx, cancel := context.WithTimeout(ctx, s.sendTimeout)
    defer cancel()

    return s.transport.Send(ctx, packet)
}
```

Плохо без причины:

```go
func (r *Repository) Save(ctx context.Context, packet Packet) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    return r.db.Save(ctx, packet)
}
```

Такой код создаёт каскад несогласованных timeout'ов. Добавляй внутренний timeout только если слой реально владеет отдельной внешней операцией и это описано в контракте.

---

## 4. Concurrency

### 4.1 Goroutine lifecycle

Каждая goroutine должна иметь понятный жизненный цикл.

Обязательно:

- понятный owner;
- способ остановки;
- обработка ошибки;
- отсутствие goroutine leaks;
- закрытие каналов только отправителем/owner'ом.

Плохо:

```go
go func() {
    for {
        doWork()
    }
}()
```

Лучше:

```go
go func() {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := doWork(ctx); err != nil {
                logger.Warn("background work failed", "error", err)
            }
        }
    }
}()
```

### 4.2 Каналы

Используй каналы для передачи владения или событий, а не как замену всем структурам данных.

Правила:

- Канал должен иметь понятный тип сообщения.
- Закрывай канал только там, где создаёшь/контролируешь отправку.
- Не используй `time.After` в циклах с высокой частотой; используй `time.Timer` или `time.Ticker`.
- Для shared state часто проще `sync.Mutex`, чем хитрая сеть каналов.

### 4.3 Race detector

Перед выкаткой или перед крупным merge запускай для целевого модуля, если это технически возможно:

```bash
go test -race ./...
```

Race detector находит только те гонки, которые реально проявились во время выполнения тестов. Поэтому для конкурентного кода нужны тесты, которые создают нагрузку и проходят реальные code paths.

Если `-race` невозможен из-за платформы, CGO, времени выполнения или внешних зависимостей, явно напиши причину и какие конкурентные участки остались без проверки.

---

## 5. Структура проекта

### 5.1 Общий принцип

В Go нет единой «официальной» структуры для всех проектов. Структура должна соответствовать размеру проекта.

Не создавай заранее директории `pkg`, `internal`, `api`, `deploy`, `configs`, `scripts`, если они пустые или не нужны.

Начинай просто, усложняй только по мере роста.

Если проект уже имеет устойчивую структуру, следуй ей. Не переносить код в рекомендуемую структуру из этого документа ради соответствия шаблону.

### 5.2 Минимальная структура для маленького приложения

```text
myapp/
  go.mod
  go.sum
  main.go
  config.go
  service.go
  service_test.go
  README.md
```

Подходит для маленькой утилиты, прототипа, одного сервиса без сложной доменной модели.

### 5.3 Feature-first структура для бизнес-сервиса

Для бизнес-систем чаще предпочтителен feature-first подход: код группируется вокруг предметной области, а не вокруг технических слоёв.

```text
myapp/
  go.mod
  go.sum
  README.md
  Makefile

  cmd/
    myapp/
      main.go

  internal/
    telemetry/
      packet.go
      service.go
      storage.go
    selective/
      service.go
    ack/
      manager.go
    radio/
      protocol.go
    config/
      config.go
    app/
      app.go

  migrations/
  deployments/
  docs/
```

Внутри feature-пакета можно держать service, protocol, storage adapter и tests рядом, если это уменьшает навигацию и не создаёт циклических зависимостей.

### 5.4 Layer-first структура для простого сервиса/демона

```text
myapp/
  go.mod
  go.sum
  README.md
  Makefile
  .golangci.yml
  .gitignore

  cmd/
    myapp/
      main.go

  internal/
    app/
      app.go
    config/
      config.go
    transport/
      http/
        server.go
      serial/
        port.go
    domain/
      packet.go
      service.go
    storage/
      postgres.go
    observability/
      logger.go
      metrics.go

  migrations/
  deployments/
  scripts/
  docs/
```

Это пример, а не обязательный шаблон. `domain`, `transport` и `storage` допустимы, если проект действительно проще читать по слоям.

Назначение:

- `cmd/<binary>/main.go` — точка входа, wiring зависимостей, запуск приложения.
- `internal/` — код, который нельзя импортировать из других модулей.
- `internal/app` — сборка приложения, lifecycle, graceful shutdown.
- `internal/config` — конфигурация и её валидация.
- `internal/domain` — бизнес-типы и бизнес-логика, если выбран layer-first подход.
- `internal/transport` — HTTP, gRPC, serial, NATS, CLI и другие внешние интерфейсы.
- `internal/storage` — БД, файлы, внешние хранилища.
- `migrations/` — миграции БД.
- `deployments/` — Docker, compose, k8s, systemd, Helm.
- `scripts/` — вспомогательные скрипты.
- `docs/` — документация.

### 5.5 Когда использовать `pkg/`

Используй `pkg/` только если код действительно предназначен для внешнего импорта другими проектами.

Не используй `pkg/` как мусорную папку для «общего кода».

Плохо:

```text
pkg/utils
pkg/common
pkg/helpers
```

Лучше:

```text
internal/checksum
internal/protocol
internal/clock
```

### 5.6 Package boundaries

Правила:

- Пакет должен иметь одну понятную ответственность.
- Не создавай пакет ради одного файла, если нет смысловой границы.
- Не называй пакеты `common`, `utils`, `helpers`.
- Избегай циклических зависимостей архитектурно, а не костылями.
- Доменный слой не должен импортировать transport/storage, если хочешь сохранить чистую бизнес-логику.

---

## 6. Архитектура Go-сервиса

### 6.1 main.go должен быть скучным

`main.go` не должен содержать бизнес-логику.

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cfg, err := config.Load()
    if err != nil {
        log.Fatal(err)
    }

    app, err := app.New(cfg)
    if err != nil {
        log.Fatal(err)
    }

    if err := app.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

### 6.2 Интерфейсы объявляй на стороне потребителя

Плохо:

```go
package storage

type Storage interface {
    Save(context.Context, Packet) error
}
```

Лучше:

```go
package service

type PacketStore interface {
    SavePacket(context.Context, Packet) error
}
```

Так сервис описывает минимально нужный контракт, а не зависит от большого интерфейса поставщика.

Не создавай интерфейс заранее, если есть только одна реализация и нет реальной потребности у потребителя или теста.

Плохо:

```go
type UserService interface {
    Create(context.Context, User) error
}

type service struct {}
```

Лучше начать с конкретного типа:

```go
type Service struct {}
```

Интерфейс появляется тогда, когда он уменьшает связанность: у потребителя нужен узкий контракт, есть несколько реализаций или тест без интерфейса становится неоправданно тяжёлым.

### 6.3 Не делай преждевременную Clean Architecture

Допустимо использовать слои внутри feature-пакета или всего небольшого сервиса:

- transport;
- service/domain;
- storage/client;
- config;
- observability.

Но не создавай `usecase`, `interactor`, `entity`, `repository`, `gateway`, если проект от этого не становится проще.

### 6.4 Dependency injection

В Go обычно достаточно явного конструктора:

```go
type Service struct {
    store  PacketStore
    logger *slog.Logger
}

func NewService(store PacketStore, logger *slog.Logger) *Service {
    return &Service{store: store, logger: logger}
}
```

Не подключай DI-фреймворк без сильной причины.

### 6.5 Package globals

Запрещены package-level mutable singleton объекты без явной причины.

Плохо:

```go
var logger = slog.Default()
var cfg Config
var db *sql.DB
```

Такие globals усложняют тесты, порядок инициализации, параллельные запуски и graceful shutdown. Передавай зависимости явно через constructor, `main`/`app` wiring или небольшой runtime container.

Допустимы:

- immutable constants;
- package-level sentinel errors;
- stateless helpers;
- `sync.Once` для действительно process-wide ресурса, если это описано и покрыто тестами.

---

## 7. Конфигурация

Правила:

- Статическая конфигурация читается на старте.
- После чтения конфигурация валидируется.
- Runtime-код получает уже типизированный `Config`, а не читает env напрямую.
- Если нужны hot reload, feature flags или ротация credentials, выдели это отдельным механизмом с owner'ом, синхронизацией и наблюдаемостью.
- Секреты не логируются.
- Значения по умолчанию должны быть явными.

```go
type Config struct {
    HTTPAddr     string
    DatabaseURL  string
    ReadTimeout  time.Duration
    WriteTimeout time.Duration
}

func (c Config) Validate() error {
    if c.HTTPAddr == "" {
        return errors.New("http addr is required")
    }
    if c.DatabaseURL == "" {
        return errors.New("database url is required")
    }
    return nil
}
```

---

## 8. Логирование и observability

### 8.1 Structured logging

Для новых проектов используй `log/slog`, если нет существующего стандарта команды.

```go
logger.Info("packet received",
    "device_id", packet.DeviceID,
    "session", packet.SessionID,
    "size", len(packet.Payload),
)
```

Правила:

- Логи должны быть структурированными.
- Не логируй секреты, токены, пароли, приватные ключи.
- Ошибку логируй как поле `error`.
- Не смешивай ключи ошибок: не используй попеременно `err`, `error`, `e`.
- Для устойчивых идентификаторов договорись об именах и держи их одинаковыми: `request_id`, `device_id`, `session_id`, `packet_id`.
- Не превращай логи в основной механизм бизнес-логики.
- Для hot path учитывай стоимость логирования.

### 8.2 Метрики

Для сервисов добавляй метрики там, где есть эксплуатационная ценность:

- количество входящих сообщений;
- количество ошибок по типам;
- latency внешних вызовов;
- retry count;
- queue length;
- dropped messages;
- reconnect count;
- uptime/build info.

### 8.3 Health/readiness

Для сервисов добавляй:

- `/healthz` — процесс жив;
- `/readyz` — сервис готов принимать нагрузку;
- `/metrics` — если используется Prometheus.

---

## 9. Тестирование

### 9.1 Unit tests

Запуск:

```bash
go test ./...
```

Правила:

- Тесты должны быть быстрыми и детерминированными.
- Используй table-driven tests для набора похожих кейсов.
- Покрывай happy path, edge cases и error path.
- Не тестируй приватные детали реализации, если это ломает рефакторинг.
- Не используй sleep в тестах без крайней необходимости.

Пример:

```go
func TestChecksum(t *testing.T) {
    tests := []struct {
        name string
        data []byte
        want uint32
    }{
        {name: "empty", data: nil, want: 0},
        {name: "payload", data: []byte{1, 2, 3}, want: 6},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := Checksum(tt.data)
            if got != tt.want {
                t.Fatalf("Checksum() = %d, want %d", got, tt.want)
            }
        })
    }
}
```

### 9.2 Integration tests

Интеграционные тесты отделяй build tags или отдельным пакетом.

```go
//go:build integration
```

Запуск:

```bash
go test -tags=integration ./...
```

Правила:

- внешние сервисы поднимай явно: docker compose, testcontainers, локальный emulator или documented staging dependency;
- каждый тест должен сам готовить данные и выполнять cleanup;
- используй timeouts через context или test-level deadline;
- не требуй production-секреты для локального запуска;
- если тесты зависят от сети или тяжёлого окружения, отдели их от обычного `go test ./...`.

### 9.3 Race tests

```bash
go test -race ./...
```

Особенно важно для:

- goroutines;
- maps;
- shared buffers;
- caches;
- reconnect loops;
- ack/retry managers;
- background workers.

### 9.4 Coverage

```bash
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Coverage — не самоцель. Важнее покрыть критичные ветки, ошибки и конкурентные сценарии.

### 9.5 Fuzzing

Используй fuzzing для парсеров, бинарных протоколов, декодеров, валидаторов входных данных.

```bash
go test -run=^$ -fuzz=Fuzz -fuzztime=30s ./...
```

Для CI и Codex-задач fuzzing запускай только с ограниченным `-fuzztime`. Долгий fuzzing, расширение corpus и анализ найденных inputs делай отдельной задачей.

Особенно полезно для:

- packet decoder;
- checksum/CRC;
- JSON/binary parsing;
- URL/input validation;
- протоколов поверх UART/TCP/UDP.

---

## 10. Работа с зависимостями

### 10.1 Go modules

Go-модуль должен иметь `go.mod` в своём корне. В monorepo таких модулей может быть несколько.

```bash
go mod tidy
```

Правила:

- Не коммить лишние зависимости.
- После добавления/удаления импортов запускай `go mod tidy` в затронутом модуле.
- Если менялись только файлы без изменения imports/dependencies, `go mod tidy` не обязателен.
- Не используй `replace` в production без комментария и причины.
- Не обновляй все зависимости случайно в одном PR вместе с бизнес-логикой.
- Большие dependency updates делай отдельным PR.

### 10.2 Vendor

`vendor/` используй только если проекту это действительно нужно:

- offline build;
- строгая воспроизводимость;
- корпоративные ограничения;
- embedded/field deployments без доступа к интернету.

Если используется vendor:

```bash
go mod vendor
go test -mod=vendor ./...
```

---

## 11. Lint/static analysis

Минимальный набор перед commit для затронутого модуля:

```bash
go list -f '{{.Dir}}' ./... | xargs gofmt -w
go vet ./...
go test ./...
```

Рекомендуемый набор, если инструменты установлены и проект их использует:

```bash
golangci-lint run ./...
govulncheck ./...
go test -race ./...
```

Пример `.golangci.yml` для golangci-lint v2:

```yaml
version: "2"

run:
  timeout: 5m

linters:
  enable:
    - govet
    - staticcheck
    - ineffassign
    - errcheck
    - unused
    - misspell
    - bodyclose
    - noctx
    - gosec

formatters:
  enable:
    - gofmt
    - goimports

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

Правила:

- Не включай все линтеры без разбора.
- Лучше меньше линтеров, но с обязательным прохождением в CI.
- Исключения должны быть точечными и объяснёнными.
- Не отключай линтер глобально ради одной спорной строки.
- Версию golangci-lint фиксируй в CI, devcontainer, tool install script или документации проекта.
- Если проект уже использует golangci-lint v1 config, не мигрируй его на v2 в одном PR с бизнес-изменением.

---

## 12. Безопасность

### 12.1 Vulnerability scan

Перед выкаткой, если инструмент доступен и сеть/кэш модулей позволяют:

```bash
govulncheck ./...
```

Если найдена уязвимость:

1. Проверь, достижима ли она из твоего кода.
2. Обнови зависимость или Go toolchain.
3. Если обновление невозможно, опиши mitigation.
4. Не игнорируй результат молча.

### 12.2 Secrets

Запрещено:

- хранить `.env` с production-секретами в git;
- логировать токены;
- передавать секреты через build args в Docker так, чтобы они остались в слоях;
- хранить приватные ключи в репозитории.

### 12.3 Input validation

Любой внешний ввод должен валидироваться:

- HTTP request;
- serial/UART packet;
- UDP/TCP packet;
- NATS message;
- CLI argument;
- config/env;
- файл с SD/диска.

---

## 13. Подготовка к выкатке

### 13.1 Локальный pre-release checklist

Перед merge/release выполни обязательный минимум в целевом модуле:

```bash
go list -f '{{.Dir}}' ./... | xargs gofmt -w
go vet ./...
go test ./...
```

Если проект использует эти инструменты и они доступны локально, дополнительно выполни:

```bash
go mod tidy
golangci-lint run ./...
go test -race ./...
govulncheck ./...
```

Если любая проверка не запускается из-за окружения, сети, отсутствующего инструмента или внешнего сервиса, явно опиши причину и оставшийся риск.

Если проект использует Docker:

```bash
docker build -t myapp:local .
docker run --rm myapp:local --version
```

Если проект использует миграции:

```bash
# Проверить migrate up/down на тестовой базе.
```

Если проект использует API:

```bash
# Проверить backward compatibility контрактов.
```

### 13.2 Build flags

Production build:

```bash
go build \
  -trimpath \
  -ldflags "-s -w -X ${VERSION_PKG}.version=${VERSION} -X ${VERSION_PKG}.commit=${COMMIT} -X ${VERSION_PKG}.date=${DATE}" \
  -o bin/myapp ./cmd/myapp
```

Пояснения:

- `-trimpath` убирает локальные пути из бинарника и помогает воспроизводимости.
- `-ldflags -X` позволяет встроить версию, commit и дату сборки. `${VERSION_PKG}` должен указывать на реальный import path пакета, где объявлены переменные версии.
- `-s -w` уменьшает размер бинарника, но может ухудшить отладку. Не применяй бездумно, если нужны полноценные debug symbols.
- `CGO_ENABLED=0` используй только если проект гарантированно не зависит от CGO. Он ломает sqlite через `mattn/go-sqlite3`, `librdkafka`, часть USB/serial библиотек и любые bindings к native libraries.

### 13.3 Version endpoint / command

Каждый сервис или CLI должен уметь показать версию:

```bash
myapp --version
```

Для HTTP-сервиса полезен endpoint:

```text
GET /version
```

Минимум информации:

- version/tag;
- git commit;
- build date;
- Go version;
- dirty/clean build, если доступно.

### 13.4 Graceful shutdown

Сервис должен корректно обрабатывать `SIGINT`/`SIGTERM`:

- остановить приём новых запросов;
- завершить активные операции в пределах timeout;
- закрыть соединения с БД, брокерами, serial port, файлами;
- остановить background workers;
- сбросить буферы и метрики, если нужно.

### 13.5 Database migrations

Правила:

- Миграции версионируются в git.
- Миграции должны быть идемпотентны настолько, насколько позволяет инструмент.
- Перед выкаткой проверь `up` на копии production-like базы.
- Для опасных миграций подготовь rollback/forward-fix план.
- Не делай тяжёлую блокирующую миграцию одновременно с выкладкой кода, если таблицы большие.

### 13.6 Config compatibility

Перед выкаткой проверь:

- все новые env/config переменные документированы;
- есть значения по умолчанию или явная ошибка запуска;
- старые конфиги не ломаются без причины;
- секреты заведены в окружении deployment'а;
- staging и production используют ожидаемые значения.

### 13.7 Backward compatibility

Проверь совместимость:

- HTTP/gRPC API;
- NATS topics/payloads;
- бинарные протоколы;
- схемы БД;
- форматы файлов;
- config/env;
- CLI flags.

Если совместимость нарушается, нужен migration plan.

---

## 14. Docker для Go

Пример multi-stage Dockerfile:

```dockerfile
ARG GO_VERSION=1.24
ARG ALPINE_VERSION=3.21

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -o /out/myapp ./cmd/myapp

FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H appuser
USER appuser
COPY --from=builder /out/myapp /usr/local/bin/myapp
ENTRYPOINT ["/usr/local/bin/myapp"]
```

Правила:

- Не запускай приложение под root, если не требуется.
- Используй multi-stage build.
- Кэшируй `go mod download` отдельно от копирования исходников.
- Не клади секреты внутрь image.
- Добавь `HEALTHCHECK`, если это уместно для runtime.
- Версии base images выбирай по `go.mod`/`toolchain` и политике проекта; не обновляй их случайно вместе с бизнес-логикой.
- Добавляй `CGO_ENABLED=0` только если проект гарантированно не зависит от CGO.
- Если CGO включён, runtime image должен содержать совместимые libc и нужные shared libraries.
- Для scratch/distroless/alpine проверь CA certificates, timezone и DNS requirements.

---

## 15. CI/CD

Минимальный pipeline:

1. Checkout.
2. Setup Go exact version from `go.mod`/`toolchain`.
3. Cache Go modules/build cache.
4. `go mod tidy` check, если проект уже использует tidy-check в CI.
5. `gofmt` check.
6. `go vet ./...`.
7. `golangci-lint run ./...`, если проект использует golangci-lint.
8. `go test ./...`.
9. `go test -race ./...` для main/master/nightly или обязательных PR.
10. `govulncheck ./...`, если pipeline имеет доступ к нужным модулям/кэшу.
11. Build artifact.
12. Docker image build/push, если нужно.
13. Release notes/checksums, если релиз.

`go mod tidy` check:

```bash
go mod tidy
git diff --exit-code go.mod go.sum
```

Выполняй этот check отдельно для каждого Go-модуля, который входит в scope изменения.

Не добавляй tidy-check в старый проект автоматически: разные версии Go, платформы и build tags могут менять `go.mod`/`go.sum`. Для Codex-задач запускай `go mod tidy` только если проект уже проверяет tidy в CI, если ты менял imports/dependencies, или если пользователь явно просит.

`gofmt` check:

```bash
test -z "$(git ls-files -z '*.go' | xargs -0 gofmt -l)"
```

Если проект не в git или содержит generated/vendor Go-файлы, используй список файлов из scope проекта, а не весь родительский каталог.

---

## 16. GoReleaser

Для CLI/бинарников используй GoReleaser, если нужны:

- сборки под несколько OS/ARCH;
- архивы;
- checksums;
- GitHub/GitLab releases;
- Docker images;
- Homebrew/nfpm packages.

Минимальный `.goreleaser.yaml`:

```yaml
version: 2

before:
  hooks:
    - go mod tidy
    - go test ./...

builds:
  - id: myapp
    main: ./cmd/myapp
    binary: myapp
    # env:
    #   - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}

archives:
  - formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: "checksums.txt"

changelog:
  sort: asc
```

Перед реальным release:

```bash
goreleaser check
goreleaser release --snapshot --clean
goreleaser release --clean
```

Если переменные версии объявлены не в package `main`, замени `main.version`, `main.commit`, `main.date` на полный import path соответствующего пакета. `CGO_ENABLED=0` добавляй только после проверки, что проект и target runtime не требуют CGO.

---

## 17. GoLand / IDE перед commit

Если работа ведётся в GoLand, настрой проект так:

- Включить Go SDK нужной версии.
- Включить `gofmt`/`goimports` on save.
- Включить инспекции GoLand для Go Vet/static issues.
- Настроить File Watcher или External Tool для `golangci-lint`, если команда использует его локально.
- Запускать тесты пакета из IDE, но финальную проверку делать командами из checklist.
- Проверить Run Configuration с нужными env/config.
- Не коммитить `.idea` целиком, если команда не договорилась об этом. Обычно коммитят только shared config, если он нужен.

---

## 18. Makefile

Рекомендуемый Makefile:

```makefile
APP := myapp
PKG := ./cmd/$(APP)
BIN := bin/$(APP)
VERSION ?= dev
VERSION_PKG ?= main
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: fmt tidy vet lint test race vuln build check release-check clean

fmt:
	# Exclude generated/vendor files here if the project commits them.
	git ls-files -z '*.go' | xargs -0 gofmt -w

tidy:
	go mod tidy

vet:
	go vet ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

race:
	go test -race ./...

vuln:
	govulncheck ./...

build:
	go build -trimpath \
		-ldflags "-s -w -X $(VERSION_PKG).version=$(VERSION) -X $(VERSION_PKG).commit=$(COMMIT) -X $(VERSION_PKG).date=$(DATE)" \
		-o $(BIN) $(PKG)

check: fmt vet test
	git diff --exit-code

release-check: tidy check lint race vuln

clean:
	rm -rf bin coverage.out
```

В проектах без git или с generated/vendor Go-файлами замени `fmt` на явный список файлов/пакетов. Если нужен статический Linux-бинарник и проект не зависит от CGO, добавь `CGO_ENABLED=0` к `build`. Если `lint`, `race` или `vuln` недоступны локально, оставь их в отдельной release/CI-проверке, а не блокируй быстрый `make check`.

---

## 19. Инструкция для Codex при изменении проекта

Когда Codex получает задачу по Go-проекту, он должен действовать так:

### 19.1 Перед изменениями

1. Определить scope задачи и ближайший `go.mod`; в monorepo не выходить за нужный модуль без причины.
2. Прочитать релевантные `README.md`, `go.mod`, `Makefile`, `.golangci.yml`, Dockerfile, CI config, если они есть.
3. Найти точку входа: `main.go`, `cmd/*/main.go` или существующий package API.
4. Понять границы пакетов и существующий стиль.
5. Не менять архитектуру без необходимости.
6. Не добавлять новую зависимость, если задачу можно решить стандартной библиотекой.
7. Если зависимость нужна — объяснить зачем.

### 19.2 Во время изменений

1. Следовать существующему стилю проекта.
2. Минимизировать diff.
3. Не смешивать рефакторинг и фичу без необходимости.
4. Добавлять тесты рядом с изменённой логикой.
5. Добавлять context/timeout для I/O операций.
6. Оборачивать ошибки с полезным контекстом.
7. Не логировать секреты.
8. Не создавать `utils/common/helpers`.
9. Не добавлять goroutine без механизма остановки.
10. Не добавлять глобальное mutable state без причины.

### 19.3 После изменений

Codex должен попытаться выполнить в целевом модуле:

```bash
go list -f '{{.Dir}}' ./... | xargs gofmt -w
go vet ./...
go test ./...
```

Если доступно и уместно для scope:

```bash
go mod tidy
golangci-lint run ./...
go test -race ./...
govulncheck ./...
```

`go mod tidy` запускай после изменения imports/dependencies или если проект уже использует tidy-check. Не делай tidy механически в старом проекте без причины.

Если команда не выполнилась, Codex должен явно написать:

- какая команда не выполнилась;
- почему;
- что уже проверено;
- что должен проверить человек.

Если repo-wide проверки шумят из-за существующих проблем вне scope, перейди на targeted validation и явно отдели pre-existing failures от результата своей правки.

---

## 20. Code review checklist

Перед merge проверь:

### Код

- [ ] Изменённые Go-файлы отформатированы `gofmt`.
- [ ] Имена понятные и идиоматичные.
- [ ] Ошибки не игнорируются.
- [ ] Ошибки оборачиваются с контекстом.
- [ ] Нет лишнего global state.
- [ ] Нет `panic` для штатных ошибок.
- [ ] Нет преждевременной абстракции.
- [ ] Нет пакетов `utils/common/helpers` без сильной причины.

### Concurrency

- [ ] У каждой goroutine есть shutdown path.
- [ ] Каналы закрываются владельцем.
- [ ] Shared state защищён mutex/channel ownership.
- [ ] Пройден `go test -race ./...` или объяснено, почему нет.

### Тесты

- [ ] Добавлены/обновлены unit tests.
- [ ] Покрыты edge cases.
- [ ] Покрыты error paths.
- [ ] Интеграционные тесты отделены tags/окружением и не требуют production-секретов.

### Security

- [ ] Нет секретов в коде, логах, тестовых fixture'ах.
- [ ] Внешний ввод валидируется.
- [ ] `govulncheck ./...` выполнен или результат описан.

### Release

- [ ] Версия/commit/date встраиваются или доступны.
- [ ] Конфиг задокументирован.
- [ ] Миграции проверены.
- [ ] Backward compatibility проверена.
- [ ] Docker/CI build проходит или описано, почему локально не проверялся.

---

## 21. Источники

Основные источники, на которых основаны правила:

- Effective Go: https://go.dev/doc/effective_go
- Go Code Review Comments: https://go.dev/wiki/CodeReviewComments
- Organizing a Go module: https://go.dev/doc/modules/layout
- Managing dependencies: https://go.dev/doc/modules/managing-dependencies
- Go Doc Comments: https://go.dev/doc/comment
- Data Race Detector: https://go.dev/doc/articles/race_detector
- Go Fuzzing: https://go.dev/doc/security/fuzz/
- govulncheck tutorial: https://go.dev/doc/tutorial/govulncheck
- govulncheck package docs: https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck
- Structured logging with slog: https://go.dev/blog/slog
- Google Go Style Guide: https://google.github.io/styleguide/go/
- Google Go Style Best Practices: https://google.github.io/styleguide/go/best-practices.html
- Google Go Style Decisions: https://google.github.io/styleguide/go/decisions.html
- Uber Go Style Guide: https://github.com/uber-go/guide
- golangci-lint configuration: https://golangci-lint.run/docs/configuration/
- GoReleaser documentation: https://goreleaser.com/
- GoReleaser checksums: https://goreleaser.com/customization/package/checksum/
