# SUP Rental

SUP Rental — учебное приложение для учёта оборудования и аренды SUP-досок.

Проект разрабатывается для практического изучения Go и настройки совместной работы с Codex в VS Code.

## Статус проекта

Этап подготовки репозитория и настройки Codex завершён.

Сейчас выполняется технический вертикальный срез. В проекте уже есть HTTP-сервер,
простая HTML-страница состояния, health endpoint и обязательное подключение к
PostgreSQL:

```text
GET /
GET /health
```

Добавлен отдельный запуск миграций PostgreSQL и техническая baseline-миграция.
Приложение можно запустить вместе с PostgreSQL через Docker Compose.
Бизнес-таблицы и бизнес-функции пока не добавлены.

## Подтверждённые требования

На текущем этапе определено следующее:

* с программой работает один администратор;
* учитываются SUP-доски, вёсла и спасательные жилеты;
* каждый предмет имеет уникальный инвентарный номер;
* предварительные состояния оборудования: доступен, забронирован, выдан и списан;
* для клиента хранятся ФИО и номер телефона;
* история аренд должна сохраняться;
* аренда является почасовой;
* одна аренда может включать несколько SUP-досок;
* скидки не используются;
* доступ к приложению через интернет не требуется.

Подробные правила аренды, бронирования, возврата, оплаты и обслуживания будут определяться постепенно.

## Техническое направление

Планируемый технический стек:

* Go;
* PostgreSQL;
* `tern` для миграций PostgreSQL;
* стандартный пакет `log/slog`;
* серверный HTML через `html/template`;
* Docker;
* Docker Compose.

Приложение будет разрабатываться как модульный монолит с явной передачей зависимостей через конструкторы.

На начальном этапе не планируется использовать микросервисы, Kubernetes, брокеры сообщений и отдельное SPA-приложение.

## Принципы разработки

Разработка ведётся небольшими, законченными и проверяемыми инкрементами.

Каждое существенное архитектурное решение должно быть объяснено. Новые библиотеки и инфраструктурные компоненты добавляются только при наличии конкретной необходимости.

Бизнес-правила, которые ещё не согласованы, не должны самостоятельно придумываться и закрепляться в коде.

## Документация

План разработки находится в файле:

```text
docs/development-plan.md
```

Инструкции для Codex находятся в файле:

```text
AGENTS.md
```

## Go-модуль

```text
github.com/Dyuzhovsergey/sup-rental
```

## Локальный запуск

Приложение использует обязательные переменные окружения:

* `HTTP_ADDRESS` — адрес в формате `host:port`, например `:8080`;
* `HTTP_READ_HEADER_TIMEOUT` — максимальное время чтения заголовков запроса
  в формате Go `time.Duration`, например `5s`;
* `HTTP_SHUTDOWN_TIMEOUT` — максимальное время ожидания активных запросов
  при корректном завершении сервера, например `10s`;
* `DATABASE_URL` — connection string локальной PostgreSQL;
* `DB_CONNECT_TIMEOUT` — максимальное время подключения к PostgreSQL,
  например `5s`.

Несекретный пример значений находится в `.env.example`.

### Подготовка локальной PostgreSQL

Перед первым запуском создайте пользователя и базу данных. Выполните в `psql`
от имени администратора PostgreSQL:

```sql
CREATE ROLE sup_rental WITH LOGIN PASSWORD 'sup_rental';
CREATE DATABASE sup_rental OWNER sup_rental;
```

Connection string не создаёт пользователя, базу данных или таблицы
автоматически.

### Переменные окружения

Создайте локальный `.env` из примера и загрузите значения в текущий shell:

```bash
cp .env.example .env
set -a
source .env
set +a
go run ./cmd/server
```

`set -a` включает автоматический export переменных, а `set +a` выключает его
после загрузки `.env`. Файл `.env` не сохраняется в Git.

После успешного подключения появляется log `connected to PostgreSQL`.

В браузере откройте страницу состояния:

```text
http://localhost:8080/
```

В другом терминале страницу и health endpoint можно проверить командами:

```bash
curl -i http://localhost:8080/
curl -i http://localhost:8080/health
```

Оба запроса должны вернуть `200 OK`. Тело ответа `/health`:

```text
ok
```

Для корректного завершения сервера нажмите `Ctrl+C`. Сервер прекратит принимать
новые подключения, дождётся активных запросов в пределах
`HTTP_SHUTDOWN_TIMEOUT` и завершится с кодом `0`.

