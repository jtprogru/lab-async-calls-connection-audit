# Лаба к посту «Кто открыл и не закрыл»: https://jtprog.ru/posts/async-calls-connection-audit/
#
# Каждая цель go-* и py-* сама поднимает target и гасит его после прогона.
# Порты по умолчанию 18080 (target) и 18081 (статистика), меняются через
# LAB_PORT и LAB_STATS.

SHELL := /bin/bash
FOR ?= 4m
IDLE ?= 2s
GOLAB := bin/golab
WITH := scripts/with-target.sh
PY := cd python && uv run --frozen
DOCKER_IMAGE ?= lab-async-calls
DOCKER_RUN := docker run --rm --cap-add NET_ADMIN --cap-add NET_RAW

.DEFAULT_GOAL := help

.PHONY: help
help: ## Показать цели
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## ---------------------------------------------------------------- сборка

.PHONY: build
build: ## Собрать target и golab в bin/
	cd go && go build -o ../bin/ ./cmd/...

.PHONY: py-sync
py-sync: ## Поставить Python и зависимости через uv
	cd python && uv sync --frozen

.PHONY: target
target: build ## Запустить target в foreground (IDLE=2s по умолчанию)
	bin/target -idle $(IDLE)

## ---------------------------------------------------------------- Go

.PHONY: go-pool
go-pool: build ## Пул на каждый вызов: Transport на вызов, Client на вызов, общий клиент
	@$(WITH) 0 $(GOLAB) transport-per-call
	@$(WITH) 0 $(GOLAB) client-per-call
	@$(WITH) 0 $(GOLAB) shared

.PHONY: go-dump
go-dump: build ## То же с дампом горутин: пары readLoop/writeLoop на каждое соединение
	@$(WITH) 0 $(GOLAB) -dump transport-per-call

.PHONY: go-no-close
go-no-close: build ## Тело не закрыто, Client.Timeout=5s: CLOSE_WAIT, потом таймер всё прибивает
	@$(WITH) 2s $(GOLAB) no-close

.PHONY: go-no-close-long
go-no-close-long: build ## Тело не закрыто, DefaultClient: таймлайн CLOSE_WAIT → RST на FOR (4m)
	@$(WITH) 2s $(GOLAB) -raw -for $(FOR) no-close-default

.PHONY: go-probe
go-probe: build ## Одно соединение: точная секунда RST и SO_ERROR (KA=-1s выключает keepalive)
	@$(WITH) 2s $(GOLAB) -for 100s $(if $(KA),-ka $(KA)) probe

.PHONY: go-body
go-body: build ## Закрыть ещё не значит дочитать: 3 байта и мегабайт
	@$(WITH) 0 $(GOLAB) read-no-close
	@$(WITH) 0 $(GOLAB) close-no-read
	@$(WITH) 0 $(GOLAB) close-no-read-big

.PHONY: go-tw
go-tw: build ## TIME_WAIT: вежливое закрытие против Close недочитанного мегабайта
	@$(WITH) 0 $(GOLAB) -tw graceful-close
	@$(WITH) 0 $(GOLAB) -tw close-no-read-big

.PHONY: go-hang
go-hang: build ## Вызов без таймаута: висит 30 секунд и дальше
	@$(WITH) 0 $(GOLAB) hang

.PHONY: go-errors
go-errors: build ## Четыре таймаута и их сообщения в логах
	@$(WITH) 0 $(GOLAB) errors

.PHONY: go-maxconns
go-maxconns: build ## Пул кончился: «awaiting headers», хотя запрос не ушёл
	@$(WITH) 0 $(GOLAB) maxconns
	@$(WITH) 0 $(GOLAB) maxconns-notimeout

.PHONY: go-all
go-all: go-pool go-no-close go-body go-tw go-hang go-errors go-maxconns ## Все быстрые Go-режимы

## ---------------------------------------------------------------- Python

.PHONY: py-pool
py-pool: build ## httpx: клиент на вызов против общего
	@$(WITH) 0 bash -c '$(PY) python pylab.py pool'

.PHONY: py-unclosed
py-unclosed: build ## httpx без aclose: соединения живут до GC
	@$(WITH) 0 bash -c '$(PY) python -W always::ResourceWarning pylab.py unclosed'

.PHONY: py-pool-timeout
py-pool-timeout: build ## Незакрытый стрим и PoolTimeout
	@$(WITH) 0 bash -c '$(PY) python pylab.py pool-timeout'

.PHONY: py-fire-and-forget
py-fire-and-forget: build ## create_task без ссылки: ошибка уходит в лог asyncio
	@$(WITH) 0 bash -c '$(PY) python pylab.py fire-and-forget'

.PHONY: py-cancel
py-cancel: build ## Отмена не доходит до фоновой задачи: create_task против TaskGroup
	@$(WITH) 0 bash -c '$(PY) python pylab.py cancel'

