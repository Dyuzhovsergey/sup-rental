# SUP Rental Repository Instructions

## Project Purpose

SUP Rental is an educational Go application for managing SUP rental equipment, customers, and hourly rentals.

The project has two goals:

1. Practice Go development through a real, gradually growing application.
2. Learn how to configure and use Codex safely inside a VS Code repository.

Develop the project through small, complete, understandable, and verifiable increments.

Do not attempt to implement the entire planned system at once.

## Current Confirmed Business Context

The currently confirmed requirements are:

* one administrator works with the application;
* the inventory includes SUP boards, paddles, and life jackets;
* every inventory item has a unique inventory number;
* preliminary equipment states are available, reserved, issued, and retired;
* a customer has a full name and phone number;
* rental history must be preserved;
* rentals are hourly;
* one rental may contain multiple SUP boards;
* discounts are not required;
* internet access is not required.

Treat these requirements as the current working context, not as a complete business specification.

Do not invent missing business rules.

Before implementing a business rule that has not been confirmed, describe the missing decision and request clarification.

## Development Approach

Work in small, logically complete increments.

Before making changes, report:

1. the goal of the increment;
2. why the change is needed;
3. which files will be created or changed;
4. which architectural decisions will be made;
5. relevant risks or alternatives.

Avoid unrelated refactoring.

Do not rewrite large parts of the project for a small change.

Do not move to the next major development stage before summarizing the current stage.

## Architecture

Use a simple modular monolith.

The preferred dependency direction is:

```text
HTTP handlers
      ↓
application services
      ↓
repository interfaces
      ↓
PostgreSQL repository implementations
```

Follow these rules:

* assemble application dependencies explicitly in the application entry point;
* use manual dependency injection through constructors;
* keep business logic independent from HTTP, PostgreSQL, Docker, and HTML templates;
* do not execute SQL in HTTP handlers;
* keep SQL queries inside PostgreSQL repository implementations;
* do not put HTTP response logic inside services or repositories;
* do not create an interface for every struct;
* define small interfaces close to the code that consumes them when appropriate;
* do not create abstractions only for possible future requirements;
* do not introduce microservices without a demonstrated need.

The entry point should remain responsible for constructing concrete dependencies and starting the application.

If dependency construction later becomes too large, propose a separate application assembly package before creating it.

## Go Code Guidelines

Use idiomatic and readable Go.

Follow these rules:

* format Go code before considering work complete;
* prefer the Go standard library when it reasonably solves the task;
* keep functions focused and reasonably small;
* use constructors when a type has required dependencies;
* make dependencies explicit;
* avoid mutable global state;
* use descriptive names;
* add comments for exported identifiers where appropriate;
* add a package comment when creating a new non-trivial package;
* prefer early returns when they make control flow clearer;
* avoid unnecessary cleverness;
* avoid premature optimization;
* do not hide important learning concepts behind generators or large frameworks.

When adding an important Go construct, explain:

* why it is used;
* how it works;
* where the data is copied or shared;
* how dependencies are passed;
* how errors propagate;
* how the code is tested.

## Context and Cancellation

Use `context.Context` for operations that may block or require cancellation, including:

* HTTP request processing;
* PostgreSQL queries;
* transactions;
* future external service calls;
* application shutdown operations where appropriate.

Pass `context.Context` explicitly.

For normal functions and methods, place it as the first parameter after the receiver.

Do not store request contexts inside long-lived structs.

Do not replace an available request context with `context.Background()` without a clear reason.

## Error Handling

Return errors to the layer that can make the appropriate decision.

Wrap errors with meaningful context by using `%w`, for example:

```go
return fmt.Errorf("create board: %w", err)
```

Do not discard errors.

Do not log the same error in every layer.

Infrastructure layers should add technical context. The application boundary should decide how the error is logged or converted into an HTTP response.

Do not expose internal database errors directly to an end user.

## Configuration and Secrets

Read application configuration from environment variables.

When environment variables are introduced:

* document them in README;
* add non-secret examples to `.env.example`;
* validate required values during application startup;
* return a clear error for invalid configuration.

Never commit:

* passwords;
* API keys;
* access tokens;
* real customer data;
* production connection strings;
* private certificates.

Do not place personal machine settings inside project configuration.

## PostgreSQL and Repositories

Use PostgreSQL for persistent application data.

Keep SQL inside PostgreSQL repository implementations.

Repository methods should accept `context.Context`.

Use explicit column lists in SQL queries.

Avoid `SELECT *`.

Wrap database errors with operation context.

Use transactions only when several operations must succeed or fail together.

Do not add a generic repository abstraction unless repeated real use cases demonstrate a need.

