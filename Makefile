# rerun — developer workflow targets.
#
#   make            vet + race tests + mutation gate (the local bar)
#   make test       go test ./...
#   make race       go test -race ./...
#   make vet        go vet ./...
#   make cover      coverage summary
#   make mutate     mutation gate (5 killed, 1 documented equivalent)
#   make pg-test    Postgres store contract against an ephemeral container

.PHONY: all test race vet cover mutate pg-test

all: vet race mutate

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

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
