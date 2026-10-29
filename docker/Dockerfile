# Linux-вариант лабы для тех, у кого под рукой только macOS: Docker Desktop
# крутит контейнеры на своём ядре Linux, и сокеты ведут себя по-линуксовому.
FROM golang:1.27.3-trixie

RUN apt-get update \
 && apt-get install -y --no-install-recommends lsof iproute2 tcpdump procps make ripgrep curl \
 && rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:0.13.6 /uv /uvx /usr/local/bin/
ENV UV_PYTHON_INSTALL_DIR=/opt/uv-python \
    UV_PROJECT_ENVIRONMENT=/opt/venv \
    UV_LINK_MODE=copy

WORKDIR /lab
COPY python/pyproject.toml python/uv.lock python/.python-version python/
RUN cd python && uv sync --frozen

COPY go go
RUN cd go && go build -o ../bin/ ./cmd/...

COPY . .
CMD ["make", "help"]