## Database Migrations

Store database migrations in the repository.

Create migrations only when a real schema change is required.

Use sequential and descriptive migration names.

Do not silently modify an already shared migration.

Create a new migration for later schema changes.

Before destructive schema operations, explain:

* which data may be lost;
* why the operation is necessary;
* whether a safer alternative exists;
* how the data can be backed up.

Do not drop tables, databases, or volumes without explicit user approval.

## HTTP Layer

HTTP handlers are responsible for:

* reading and validating transport input;
* calling an application service;
* mapping results to HTTP responses;
* selecting appropriate HTTP status codes;
* rendering templates when required.

HTTP handlers must not:

* contain SQL;
* construct infrastructure dependencies;
* implement complex business rules;
* expose internal error details.

Keep routing, handlers, services, and repositories separate.

## Frontend Direction

The initial frontend direction is server-side rendering with:

* Go `html/template`;
* normal HTML;
* simple CSS;
* minimal JavaScript.

Do not introduce React, Vue, another SPA framework, or HTMX without first comparing the available approaches and receiving approval.

Keep application services independent from HTML rendering so that the frontend can be replaced later.

## Dependency Management

Do not add a dependency only because it is popular.

Before adding a dependency, explain:

* which concrete problem it solves;
* why the standard library is insufficient;
* its maintenance and operational cost;
* whether a smaller alternative exists.

Do not add a dependency injection framework at the initial stage.

Do not add message brokers, Kubernetes, a full observability stack, or distributed system components without a practical requirement.

## Testing

Add tests that protect meaningful behavior.

Prefer:

* table-driven tests when they improve clarity;
* unit tests for application services and business rules;
* `httptest` for HTTP handlers;
* integration tests for PostgreSQL repositories;
* small test fixtures;
* explicit test names describing behavior.

Do not create tests that only reproduce implementation details without protecting behavior.

Do not claim that tests passed unless they were actually executed.

When a test cannot be executed, state that clearly.

Do not remove or weaken a test only to make a failing build pass.

## Verification Commands

Use the commands that are applicable to the current project state:

```bash
go fmt ./...
go vet ./...
go test ./...
go build ./...
```

After Docker Compose is introduced, also run:

```bash
docker compose config
```

Before reporting completion:

* inspect the changed files;
* run formatting;
* run relevant tests;
* run the relevant build command;
* report the exact commands executed;
* report any commands that could not be executed.

Do not report successful verification based only on code inspection.

## Docker

Use Docker and Docker Compose for local installation and future customer installation.

Keep the initial Docker setup simple.

Do not:

* delete Docker volumes without explicit approval;
* run `docker system prune`;
* run `docker volume prune`;
* run `docker compose down -v`;
* publish images without explicit instruction;
* add extra infrastructure containers without justification.

Explain how the Dockerfile works when it is introduced.

Explain how services communicate inside Docker Compose.

## Git Safety

Do not run the following actions without explicit user instruction:

* `git push`;
* force push;
* `git reset --hard`;
* history rewriting;
* branch deletion;
* tag deletion;
* remote repository modification;
* automatic merge;
* automatic release publication.

Do not create a Git commit unless explicitly requested.

At the end of an increment, recommend a commit message in this format:

```text
type(scope): short description
```

Prefer separate commits for independent changes.

## Documentation

Keep documentation synchronized with implemented behavior.

Update README when changing:

* startup commands;
* environment variables;
* Docker usage;
* migrations;
* project structure;
* testing commands.

Use the following documents when the corresponding information exists:

```text
README.md
docs/development-plan.md
docs/architecture.md
docs/deployment.md
.env.example
```

Do not create empty documentation files in advance.

Do not duplicate the complete README inside `AGENTS.md`.

## Final Change Report

After making changes, report:

1. what was implemented;
2. how the solution works;
3. which Go concepts were introduced;
4. which commands should be executed;
5. how to verify the result manually;
6. which automated tests should pass;
7. which limitations remain;
8. the single logical next increment;
9. the recommended Git commit message.

Every final response about this project must end with one clear next step.

## Current Restrictions

At the current project stage:

* do not create business entities before their requirements are agreed;
* do not create empty package directories;
* do not add PostgreSQL before the technical vertical slice;
* do not add Docker before the technical vertical slice;
* do not implement frontend pages before the frontend approach is approved;
* do not introduce authentication before its requirements are agreed;
* do not introduce microservices;
* do not introduce a message broker;
* do not introduce Kubernetes;
* do not introduce a full observability stack;
* do not create speculative abstractions.

Prefer the smallest working solution that supports the current agreed increment.
