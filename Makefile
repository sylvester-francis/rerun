# rerun — developer workflow targets.
#
#   make            vet + lint + race tests + mutation gate + doc-check (local bar)
#   make test       go test ./...
#   make race       go test -race ./...
#   make vet        go vet ./...
#   make lint       gofmt cleanliness + staticcheck
#   make doc-check  fail on any undocumented exported symbol
#   make cover      coverage summary
#   make mutate     mutation gate (9 killed, 1 documented equivalent)
#   make pg-test    Postgres store contract against an ephemeral container

# Pinned so CI and local runs agree; staticcheck is a tool, not a module dependency.
STATICCHECK_VERSION ?= 2025.1.1

.PHONY: all test race vet lint doc-check cover mutate pg-test

all: vet lint race mutate doc-check

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

# Formatting and static-analysis gate: gofmt must be clean and staticcheck must
# pass. rerun's comments and docs use Unicode (em dashes, mermaid, the banner),
# so there is deliberately no ASCII-only gate.
lint:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi; \
	echo "gofmt: ok"
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

# Fail on any undocumented exported symbol (std-lib AST walker, no deps).
doc-check:
	go run ./tools/doccheck .

cover:
	go test -cover ./...

mutate:
	go run ./tools/mutate

# pg-test boots an ephemeral PostgreSQL in Docker, runs the Postgres store
# contract (build-tagged, real database) against it, then tears it down —
# leaving no container behind even when the tests fail.
pg-test:
	@docker rm -f rerun-pg >/dev/null 2>&1 || true
	@docker run -d --name rerun-pg \
		-e POSTGRES_USER=rerun -e POSTGRES_PASSWORD=rerun -e POSTGRES_DB=rerun \
		-p 55432:5432 postgres:16-alpine >/dev/null
	@echo "waiting for postgres to accept connections..."
	@for i in $$(seq 1 50); do \
		docker exec rerun-pg pg_isready -U rerun -d rerun >/dev/null 2>&1 && break; \
		sleep 0.3; \
	done
	@RERUN_PG_DSN="postgres://rerun:rerun@localhost:55432/rerun?sslmode=disable" \
		go test -tags postgres -race ./postgres/; rc=$$?; \
		docker rm -f rerun-pg >/dev/null 2>&1; exit $$rc