.PHONY: py-blocking
py-blocking: build ## requests в async def, с debug-режимом asyncio
	@$(WITH) 0 bash -c '$(PY) python pylab.py blocking'
	@$(WITH) 0 bash -c '$(PY) python -X dev pylab.py blocking 2>&1 | grep -m3 Executing'

.PHONY: py-no-timeout
py-no-timeout: build ## requests без timeout висит вечно
	@$(WITH) 0 bash -c '$(PY) python pylab.py no-timeout'

.PHONY: py-callgraph
py-callgraph: build ## Python 3.14: кто кого ждёт, asyncio.print_call_graph
	@$(WITH) 0 bash -c '$(PY) python callgraph.py'

.PHONY: py-test
py-test: build ## pytest по умолчанию: оба теста зелёные
	@$(WITH) 0 bash -c '$(PY) pytest -q'

.PHONY: py-test-strict
py-test-strict: build ## pytest с -W error: тест с утёкшим клиентом краснеет (выход 1 ожидаем)
	@$(WITH) 0 bash -c '$(PY) pytest -q -W error::ResourceWarning -W error::pytest.PytestUnraisableExceptionWarning'

.PHONY: py-all
py-all: py-pool py-unclosed py-pool-timeout py-fire-and-forget py-cancel py-blocking py-callgraph py-test ## Все Python-режимы

## ---------------------------------------------------------------- синтетический сервис

.PHONY: service-inventory
service-inventory: ## Инвентаризация точек выхода в сеть: оба rg из шага 1
	@cd service && echo '## Go' && rg -n --sort path --type go \
	  -e 'http\.(Get|Post|PostForm|Head)\(' -e 'http\.DefaultClient' \
	  -e '&http\.(Client|Transport)\{' -e 'http\.NewRequest' \
	  -e 'net\.Dial' -e 'sql\.Open\(' -e 'grpc\.(Dial|NewClient)\(' \
	  -e '\bgo [a-zA-Z.]+\(' .
	@cd service && echo '## Python' && rg -n --sort path --type py \
	  -e 'requests\.(get|post|put|patch|delete|request|Session)\(' \
	  -e 'httpx\.(AsyncClient|Client|get|post)\(' -e 'aiohttp\.ClientSession\(' \
	  -e 'asyncio\.(create_task|ensure_future)\(' -e 'open_connection\(' .

.PHONY: service-lint
service-lint: ## go vet, golangci-lint (bodyclose, noctx, errcheck, contextcheck) и ruff
	-@cd service && echo '## go vet' && go vet ./...
	-@cd service && echo '## golangci-lint' && golangci-lint run ./...
	-@cd service && echo '## golangci-lint --uniq-by-line=false' && golangci-lint run --uniq-by-line=false ./...
	-@cd service && echo '## ruff' && uvx ruff@0.17.2 check .

## ---------------------------------------------------------------- прогоны целиком

.PHONY: results
results: build ## Прогнать всё и сложить выводы в results/<ос>/ (долго: FOR на длинные режимы)
	scripts/run-all.sh results/$$(uname | tr A-Z a-z | sed s/darwin/macos/)

## ---------------------------------------------------------------- Docker (Linux на macOS)

.PHONY: docker-build
docker-build: ## Собрать образ с Go 1.27.3, Python 3.14 и сетевыми утилитами
	docker build -f docker/Dockerfile -t $(DOCKER_IMAGE) .

.PHONY: docker-shell
docker-shell: docker-build ## Шелл в контейнере: дальше те же make-цели
	$(DOCKER_RUN) -it $(DOCKER_IMAGE) bash

.PHONY: docker-go
docker-go: docker-build ## Быстрые Go-режимы на ядре Linux
	$(DOCKER_RUN) $(DOCKER_IMAGE) make -s go-all

.PHONY: docker-long
docker-long: docker-build ## Таймлайн CLOSE_WAIT → RST на ядре Linux, с tcpdump
	$(DOCKER_RUN) $(DOCKER_IMAGE) bash -c 'make -s go-no-close-long FOR=$(FOR) & LAB_PORT=18098 LAB_STATS=18099 scripts/with-target.sh 2s scripts/rst-capture.sh 18098 bin/golab -for 100s probe; wait'

.PHONY: docker-results
docker-results: docker-build ## Прогнать всё в контейнере и сложить в results/docker/
	mkdir -p results/docker
	$(DOCKER_RUN) -v $(CURDIR)/results/docker:/out -e FOR=$(FOR) -e SETS="$(or $(SETS),go py long)" $(DOCKER_IMAGE) scripts/run-all.sh /out

.PHONY: clean
clean: ## Удалить bin/ и python/.venv
	rm -rf bin python/.venv