Переменная `TEST_DATABASE_URL` используется только integration-тестом
подключения PostgreSQL. Без неё тест явно пропускается.

### Миграции PostgreSQL

Миграции выполняются отдельно от запуска HTTP-сервера с помощью `tern`.
Это делает изменение схемы явной операцией и не требует добавлять migration tool
в runtime-зависимости приложения.

Установите проверенную для проекта версию `tern`:

```bash
go install github.com/jackc/tern/v2@v2.4.0
```

После загрузки переменных из `.env` примените миграции:

```bash
tern migrate --migrations migrations
```

Текущий статус можно проверить командой:

```bash
tern status --migrations migrations
```

Первая baseline-миграция не создаёт бизнес-таблиц. `tern` создаёт только
служебную таблицу `public.schema_version`, в которой хранит номер применённой
миграции.

Для проверки обратимости первой миграции можно откатить её и применить снова:

```bash
tern migrate --migrations migrations --destination 0
tern migrate --migrations migrations
```

### Docker image

Приложение собирается через multi-stage Dockerfile. Финальный image содержит
только скомпилированный сервер, CA-сертификаты и минимальное runtime-окружение.
Процесс запускается от непривилегированного пользователя `app`.

Соберите image:

```bash
docker build -t sup-rental:dev .
```

Пока PostgreSQL работает непосредственно на Linux host, контейнер можно
запустить через host network:

```bash
docker run --rm \
    --name sup-rental-server \
    --network host \
    --env-file .env \
    sup-rental:dev
```

Такой запуск позволяет использовать текущий `localhost:5432` из
`DATABASE_URL`. После появления Docker Compose приложение будет обращаться к
PostgreSQL по имени сервиса внутри Compose-сети.

Перед запуском освободите порт `8080`, если приложение уже запущено через
`go run`. Для разовой проверки на другом порту можно переопределить только адрес:

```bash
docker run --rm \
    --name sup-rental-server \
    --network host \
    --env-file .env \
    --env HTTP_ADDRESS=127.0.0.1:18080 \
    sup-rental:dev
```

В другом терминале проверьте приложение:

```bash
curl -i http://localhost:8080/
curl -i http://localhost:8080/health
```

Для остановки нажмите `Ctrl+C`. Docker передаст сигнал приложению, после чего
должен выполниться graceful shutdown.

Проверить, что image запускается не от `root`, можно командой:

```bash
docker run --rm --entrypoint id sup-rental:dev
```

Dockerfile не запускает миграции внутри процесса приложения и не помещает
`.env` в image. При одиночном запуске application image миграции применяются с
host, а при запуске через Docker Compose их выполняет отдельный сервис
`migrate`.

### Docker Compose

Docker Compose запускает три сервиса:

```text
postgres → migrate → app
```

`postgres` хранит данные в named volume, `migrate` применяет миграции через
`tern`, а `app` запускается только после готовности PostgreSQL и успешного
завершения миграций.

Убедитесь, что в локальном `.env` заданы Compose-переменные из `.env.example`:

```dotenv
COMPOSE_HTTP_PORT=8080
POSTGRES_USER=sup_rental
POSTGRES_PASSWORD=sup_rental
POSTGRES_DB=sup_rental
```

Соберите и запустите стек:

```bash
docker compose up --build -d
docker compose ps -a
docker compose logs migrate
```

Ожидаемое состояние:

* `postgres` — `healthy`;
* `migrate` — `Exited (0)`;
* `app` — `healthy`.

Проверьте применённую версию миграции:

```bash
docker compose exec postgres \
    psql -U sup_rental -d sup_rental \
    -c "SELECT version FROM public.schema_version;"
```

Проверьте HTTP-маршруты:

```bash
curl -i http://localhost:8080/
curl -i http://localhost:8080/health
```

Остановите контейнеры обычной командой:

```bash
docker compose down
```

Named volume с PostgreSQL сохраняется после `docker compose down`. Команда
`docker compose down -v` удаляет volume вместе с данными и не должна выполняться
без осознанного решения и резервной копии.

PostgreSQL-порт `5432` не публикуется на host. Контейнеры обращаются к базе по
внутреннему hostname `postgres`, поэтому Compose не конфликтует с локальной
PostgreSQL.

## Проверка

Применимые проверки выполняются в следующем порядке:

```bash
go fmt ./...
go vet ./...
go test ./...
go build ./...
docker compose config --quiet
```
