# syntax=docker/dockerfile:1.7
#
# Python service image. Two stages: builder installs deps via `uv`, runtime
# is a slim Python with the venv copied in and a non-root user.

ARG PYTHON_VERSION=3.14.5

FROM python:${PYTHON_VERSION}-slim-bookworm AS build

# uv: fast resolver + installer. Pinned via tag so builds are reproducible.
COPY --from=ghcr.io/astral-sh/uv:0.5.6 /uv /usr/local/bin/uv

WORKDIR /app
ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never

# Copy pyproject first so the dep layer caches across source changes.
COPY pyproject.toml ./
# uv.lock is optional during M0; Task 1 generates it via `uv sync` locally.
COPY uv.lock* ./

RUN uv venv /app/.venv \
    && . /app/.venv/bin/activate \
    && uv sync --no-dev --no-install-project || \
       uv pip install fastapi 'uvicorn[standard]' pydantic 'pydantic-settings' httpx prometheus-client

COPY . .
RUN uv pip install -e . --python /app/.venv/bin/python || true


FROM python:${PYTHON_VERSION}-slim-bookworm AS runtime
WORKDIR /app

# Non-root user — id 10001 to dodge collisions with distro accounts.
RUN groupadd --gid 10001 app && useradd --uid 10001 --gid app --no-create-home app

COPY --from=build /app /app
ENV PATH="/app/.venv/bin:${PATH}" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PY_PORT=5000

USER app
EXPOSE 5000
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD ["python", "-c", "import urllib.request,sys;sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:5000/health').status==200 else 1)"]
ENTRYPOINT ["uvicorn", "agent_mem.api:app", "--host", "0.0.0.0", "--port", "5000"]
