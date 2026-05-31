# syntax=docker/dockerfile:1.7
#
# Python service image. Two stages: builder installs deps via uv into a venv,
# runtime copies just the venv and source into a slim image.

ARG PYTHON_VERSION=3.14.5

FROM python:${PYTHON_VERSION}-slim-bookworm AS build

COPY --from=ghcr.io/astral-sh/uv:0.5.6 /uv /usr/local/bin/uv

WORKDIR /app

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never \
    VIRTUAL_ENV=/app/.venv \
    PATH="/app/.venv/bin:$PATH"

# Cache dep layer separately from source.
COPY pyproject.toml uv.lock* ./
RUN uv venv /app/.venv && uv sync --no-dev --no-install-project

# Install the project itself.
COPY . .
RUN uv pip install --no-deps -e .


FROM python:${PYTHON_VERSION}-slim-bookworm AS runtime
WORKDIR /app

RUN groupadd --gid 10001 app && useradd --uid 10001 --gid app --no-create-home app

COPY --from=build /app /app

ENV VIRTUAL_ENV=/app/.venv \
    PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PY_PORT=5000

USER app
EXPOSE 5000
ENTRYPOINT ["uvicorn", "medha.api:app", "--host", "0.0.0.0", "--port", "5000"]
